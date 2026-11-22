package deliverystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *Store) SetState(ctx context.Context, seat, key, value string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = checkFenceTx(ctx, tx); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO seat_state VALUES(?,?,?) ON CONFLICT(seat,key) DO UPDATE SET value=excluded.value`, seat, key, value)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) GetState(ctx context.Context, seat string) (map[string]string, error) {
	q, e := s.readDB().QueryContext(ctx, `SELECT key,value FROM seat_state WHERE seat=?`, seat)
	if e != nil {
		return nil, e
	}
	defer q.Close()
	out := map[string]string{}
	for q.Next() {
		var k, v string
		if e = q.Scan(&k, &v); e != nil {
			return nil, e
		}
		out[k] = v
	}
	return out, q.Err()
}

// Notifications writes only told-state, never read/answered state. A lost response can be re-announced after 60s.
func (s *Store) Notifications(ctx context.Context, seat string, operator bool, now time.Time) ([]Row, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = checkFenceTx(ctx, tx); e != nil {
		return nil, e
	}
	focused := false
	q, e := tx.QueryContext(ctx, `SELECT value FROM seat_state WHERE seat=? AND key IN ('focus','busy')`, seat)
	if e != nil {
		return nil, e
	}
	for q.Next() {
		var raw string
		var v struct {
			Until time.Time `json:"until"`
		}
		if e = q.Scan(&raw); e != nil {
			q.Close()
			return nil, e
		}
		if json.Unmarshal([]byte(raw), &v) == nil && v.Until.After(now) {
			focused = true
		}
	}
	e = q.Err()
	q.Close()
	if e != nil {
		return nil, e
	}
	cond := ""
	if operator {
		cond = " AND m.sender='operator'"
	}
	q, e = tx.QueryContext(ctx, `SELECT `+rowCols+` FROM deliveries d JOIN messages m ON m.seq=d.seq LEFT JOIN told t ON t.seat=d.seat AND t.seq=d.seq WHERE d.seat=? AND d.noticed_at IS NULL AND (d.priority=0 OR (d.priority=1 AND m.final=0)) AND (t.at IS NULL OR t.at<?) AND (?=0 OR d.priority=0)`+cond+` ORDER BY d.priority,d.created_at,d.seq LIMIT 5`, seat, now.Add(-time.Minute).UnixMilli(), b2i(focused))
	if e != nil {
		return nil, e
	}
	out := []Row{}
	for q.Next() {
		var r Row
		var f int
		if e = q.Scan(&r.Seq, &r.ID, &r.Sender, &r.ConversationID, &r.ParentID, &r.Subject, &r.Priority, &f, &r.CreatedText, &r.createdMilli, &r.Body, &r.Noticed, &r.Answered); e != nil {
			q.Close()
			return nil, e
		}
		r.Final = f == 1
		out = append(out, r)
	}
	e = q.Err()
	q.Close()
	if e != nil {
		return nil, e
	}
	for _, r := range out {
		if _, e = tx.ExecContext(ctx, `INSERT INTO told VALUES(?,?,?) ON CONFLICT(seat,seq) DO UPDATE SET at=excluded.at`, seat, r.Seq, now.UnixMilli()); e != nil {
			return nil, e
		}
	}
	return out, tx.Commit()
}
func (s *Store) ExportSeat(ctx context.Context, seat, mode, since string, limit, maxBytes int) ([]Delivered, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("invalid limit")
	}
	var after int64
	if since != "" {
		t, e := time.Parse(time.RFC3339Nano, since)
		if e != nil {
			return nil, e
		}
		after = t.UnixMilli()
	}
	cond := ""
	if mode == "outstanding" {
		cond = " AND d.owed=1 AND d.answered_at IS NULL"
	}
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var n, bytes int
	if e = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(m.bytes),0) FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND m.created_at>?`+cond, seat, after).Scan(&n, &bytes); e != nil {
		return nil, e
	}
	if n > limit || bytes > maxBytes/6 {
		return nil, ErrPageRequired
	}
	q, e := tx.QueryContext(ctx, `SELECT `+deliveredCols+` FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND m.created_at>?`+cond+` ORDER BY m.created_at,m.id LIMIT ?`, seat, after, limit)
	if e != nil {
		return nil, e
	}
	defer q.Close()
	out := []Delivered{}
	for q.Next() {
		r, e := scanDelivered(q)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, q.Err()
}
