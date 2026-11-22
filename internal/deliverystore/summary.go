package deliverystore

import (
	"context"
	"database/sql"
)

type MailboxStat struct {
	Seat                                                                               string
	UnreadP0, UnreadP1, PendingP0, PendingP1, OldestUnread, OldestPending, LastRequest int64
}

// MailboxStats uses a single read snapshot and never loads bodies.
func (s *Store) MailboxStats(ctx context.Context) ([]MailboxStat, int64, error) {
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	var gen int64
	if e = tx.QueryRowContext(ctx, `SELECT coalesce(max(seq),0) FROM messages`).Scan(&gen); e != nil {
		return nil, 0, e
	}
	q, e := tx.QueryContext(ctx, `SELECT s.seat,
 coalesce((SELECT n FROM priority_counts WHERE seat=s.seat AND priority=0),0),
 coalesce((SELECT n FROM priority_counts WHERE seat=s.seat AND priority=1),0),
 (SELECT count(*) FROM deliveries WHERE seat=s.seat AND owed=1 AND answered_at IS NULL AND priority=0),
 (SELECT count(*) FROM deliveries WHERE seat=s.seat AND owed=1 AND answered_at IS NULL AND priority=1),
 coalesce((SELECT min(created_at) FROM deliveries WHERE seat=s.seat AND noticed_at IS NULL AND priority<=1),0),
 coalesce((SELECT min(created_at) FROM deliveries WHERE seat=s.seat AND owed=1 AND answered_at IS NULL),0)
  ,coalesce((SELECT at FROM seat_presence WHERE seat=s.seat),0) FROM seats s ORDER BY s.seat`)
	if e != nil {
		return nil, 0, e
	}
	defer q.Close()
	out := []MailboxStat{}
	for q.Next() {
		var r MailboxStat
		if e = q.Scan(&r.Seat, &r.UnreadP0, &r.UnreadP1, &r.PendingP0, &r.PendingP1, &r.OldestUnread, &r.OldestPending, &r.LastRequest); e != nil {
			return nil, 0, e
		}
		out = append(out, r)
	}
	return out, gen, q.Err()
}

// PendingFrom filters inside SQLite before pagination. Other senders do not affect counts or page selection.
func (s *Store) PendingFrom(ctx context.Context, seat, from string, limit int) (View, error) {
	var v View
	if limit < 0 || limit > 200 {
		return v, ErrPageRequired
	}
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	cond := ` FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND m.sender=? AND d.owed=1 AND d.answered_at IS NULL`
	if e = tx.QueryRowContext(ctx, `SELECT count(*)`+cond, seat, from).Scan(&v.Total); e != nil {
		return v, e
	}
	q, e := tx.QueryContext(ctx, `SELECT `+rowCols+cond+` ORDER BY d.priority,m.created_at DESC,d.seq DESC LIMIT ?`, seat, from, limit)
	if e != nil {
		return v, e
	}
	defer q.Close()
	for q.Next() {
		var r Row
		var fin int
		if e = q.Scan(&r.Seq, &r.ID, &r.Sender, &r.ConversationID, &r.ParentID, &r.Subject, &r.Priority, &fin, &r.CreatedText, &r.createdMilli, &r.Body, &r.Noticed, &r.Answered); e != nil {
			return v, e
		}
		r.Final = fin == 1
		v.Rows = append(v.Rows, r)
	}
	return v, q.Err()
}

// SavePresence flushes authenticated observations together. A revoked or
// unregistered seat cannot create a presence row just by polling.
func (s *Store) SavePresence(ctx context.Context, batch map[string]int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for seat, at := range batch {
		if _, e = tx.ExecContext(ctx, `INSERT INTO seat_presence(seat,at) SELECT seat,? FROM seats WHERE seat=? ON CONFLICT(seat) DO UPDATE SET at=max(at,excluded.at)`, at, seat); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) LastRequest(ctx context.Context, seat string) (int64, error) {
	var ms int64
	e := s.readDB().QueryRowContext(ctx, "SELECT coalesce((SELECT at FROM seat_presence WHERE seat=?),0)", seat).Scan(&ms)
	return ms, e
}
