package deliverystore

import (
	"context"
	"database/sql"
	"time"
)

// StatusRow describes one recipient. Reading it has no acknowledgement side effect.
type StatusRow struct {
	ID        string  `json:"id"`
	Sender    string  `json:"from"`
	Recipient string  `json:"to"`
	Stored    string  `json:"stored_at"`
	Delivered *string `json:"delivered_at"`
	Read      *string `json:"read_at"`
	Answered  *string `json:"answered_at"`
	AnswerID  *string `json:"answer_id"`
	Closed    *string `json:"closed_at"`
}

func (s *Store) MessageStatus(ctx context.Context, seat, id string) ([]StatusRow, error) {
	rows, e := s.readDB().QueryContext(ctx, `SELECT m.id,m.sender,d.seat,m.created_text,d.record_json,coalesce(d.delivered_at,0),coalesce(d.noticed_at,0),coalesce(d.answered_at,0),d.answer_id,coalesce(a.final,0)
 FROM messages m JOIN deliveries d ON d.seq=m.seq LEFT JOIN messages a ON a.id=d.answer_id
 WHERE m.id=? AND (m.sender=? OR d.seat=?) ORDER BY d.seat`, id, seat, seat)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []StatusRow
	for rows.Next() {
		var r StatusRow
		var raw string
		var delivered, read, answered int64
		var final bool
		if e = rows.Scan(&r.ID, &r.Sender, &r.Recipient, &r.Stored, &raw, &delivered, &read, &answered, &r.AnswerID, &final); e != nil {
			return nil, e
		}
		r.Delivered = ExactStamp(raw, "log_delivered_at", delivered)
		r.Read = ExactStamp(raw, "log_acked_at", read)
		r.Answered = ExactStamp(raw, "log_answered_at", answered)
		if final {
			r.Closed = r.Answered
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

type Outstanding struct {
	ID        string `json:"id"`
	Recipient string `json:"to"`
	Created   string `json:"created_at"`
	Priority  int    `json:"priority"`
}

func (s *Store) UnansweredSent(ctx context.Context, seat string, cutoff time.Time) ([]Outstanding, int, error) {
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	const filter = ` FROM messages m JOIN deliveries d ON d.seq=m.seq WHERE m.sender=? AND d.owed=1 AND d.answered_at IS NULL AND m.created_at<=?`
	var total int
	if e = tx.QueryRowContext(ctx, "SELECT count(*)"+filter, seat, cutoff.UnixMilli()).Scan(&total); e != nil {
		return nil, 0, e
	}
	q, e := tx.QueryContext(ctx, "SELECT m.id,d.seat,m.created_text,m.priority"+filter+" ORDER BY m.created_at,m.id,d.seat LIMIT 200", seat, cutoff.UnixMilli())
	if e != nil {
		return nil, 0, e
	}
	defer q.Close()
	out := []Outstanding{}
	for q.Next() {
		var r Outstanding
		if e = q.Scan(&r.ID, &r.Recipient, &r.Created, &r.Priority); e != nil {
			return nil, 0, e
		}
		out = append(out, r)
	}
	return out, total, q.Err()
}

func (s *Store) IsSender(seat, id string) (bool, error) {
	var own bool
	e := s.readDB().QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id=? AND sender=?)", id, seat).Scan(&own)
	return own, e
}

type OverdueRow struct {
	ID, From, To string
	At           int64
}

func (s *Store) OverdueP1(ctx context.Context, cutoff time.Time) ([]OverdueRow, int, error) {
	const filter = ` FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.priority=1 AND d.owed=1 AND d.answered_at IS NULL AND m.created_at<?`
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	var total int
	if e = tx.QueryRowContext(ctx, "SELECT count(*)"+filter, cutoff.UnixMilli()).Scan(&total); e != nil {
		return nil, 0, e
	}
	q, e := tx.QueryContext(ctx, "SELECT m.id,m.sender,d.seat,m.created_at"+filter+" ORDER BY m.created_at,m.id,d.seat LIMIT 200", cutoff.UnixMilli())
	if e != nil {
		return nil, 0, e
	}
	defer q.Close()
	var out []OverdueRow
	for q.Next() {
		var r OverdueRow
		if e = q.Scan(&r.ID, &r.From, &r.To, &r.At); e != nil {
			return nil, 0, e
		}
		out = append(out, r)
	}
	return out, total, q.Err()
}

func (s *Store) HasMailHistory(seat string) (bool, error) {
	var used bool
	e := s.readDB().QueryRow("SELECT EXISTS(SELECT 1 FROM deliveries WHERE seat=?) OR EXISTS(SELECT 1 FROM messages WHERE sender=?)", seat, seat).Scan(&used)
	return used, e
}

type RenameImpact struct {
	Old           string `json:"old"`
	New           string `json:"new"`
	OldExists     bool   `json:"old_exists"`
	NewExists     bool   `json:"new_exists"`
	Deliveries    int    `json:"deliveries"`
	Open          int    `json:"open_obligations"`
	AlsoDelivered int    `json:"also_delivered_to_target"`
	Unreceipted   int    `json:"unreceipted_runtime_deliveries"`
}

func (s *Store) RenameImpact(ctx context.Context, old, new string) (RenameImpact, error) {
	r := RenameImpact{Old: old, New: new}
	e := s.readDB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seats WHERE seat=?),EXISTS(SELECT 1 FROM seats WHERE seat=?),
 count(*),coalesce(sum(d.owed=1 AND d.answered_at IS NULL),0),
 coalesce(sum(EXISTS(SELECT 1 FROM deliveries n WHERE n.seat=? AND n.seq=d.seq)),0),
 coalesce(sum(EXISTS(SELECT 1 FROM delivery_outbox o WHERE o.seat=d.seat AND o.seq=d.seq AND o.receipt IS NULL)),0)
 FROM deliveries d WHERE d.seat=?`, old, new, new, old).Scan(&r.OldExists, &r.NewExists, &r.Deliveries, &r.Open, &r.AlsoDelivered, &r.Unreceipted)
	return r, e
}
