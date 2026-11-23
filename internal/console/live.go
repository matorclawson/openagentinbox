package console

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Agent state and its timeline change in the same transaction, including enrollments via a host.
const liveSchema = `
CREATE TABLE IF NOT EXISTS roster(name TEXT PRIMARY KEY,ticket TEXT UNIQUE NOT NULL,host TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS lifecycle(id INTEGER PRIMARY KEY,kind TEXT NOT NULL,name TEXT NOT NULL,at INTEGER NOT NULL DEFAULT (unixepoch('subsec')*1000));
CREATE TRIGGER IF NOT EXISTS log_join AFTER INSERT ON enrollments BEGIN INSERT INTO lifecycle(kind,name) VALUES('Requested access',NEW.name); END;
CREATE TRIGGER IF NOT EXISTS log_enrolled AFTER INSERT ON agents BEGIN INSERT INTO lifecycle(kind,name) VALUES('Enrolled',NEW.name); END;
CREATE TRIGGER IF NOT EXISTS log_key AFTER UPDATE OF key ON agents WHEN NEW.key IS NOT OLD.key BEGIN INSERT INTO lifecycle(kind,name) VALUES(CASE WHEN NEW.key IS NULL THEN 'Access revoked' ELSE 'Credential replaced' END,NEW.name); END;
CREATE TRIGGER IF NOT EXISTS trim_lifecycle AFTER INSERT ON lifecycle BEGIN DELETE FROM lifecycle WHERE id<=NEW.id-128; END;`

// Broadcast invalidations only. SQLite supplies the current snapshot after reconnect.
type changes struct {
	watching map[string]int
	mu       sync.Mutex
	rev      uint64
	epoch    string
	ch       chan struct{}
	waiting  int
}

