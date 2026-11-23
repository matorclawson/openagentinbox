package ephemeral

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
)

var ErrStartConflict = errors.New("run ID already bound to another or unrecorded start request")

// StartOnce atomically starts work or recognizes a repeated immutable request.
// Initial attributes are included in the fingerprint even if later edited.
// IDs created through Start or earlier releases have no verifiable fingerprint.
func (s *Store) StartOnce(id, name string, version int, queue string, input json.RawMessage, attrs map[string]string) (bool, error) {
	if id == "" || queue == "" || !json.Valid(input) {
		return false, errors.New("id, queue and JSON input required")
	}
	if attrs == nil {
		attrs = map[string]string{}
	}
	payload, err := json.Marshal([]any{name, version, queue, string(input), attrs})
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	created := false
	err = s.tx(func() error {
		r, e := s.query("SELECT coalesce(digest,'') FROM runs LEFT JOIN start_requests ON run=id WHERE id=?", id)
		if e != nil {
			return e
		}
		if len(r) > 0 {
			if r[0][0] != digest {
				return ErrStartConflict
			}
			return nil
		}
		if e = s.start(id, name, version, queue, input, attrs); e != nil {
			return e
		}
		if e = s.exec("INSERT INTO start_requests VALUES(?,?)", id, digest); e != nil {
			return e
		}
		created = true
		return nil
	})
	return created && err == nil, err
}

// NextDue returns the earliest runnable transition in this queue in Unix milliseconds.
// Newly committed work can advance this hint. Recheck after a wake or bounded timeout.
func (s *Store) NextDue(queue string) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.query("SELECT due FROM runs WHERE queue=? AND "+runnableStates+" ORDER BY due,id LIMIT 1", queue)
	if e != nil || len(r) == 0 {
		return 0, false, e
	}
	due, e := strconv.ParseInt(r[0][0], 10, 64)
	return due, e == nil, e
}
