package deliverystore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type DigestEntry struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	Subject  string `json:"subject"`
	Priority int    `json:"priority"`
	TaskRef  string `json:"task_ref,omitempty"`
}
type Digest struct {
	Schema  string        `json:"schema"`
	Unread  int           `json:"unread"`
	Pending int           `json:"pending"`
	Entries []DigestEntry `json:"entries"`
	More    bool          `json:"has_more"`
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}

// Digest selects at most five outstanding requests using the priority index.
// It never reads bodies. The serialized result is bounded to 8 KiB, including
// JSON escaping. Full pending retrieval is independent and never throttled.
func (s *Store) Digest(ctx context.Context, seat string) (Digest, error) {
	d := Digest{Schema: "openagentinbox.digest/v1", Entries: []DigestEntry{}}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return d, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT unread FROM mailbox_counts WHERE seat=?),0),COALESCE((SELECT pending FROM mailbox_counts WHERE seat=?),0)`, seat, seat).Scan(&d.Unread, &d.Pending)
	if e != nil {
		return d, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT m.id,m.sender,substr(m.subject,1,160),d.priority,COALESCE(l.task_ref,'') FROM deliveries d INDEXED BY deliveries_pending_priority JOIN messages m ON m.seq=d.seq LEFT JOIN message_links l ON l.seat=d.seat AND l.seq=d.seq WHERE d.seat=? AND d.owed=1 AND d.answered_at IS NULL ORDER BY d.priority,d.seq LIMIT 5`, seat)
	if e != nil {
		return d, e
	}
	defer rows.Close()
	for rows.Next() {
		var r DigestEntry
		if e = rows.Scan(&r.ID, &r.From, &r.Subject, &r.Priority, &r.TaskRef); e != nil {
			return d, e
		}
		r.Subject = clip(r.Subject, 160)
		d.Entries = append(d.Entries, r)
		raw, _ := json.Marshal(d)
		if len(raw) > 8000 {
			d.Entries = d.Entries[:len(d.Entries)-1]
			break
		}
	}
	d.More = d.Pending > len(d.Entries)
	return d, rows.Err()
}

// LinkTask records only a reference. It never claims authoritative task status.
func (s *Store) LinkTask(ctx context.Context, seat, id, ref string) error {
	if len(ref) > 512 || strings.ContainsAny(ref, "\x00\r\n") || len(id) > 256 {
		return errors.New("invalid task reference")
	}
	result, e := s.db.ExecContext(ctx, `INSERT INTO message_links(seat,seq,task_ref) SELECT d.seat,d.seq,? FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND m.id=? ON CONFLICT(seat,seq) DO UPDATE SET task_ref=excluded.task_ref`, ref, seat, id)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNoSuch
	}
	return nil
}

type SnapshotEntry struct {
	Seq      int64  `json:"seq"`
	ID       string `json:"id"`
	Read     bool   `json:"read"`
	Answered bool   `json:"answered"`
}
type Snapshot struct {
	Schema       string          `json:"schema"`
	Entries      []SnapshotEntry `json:"entries"`
	After        int64           `json:"after"`
	ChangesAfter int64           `json:"changes_after"`
	More         bool            `json:"has_more"`
}

// A resync starts after=0 and retains the FIRST page's changes_after. After all
// pages, replay changes from that watermark (including deletes/late imports).
// If retention overtook the watermark, restart; never silently skip a gap.
func (s *Store) Snapshot(ctx context.Context, seat string, after int64, limit int) (Snapshot, error) {
	p := Snapshot{Schema: "openagentinbox.snapshot/v1", Entries: []SnapshotEntry{}, After: after}
	if after < 0 || limit < 1 || limit > 50 {
		return p, errors.New("invalid page")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT MAX(COALESCE((SELECT MAX(event) FROM mailbox_events WHERE seat=?),0),COALESCE((SELECT floor FROM sync_floor WHERE seat=?),0))`, seat, seat).Scan(&p.ChangesAfter)
	if e != nil {
		return p, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT d.seq,m.id,d.noticed_at IS NOT NULL,d.answered_at IS NOT NULL FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND d.seq>? ORDER BY d.seq LIMIT ?`, seat, after, limit+1)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var r SnapshotEntry
		if e = rows.Scan(&r.Seq, &r.ID, &r.Read, &r.Answered); e != nil {
			return p, e
		}
		if len(p.Entries) == limit {
			p.More = true
			break
		}
		p.Entries = append(p.Entries, r)
		p.After = r.Seq
	}
	return p, rows.Err()
}
