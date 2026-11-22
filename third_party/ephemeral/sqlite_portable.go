package ephemeral

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	_ "modernc.org/sqlite"
)

// A dedicated connection keeps BEGIN and COMMIT on the same database session.
type Store struct {
	mu   sync.Mutex
	conn *sql.Conn
	db   *sql.DB
}

func Open(path string) (*Store, error) {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	conn, e := db.Conn(context.Background())
	if e != nil {
		db.Close()
		return nil, e
	}
	s := &Store{db: db, conn: conn}
	if _, e = s.query("PRAGMA busy_timeout=5000"); e == nil {
		for _, q := range schema {
			if _, e = s.query(q); e != nil {
				break
			}
		}
	}
	if e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.db.Close()
		s.conn = nil
	}
}
func (s *Store) query(q string, args ...any) (rows, error) {
	if s.conn == nil {
		return nil, errors.New("closed store")
	}
	r, e := s.conn.QueryContext(context.Background(), q, args...)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	cols, e := r.Columns()
	if e != nil {
		return nil, e
	}
	out := rows{}
	for r.Next() {
		v := make([]sql.NullString, len(cols))
		dst := make([]any, len(cols))
		for i := range v {
			dst[i] = &v[i]
		}
		if e = r.Scan(dst...); e != nil {
			return nil, e
		}
		line := make([]string, len(cols))
		for i := range v {
			line[i] = v[i].String
		}
		out = append(out, line)
	}
	return out, r.Err()
}
