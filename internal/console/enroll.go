package console

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
)

func (s *server) enrollRoutes(add func(string, handler)) {
	add("GET /.well-known/openagentinbox.json", func(http.ResponseWriter, *http.Request) (any, error) {
		return object{"version": 1, "mode": "standalone", "mcp": "/mcp", "enrollment": "/api/agent/enroll", "cli": "/openagentinbox.mjs", "guide": "/agent-guide.md", "inbox": "/api/agent/inbox", "send": "/api/agent/send"}, nil
	})
	add("POST /api/agent/enroll", s.enroll)
	add("POST /api/desk/enrollment", s.approve)
	add("GET /api/desk/enrollment/{code}", func(_ http.ResponseWriter, r *http.Request) (any, error) {

		var name string
		var expiry int64
		c := strings.ToUpper(r.PathValue("code"))
		e := s.db.QueryRow("SELECT name,expires FROM enrollments WHERE code=? AND expires>?", c, now()).Scan(&name, &expiry)
		return object{"name": name, "code": c, "expires": expiry}, e
	})
}
func (s *server) enroll(_ http.ResponseWriter, r *http.Request) (any, error) {

	b := bearer(r)
	if b == "" {
		return nil, failure{401, "Generate a bearer credential"}
	}
	var q struct{ Name string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if !nameRE.MatchString(q.Name) || q.Name == s.operator {
		return nil, failure{400, "Invalid inbox name"}
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT count(*) FROM owner").Scan(&n); e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, failure{409, "Owner setup pending"}
	}
	key := hash(b)
	var name string
	var existing sql.NullString
	e = tx.QueryRow("SELECT name,key FROM agents WHERE name=? OR key=?", q.Name, key).Scan(&name, &existing)
	if e == nil {
		if name == q.Name && existing.String == key {
			return object{"status": "active", "name": name}, nil
		}
		return nil, failure{409, "Inbox already assigned"}
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if _, e = tx.Exec("DELETE FROM enrollments WHERE expires<=?", now()); e != nil {
		return nil, e
	}
	var code string
	var expiry int64
	e = tx.QueryRow("SELECT name,code,expires FROM enrollments WHERE key=?", key).Scan(&name, &code, &expiry)
	if errors.Is(e, sql.ErrNoRows) {
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		var all int
		if e = tx.QueryRow("SELECT count(*),count(CASE WHEN peer=? THEN 1 END) FROM enrollments", peer).Scan(&all, &n); e != nil {
			return nil, e
		}
		if n >= 8 || all >= 128 {
			return nil, failure{429, "Too many pending enrollments"}
		}
		raw := make([]byte, 6)
		rand.Read(raw)
		code = strings.ToUpper(hex.EncodeToString(raw))
		expiry = now() + 600000
		_, e = tx.Exec("INSERT INTO enrollments VALUES(?,?,?,?,?)", code, key, q.Name, expiry, peer)
	} else if e == nil && name != q.Name {
		return nil, failure{409, "Enrollment name mismatch"}
	}
	if e != nil {
		return nil, e
	}
	return object{"status": "pending", "name": q.Name, "code": code, "expires": expiry, "approval_url": s.origin + "/?enroll=" + code}, tx.Commit()
}
func (s *server) approve(_ http.ResponseWriter, r *http.Request) (any, error) {

	var q struct {
		Code, Name string
		Approve    *bool
	}
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if q.Approve == nil {
		return nil, failure{400, "Missing decision"}
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var key string
	if e = tx.QueryRow("DELETE FROM enrollments WHERE code=? AND name=? AND expires>? RETURNING key", strings.ToUpper(q.Code), q.Name, now()).Scan(&key); e != nil {
		return nil, e
	}
	if *q.Approve {
		res, e := tx.Exec("INSERT OR IGNORE INTO agents VALUES(?,?)", q.Name, key)
		if e != nil {
			return nil, e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		if n == 0 {
			return nil, failure{409, "Inbox already assigned"}
		}
	}
	return object{"approved": *q.Approve}, tx.Commit()
}
