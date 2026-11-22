// Package dostoevsky embeds durable, partitioned event streams in Go applications.
package dostoevsky

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid  = errors.New("invalid argument")
	ErrOwner    = errors.New("partition belongs to another node")
	ErrLimit    = errors.New("size limit exceeded")
	ErrClosed   = errors.New("node closed")
	ErrConflict = errors.New("checkpoint or message ID conflict")
	ErrEmpty    = errors.New("offset at end of log")
)

const MaxValue = 1 << 20

var name = regexp.MustCompile("^[a-zA-Z0-9_-]{1,96}$")

// Config fixes partition placement. Node identifies this process in the ordered Nodes list.
type Config struct {
	Node   string
	Nodes  []string
	Topics map[string]int
}
type Partition struct {
	Topic  string
	Number int
}
type Message struct {
	Key   string
	Value []byte
	ID    string
}
type Record struct {
	Partition
	Message
	Offset int64
	Time   time.Time
}

// Position holds the next offset and the most recently committed processor state.
type Position struct {
	Next  int64
	State []byte
}
type checkpoint struct {
	Expected *int64 `json:",omitempty"`
	Partition
	Group string
	Position
}
type stream struct {
	records []location
	ids     map[string]int64
	groups  map[string]Position
	changed chan struct{}
}

type Node struct {
	mu       sync.Mutex
	j        *journal
	cfg      Config
	topology string
	streams  map[Partition]*stream
	closed   bool
}

func (c Config) validate() error {
	seen := map[string]bool{}
	for _, n := range c.Nodes {
		if !name.MatchString(n) || seen[n] {
			return ErrInvalid
		}
		seen[n] = true
	}
	if !seen[c.Node] || len(c.Topics) == 0 {
		return ErrInvalid
	}
	for t, n := range c.Topics {
		if !name.MatchString(t) || n < 1 || n > 65536 {
			return ErrInvalid
		}
	}
	return nil
}

