package dostoevsky

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Mesh routes calls to local storage or the application's HTTP handler at a peer.
type Mesh struct {
	local  *Node
	peers  map[string]string
	token  string
	client *http.Client
}

func NewMesh(n *Node, peers map[string]string, token string, client *http.Client) (*Mesh, error) {
	if n == nil || len(token) < 16 {
		return nil, fmt.Errorf("%w: node and a token of at least 16 bytes required", ErrInvalid)
	}
	if client == nil {
		client = &http.Client{}
	}
	transport := http.Client{Transport: client.Transport, Timeout: client.Timeout, Jar: client.Jar}
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m := &Mesh{n, map[string]string{}, token, &transport}
	for _, id := range n.cfg.Nodes {
		if id == n.cfg.Node {
			continue
		}
		u, e := url.Parse(peers[id])
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("%w: URL for %s", ErrInvalid, id)
		}
		m.peers[id] = strings.TrimRight(u.String(), "/")
	}
	return m, nil
}

const PeerPath = "/dostoevsky/v1"

type request struct {
	Op, Owner, Topology string
	Partition
	Message
	Offset int64
	Group  string
	State  []byte
	Wait   bool
}
type reply struct {
	Record   *Record   `json:",omitempty"`
	Position *Position `json:",omitempty"`
	Error    string    `json:",omitempty"`
}

func (m *Mesh) execute(ctx context.Context, q request) (reply, error) {
	switch q.Op {
	case "publish":
		p, e := m.local.partition(q.Topic, q.Key)
		if e != nil || p != q.Partition {
			return reply{}, ErrInvalid
		}
		r, e := m.local.Publish(ctx, q.Topic, q.Message)
		return reply{Record: &r}, e
	case "read":
		r, e := m.local.Read(ctx, q.Partition, q.Offset, q.Wait)
		return reply{Record: &r}, e
	case "position":
		p, e := m.local.Position(ctx, q.Partition, q.Group)
		return reply{Position: &p}, e
	case "commit":
		return reply{}, m.local.Commit(ctx, q.Partition, q.Group, q.Offset, q.State)
	default:
		return reply{}, ErrInvalid
	}
}

var protocolErrors = []error{ErrInvalid, ErrOwner, ErrLimit, ErrClosed, ErrConflict, ErrEmpty, context.DeadlineExceeded, context.Canceled}

func (m *Mesh) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != PeerPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+m.token)) != 1 {
		w.WriteHeader(401)
		return
	}
	var q request
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrame))
	if e != nil || json.Unmarshal(b, &q) != nil {
		w.WriteHeader(400)
		return
	}
	if q.Owner != m.local.cfg.Node || q.Topology != m.local.topology {
		http.Error(w, "peer manifest mismatch", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, e := m.execute(ctx, q)
	if e != nil {
		out = reply{Error: "storage failure"}
		for _, v := range protocolErrors {
			if errors.Is(e, v) {
				out.Error = v.Error()
				break
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
func (m *Mesh) call(ctx context.Context, q request) (reply, error) {
	owner, e := m.local.owner(q.Partition)
	if e != nil {
		return reply{}, e
	}
	if owner == m.local.cfg.Node {
		return m.execute(ctx, q)
	}
	q.Owner = owner
	q.Topology = m.local.topology
	b, e := json.Marshal(q)
	if e != nil {
		return reply{}, e
	}
	if len(b) > maxFrame {
		return reply{}, ErrLimit
	}
	req, e := http.NewRequestWithContext(ctx, "POST", m.peers[owner]+PeerPath, bytes.NewReader(b))
	if e != nil {
		return reply{}, e
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/json")
	res, e := m.client.Do(req)
	if e != nil {
		return reply{}, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return reply{}, fmt.Errorf("peer %s: HTTP %d", owner, res.StatusCode)
	}
	var out reply
	b, e = io.ReadAll(io.LimitReader(res.Body, maxFrame+1))
	if e != nil {
		return reply{}, e
	}
	if len(b) > maxFrame {
		return reply{}, ErrLimit
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return reply{}, e
	}
	if out.Error != "" {
		for _, v := range protocolErrors {
			if out.Error == v.Error() {
				return reply{}, v
			}
		}
		return reply{}, fmt.Errorf("peer %s: %s", owner, out.Error)
	}
	return out, nil
}
func (m *Mesh) Publish(ctx context.Context, t string, msg Message) (Record, error) {
	if len(msg.Value) > MaxValue || len(msg.Key) > 4096 || len(msg.ID) > 256 {
		return Record{}, ErrLimit
	}
	if !validText(msg.Key, msg.ID) {
		return Record{}, ErrInvalid
	}
	p, e := m.local.partition(t, msg.Key)
	if e != nil {
		return Record{}, e
	}
	out, e := m.call(ctx, request{Op: "publish", Partition: p, Message: msg})
	return result(out, e)
}
func result(r reply, e error) (Record, error) {
	if e != nil {
		return Record{}, e
	}
	if r.Record == nil {
		return Record{}, errors.New("missing peer record")
	}
	return *r.Record, nil
}
func (m *Mesh) Read(ctx context.Context, p Partition, offset int64, wait bool) (Record, error) {
	for {
		out, e := m.call(ctx, request{Op: "read", Partition: p, Offset: offset, Wait: wait})
		if wait && e == context.DeadlineExceeded && ctx.Err() == nil {
			continue
		}
		return result(out, e)
	}
}
func (m *Mesh) Position(ctx context.Context, p Partition, g string) (Position, error) {
	out, e := m.call(ctx, request{Op: "position", Partition: p, Group: g})
	if e != nil {
		return Position{}, e
	}
	if out.Position == nil {
		return Position{}, errors.New("missing peer position")
	}
	return *out.Position, nil
}
func (m *Mesh) Commit(ctx context.Context, p Partition, g string, offset int64, state []byte) error {
	if len(state) > MaxValue {
		return ErrLimit
	}
	_, e := m.call(ctx, request{Op: "commit", Partition: p, Group: g, Offset: offset, State: state})
	return e
}
