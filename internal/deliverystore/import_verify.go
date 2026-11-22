package deliverystore

import (
	"context"
	"fmt"
)

type SeatCounts struct {
	Messages int `json:"messages"`
	Unread   int `json:"unread"`
	Pending  int `json:"pending"`
	Answered int `json:"answered"`
}

// VerifyImport independently rereads every stored body, one at a time, and compares its persisted hash.
func (s *Store) VerifyImport(ctx context.Context) (map[string]SeatCounts, error) {
	q, e := s.readDB().QueryContext(ctx, `SELECT id,body,coalesce(payload_sha256,'') FROM messages ORDER BY seq`)
	if e != nil {
		return nil, e
	}
	for q.Next() {
		var id, hash string
		var b []byte
		if e = q.Scan(&id, &b, &hash); e != nil {
			q.Close()
			return nil, e
		}
		if hash == "" || hash != PayloadSHA256(b) {
			q.Close()
			return nil, fmt.Errorf("stored body hash mismatch: %s", id)
		}
	}
	e = q.Err()
	q.Close()
	if e != nil {
		return nil, e
	}
	q, e = s.readDB().QueryContext(ctx, `SELECT seat,count(*),sum(noticed_at IS NULL),sum(owed=1 AND answered_at IS NULL),sum(answered_at IS NOT NULL) FROM deliveries GROUP BY seat`)
	if e != nil {
		return nil, e
	}
	defer q.Close()
	out := map[string]SeatCounts{}
	for q.Next() {
		var seat string
		var c SeatCounts
		if e = q.Scan(&seat, &c.Messages, &c.Unread, &c.Pending, &c.Answered); e != nil {
			return nil, e
		}
		out[seat] = c
	}
	return out, q.Err()
}