// Open creates or recovers a node. A directory can have one open writer.
func Open(dir string, c Config) (*Node, error) {
	if e := c.validate(); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return nil, e
	}
	var own Config
	if e = json.Unmarshal(raw, &own); e != nil {
		return nil, e
	}
	manifest := own
	manifest.Node = ""
	b, _ := json.Marshal(manifest)
	j, e := openJournal(dir)
	if e != nil {
		return nil, e
	}
	n := &Node{cfg: own, j: j, streams: make(map[Partition]*stream), topology: fmt.Sprintf("%x", sha256.Sum256(b))}
	var declared int
	e = j.recover(func(ev entry, loc location) error {
		if ev.Config != nil {
			saved := ev.Config
			if saved.Node != own.Node || !slices.Equal(saved.Nodes, own.Nodes) {
				return errors.New("data directory node layout mismatch")
			}
			for topic, count := range saved.Topics {
				if own.Topics[topic] != count {
					return fmt.Errorf("topic %s removed or resized", topic)
				}
			}
			declared = len(saved.Topics)
			return nil
		}
		if declared == 0 {
			return errors.New("missing manifest")
		}
		return n.index(ev, loc)
	})
	if e == nil && declared != len(own.Topics) {
		_, e = j.append(entry{Config: &own})
	}
	if e != nil {
		j.file.Close()
		return nil, e
	}
	return n, nil
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	n.closed = true
	for _, s := range n.streams {
		close(s.changed)
	}
	return n.j.file.Close()
}
func (n *Node) owner(p Partition) (string, error) {
	count := n.cfg.Topics[p.Topic]
	if p.Number < 0 || p.Number >= count {
		return "", ErrInvalid
	}
	return n.cfg.Nodes[p.Number%len(n.cfg.Nodes)], nil
}
func (n *Node) partition(t, key string) (Partition, error) {
	c := n.cfg.Topics[t]
	if c == 0 {
		return Partition{}, ErrInvalid
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return Partition{t, int(h.Sum32() % uint32(c))}, nil
}
func (n *Node) stream(p Partition) (*stream, error) {
	if n.closed {
		return nil, ErrClosed
	}
	owner, e := n.owner(p)
	if e != nil {
		return nil, e
	}
	if owner != n.cfg.Node {
		return nil, ErrOwner
	}
	s := n.streams[p]
	if s == nil {
		s = &stream{ids: map[string]int64{}, groups: map[string]Position{}, changed: make(chan struct{})}
		n.streams[p] = s
	}
	return s, nil
}
func (n *Node) index(e entry, loc location) error {
	if r := e.Record; r != nil {
		s, err := n.stream(r.Partition)
		if err != nil {
			return err
		}
		if r.Offset != int64(len(s.records)) {
			return ErrConflict
		}
		s.records = append(s.records, loc)
		if r.ID != "" {
			s.ids[r.ID] = r.Offset
		}
		close(s.changed)
		s.changed = make(chan struct{})
		return nil
	}
	if c := e.Check; c != nil {
		s, err := n.stream(c.Partition)
		if err != nil {
			return err
		}
		expected := c.Next - 1
		if c.Expected != nil {
			expected = *c.Expected
		}
		if expected != s.groups[c.Group].Next || c.Next <= expected || c.Next > int64(len(s.records)) {
			return ErrConflict
		}
		s.groups[c.Group] = Position{c.Next, bytes.Clone(c.State)}
		return nil
	}
	return errors.New("unknown journal entry")
}

// Publish routes by key. An optional ID makes retries idempotent within a partition.
func (n *Node) Publish(ctx context.Context, t string, m Message) (Record, error) {
	p, e := n.partition(t, m.Key)
	if e != nil {
		return Record{}, e
	}
	if len(m.Value) > MaxValue || len(m.ID) > 256 || len(m.Key) > 4096 {
		return Record{}, ErrLimit
	}
	if !validText(m.ID, m.Key) {
		return Record{}, ErrInvalid
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	s, e := n.stream(p)
	if e != nil {
		return Record{}, e
	}
	if e = ctx.Err(); e != nil {
		return Record{}, e
	}
	if off, ok := s.ids[m.ID]; m.ID != "" && ok {
		ev, e := n.j.read(s.records[off])
		if e != nil {
			return Record{}, e
		}
		r := ev.Record
		if r.Key != m.Key || !bytes.Equal(r.Value, m.Value) {
			return Record{}, ErrConflict
		}
		return *r, nil
	}
	r := Record{Partition: p, Message: m, Offset: int64(len(s.records)), Time: time.Now().UTC()}
	ev := entry{Record: &r}
	loc, e := n.j.append(ev)
	if e == nil {
		e = n.index(ev, loc)
	}
	return r, e
}

// Read returns one record. With wait=true it sleeps until the offset exists or ctx ends.
func (n *Node) Read(ctx context.Context, p Partition, offset int64, wait bool) (Record, error) {
	if offset < 0 {
		return Record{}, ErrInvalid
	}
	for {
		n.mu.Lock()
		s, e := n.stream(p)
		if e == nil {
			e = ctx.Err()
		}
		if e != nil {
			n.mu.Unlock()
			return Record{}, e
		}
		if offset < int64(len(s.records)) {
			ev, e := n.j.read(s.records[offset])
			n.mu.Unlock()
			if e != nil {
				return Record{}, e
			}
			return *ev.Record, nil
		}
		ch := s.changed
		n.mu.Unlock()
		if !wait {
			return Record{}, ErrEmpty
		}
		select {
		case <-ctx.Done():
			return Record{}, ctx.Err()
		case <-ch:
		}
	}
}
func (n *Node) Position(ctx context.Context, p Partition, g string) (Position, error) {
	if !name.MatchString(g) {
		return Position{}, ErrInvalid
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	s, e := n.stream(p)
	if e != nil {
		return Position{}, e
	}
	pos := s.groups[g]
	pos.State = bytes.Clone(pos.State)
	return pos, ctx.Err()
}

// Commit advances exactly one offset and atomically replaces this group's partition-local state.
func (n *Node) Commit(ctx context.Context, p Partition, g string, offset int64, state []byte) error {
	return n.CommitBatch(ctx, p, g, offset, offset+1, state)
}
func (n *Node) CommitBatch(ctx context.Context, p Partition, g string, offset, next int64, state []byte) error {
	if !name.MatchString(g) || offset < 0 || next <= offset {
		return ErrInvalid
	}
	if len(state) > MaxValue {
		return ErrLimit
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	s, e := n.stream(p)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if s.groups[g].Next != offset || next > int64(len(s.records)) {
		return ErrConflict
	}
	ev := entry{Check: &checkpoint{Partition: p, Group: g, Position: Position{next, state}}}
	if next != offset+1 {
		ev.Check.Expected = &offset
	}
	loc, e := n.j.append(ev)
	if e != nil {
		return e
	}
	return n.index(ev, loc)
}
func validText(ss ...string) bool {
	for _, s := range ss {
		if !utf8.ValidString(s) {
			return false
		}
	}
	return true
}