func (c *changes) cursor() string { return c.epoch + "-" + strconv.FormatUint(c.rev, 10) }
func (c *changes) signal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rev++
	if c.ch != nil {
		close(c.ch)
		c.ch = nil
	}
}
func (c *changes) wait(r *http.Request, cursor string) (string, error) {
	c.mu.Lock()
	if c.epoch == "" {
		c.epoch = token()
	}
	if cursor != c.cursor() {
		v := c.cursor()
		c.mu.Unlock()
		return v, nil
	}
	if c.waiting >= 64 {
		c.mu.Unlock()
		return "", failure{429, "Too many open workspace views"}
	}
	if c.ch == nil {
		c.ch = make(chan struct{})
	}
	ch := c.ch
	c.waiting++
	c.mu.Unlock()
	select {
	case <-ch:
	case <-r.Context().Done():
	case <-time.After(18 * time.Second):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiting--
	return c.cursor(), r.Context().Err()
}

// Presence counts only authenticated client watches currently held by this process.
func (c *changes) subscribe(name string) func() {
	c.mu.Lock()
	if c.watching == nil {
		c.watching = map[string]int{}
	}
	c.watching[name]++
	first := c.watching[name] == 1
	c.mu.Unlock()
	if first {
		c.signal()
	}
	return func() {
		c.mu.Lock()
		c.watching[name]--
		last := c.watching[name] == 0
		if last {
			delete(c.watching, name)
		}
		c.mu.Unlock()
		if last {
			c.signal()
		}
	}
}
func (c *changes) watchers() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := make(map[string]bool, len(c.watching))
	for name := range c.watching {
		v[name] = true
	}
	return v
}
func (s *server) liveRoutes(add func(string, handler)) {
	add("GET /api/desk/live", s.workspace)
	add("POST /api/desk/batch", s.batch)
	add("POST /api/desk/roster", s.grant)
	add("POST /api/desk/roster/cancel", s.cancelRoster)
	add("POST /api/agent/handshake", s.handshake)
	add("GET /api/agent/watch", s.watch)
}
func (s *server) workspace(_ http.ResponseWriter, r *http.Request) (any, error) {
	cursor, e := s.live.wait(r, r.URL.Query().Get("after"))
	if e != nil {
		return nil, e
	}
	if ok, e := s.signed(r); e != nil || !ok {
		return nil, failure{401, "Sign in to continue"}
	}
	data := object{"cursor": cursor, "peerLimit": s.peerLimit, "watching": s.live.watchers()}
	// Array-valued queries keep the wire format stable for an empty workspace.
	for key, query := range map[string]string{
		"pending":  "SELECT name,code,expires FROM enrollments WHERE expires>?1 ORDER BY expires,code",
		"agents":   "SELECT name,key IS NOT NULL FROM agents ORDER BY name",
		"activity": "SELECT kind,name,at FROM lifecycle ORDER BY id DESC LIMIT 12",
		"mail":     "SELECT m.subject,m.sender,d.seat,m.created_text,m.id FROM messages m JOIN deliveries d USING(seq) ORDER BY m.seq DESC LIMIT 8",
		"roster":   "SELECT substr(host,instr(host,'/')+1),count(*),min(expires),host FROM roster WHERE expires>?1 GROUP BY host ORDER BY host",
	} {
		var args []any
		if key == "pending" || key == "roster" {
			args = []any{now()}
		}
		rows, e := s.db.QueryContext(r.Context(), query, args...)
		if e != nil {
			return nil, e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return nil, e
		}
		items := [][]any{}
		for rows.Next() {
			values := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range values {
				ptr[i] = &values[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				rows.Close()
				return nil, e
			}
			items = append(items, values)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		data[key] = items
	}
	return data, nil
}

type requestRef struct{ Name, Code string }

func (s *server) batch(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		Requests []requestRef
		Approve  *bool
	}
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if q.Approve == nil || len(q.Requests) == 0 || len(q.Requests) > 512 {
		return nil, failure{400, "Choose between 1 and 512 requests"}
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	for _, item := range q.Requests {
		var key string
		if e = tx.QueryRow("DELETE FROM enrollments WHERE code=? AND name=? AND expires>? RETURNING key", item.Code, item.Name, now()).Scan(&key); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, failure{409, "Queue changed. Review the current requests."}
			}
			return nil, e
		}
		if *q.Approve {
			res, e := tx.Exec("INSERT OR IGNORE INTO agents VALUES(?,?)", item.Name, key)
			if e != nil {
				return nil, e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return nil, e
			}
			if n != 1 {
				return nil, failure{409, "Name already in use. Review the queue."}
			}
		} else {
			if _, e = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('Request dismissed',?)", item.Name); e != nil {
				return nil, e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"updated": len(q.Requests)}, nil
}
func rosterTicket(secret, name string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(name))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (s *server) grant(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		Host  string
		Names []string
	}
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if !nameRE.MatchString(q.Host) || len(q.Names) < 1 || len(q.Names) > 512 {
		return nil, failure{400, "Choose a host and 1–512 inbox names"}
	}
	secret := token()
	expiry := now() + 86400000
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM roster WHERE expires<=?", now()); e != nil {
		return nil, e
	}
	for _, n := range q.Names {
		if !nameRE.MatchString(n) || n == s.operator {
			return nil, failure{400, "Invalid agent name"}
		}
		var exists int
		if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM agents WHERE name=?)", n).Scan(&exists); e != nil {
			return nil, e
		}
		if exists != 0 {
			return nil, failure{409, "Inbox already exists: " + n}
		}
		res, e := tx.Exec("INSERT OR IGNORE INTO roster VALUES(?,?,?,?)", n, hash(rosterTicket(secret, n)), hash(secret)+"/"+q.Host, expiry)
		if e != nil {
			return nil, e
		}
		count, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		if count != 1 {
			return nil, failure{409, "Name already on a roster: " + n}
		}
	}
	if _, e = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('Host roster authorized',?)", q.Host); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"secret": secret, "expires": expiry}, nil
}
func (s *server) cancelRoster(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ ID string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var host string
	if e = tx.QueryRow("SELECT host FROM roster WHERE host=?", q.ID).Scan(&host); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("DELETE FROM roster WHERE host=?", host); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('Unclaimed tickets canceled',substr(?,instr(?,'/')+1))", host, host); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"ok": true}, nil
}
func (s *server) handshake(_ http.ResponseWriter, r *http.Request) (any, error) {
	b := bearer(r)
	if b == "" {
		return nil, failure{403, "Host grant required"}
	}
	rows, e := s.db.QueryContext(r.Context(), "SELECT name FROM roster WHERE host GLOB ? AND expires>? ORDER BY name", hash(b)+"/*", now())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		names = append(names, n)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(names) == 0 {
		return nil, failure{403, "No remaining names on this grant"}
	}
	return object{"names": names}, nil
}
func (s *server) watch(_ http.ResponseWriter, r *http.Request) (any, error) {
	seat, e := s.actor(r)
	if e != nil {
		return nil, e
	}
	after, e := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if e != nil || after < 0 {
		return nil, failure{400, "Invalid cursor"}
	}
	w, e := s.store.WatchSeat(seat, false)
	if e != nil {
		return nil, failure{429, "Too many watchers"}
	}
	defer w.Close()
	defer s.live.subscribe(seat)()
	var latest int64
	read := func() error {
		return s.db.QueryRowContext(r.Context(), "SELECT coalesce(max(seq),0) FROM deliveries WHERE seat=?", seat).Scan(&latest)
	}
	if e = read(); e != nil {
		return nil, e
	}
	if after >= latest {
		select {
		case <-w.Changes():
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(18 * time.Second):
		}
		if _, e = s.actor(r); e != nil {
			return nil, e
		}
		if e = read(); e != nil {
			return nil, e
		}
	}
	return object{"cursor": latest, "name": seat}, nil
}
