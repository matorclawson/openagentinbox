package ephemeral

import (
	"encoding/json"
	"errors"
	"strconv"
)

type Event struct {
	Sequence, TimeMillis int64
	Kind                 string
	Step                 int
	Data                 string
}

func (s *Store) History(id string, after int64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be 1..1000")
	}
	r, e := s.query("SELECT seq,at,kind,step,data FROM events WHERE run=? AND seq>? ORDER BY seq LIMIT ?", id, after, limit)
	if e != nil {
		return nil, e
	}
	out := []Event{}
	for _, v := range r {
		seq, _ := strconv.ParseInt(v[0], 10, 64)
		at, _ := strconv.ParseInt(v[1], 10, 64)
		out = append(out, Event{seq, at, v[2], num(v[3]), v[4]})
	}
	return out, nil
}

type Description struct {
	ID, Name, Queue, State string
	Version, Step, Attempt int
	DueMillis              int64
	Value                  json.RawMessage
	Attributes             map[string]string
}

func (s *Store) Describe(id string) (Description, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var d Description
	r, e := s.query("SELECT id,name,queue,state,version,step,attempt,due,value,k,v FROM runs LEFT JOIN attrs ON run=id WHERE id=? ORDER BY k", id)
	if e != nil {
		return d, e
	}
	if len(r) == 0 {
		return d, ErrMissing
	}
	v := r[0]
	d.ID, d.Name, d.Queue, d.State = v[0], v[1], v[2], v[3]
	d.Version, d.Step, d.Attempt = num(v[4]), num(v[5]), num(v[6])
	d.DueMillis, _ = strconv.ParseInt(v[7], 10, 64)
	d.Value = json.RawMessage(v[8])
	d.Attributes = map[string]string{}
	for _, v := range r {
		if v[9] != "" {
			d.Attributes[v[9]] = v[10]
		}
	}
	return d, nil
}
func (s *Store) Find(key, value, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be 1..1000")
	}
	r, e := s.query("SELECT run FROM attrs WHERE k=? AND v=? AND run>? ORDER BY run LIMIT ?", key, value, after, limit)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, v := range r {
		out = append(out, v[0])
	}
	return out, nil
}

// List returns run IDs in stable keyset order. An empty state includes all runs.
func (s *Store) List(state, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be 1..1000")
	}
	q := "SELECT id FROM runs WHERE id>? ORDER BY id LIMIT ?"
	args := []any{after, limit}
	if state != "" {
		q = "SELECT id FROM runs WHERE state=? AND id>? ORDER BY id LIMIT ?"
		args = []any{state, after, limit}
	}
	r, e := s.query(q, args...)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, row := range r {
		ids = append(ids, row[0])
	}
	return ids, nil
}
