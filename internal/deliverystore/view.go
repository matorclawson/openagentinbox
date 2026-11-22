package deliverystore

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Row is one listed message with everything the CLI's JSON view names (id, from, created_at, log_state, priority, final, subject,
// conversation_id, in_reply_to). Body is carried only so a renderer can build the one-line preview the CLI shows.
type Row struct {
	Seq                                           int64
	ID, Sender, ConversationID, ParentID, Subject string
	CreatedText                                   string
	Priority                                      int
	Final, Noticed, Answered                      bool
	createdMilli                                  int64
	Body                                          []byte // the stored body, for the renderer's preview and header reads; never sent to a caller
}

// LogState is the file store's vocabulary for the row: delivered (unread), acknowledged (read), answered.
func (r Row) LogState() string {
	switch {
	case r.Answered:
		return "answered"
	case r.Noticed:
		return "acknowledged"
	}
	return "delivered"
}

// View is one page the way bin/openagentinbox inbox and pending build it: Rows are bounded, Total is the true count behind them.
type View struct {
	Rows  []Row
	Total int
}

const (
	unreadCond  = `INDEXED BY deliveries_unread JOIN messages m ON m.seq = d.seq WHERE d.seat = ? AND d.noticed_at IS NULL`
	pendingCond = `INDEXED BY deliveries_pending JOIN messages m ON m.seq = d.seq WHERE d.seat = ? AND d.owed = 1 AND d.answered_at IS NULL`
	rowCols     = `m.seq, m.id, m.sender, COALESCE(m.conversation_id,''), COALESCE(m.parent_id,''), m.subject, m.priority, m.final, coalesce(nullif(d.created_text,''),m.created_text), coalesce(d.created_milli,m.created_at), m.display_body, d.noticed_at IS NOT NULL, d.answered_at IS NOT NULL`
)

func viewRows(db *sql.Tx, cond string, seat, prio string, order string, limit int) ([]Row, error) {
	// Filter priority in the delivery index BEFORE looking up message payloads.
	if prio != "" {
		name := "deliveries_unread"
		if strings.Contains(cond, "deliveries_pending") {
			name = "deliveries_pending"
		}
		index := name + "_priority_created"
		if prio == ` AND m.priority > 0` {
			index = "deliveries_unread_other_created"
		}
		cond = strings.Replace(cond, "INDEXED BY "+name, "INDEXED BY "+index, 1)
		prio = strings.ReplaceAll(prio, "m.priority", "d.priority")
	}
	if prio != "" {
		order = strings.ReplaceAll(order, "d.seq DESC", "d.created_at DESC, d.seq DESC")
		order = strings.ReplaceAll(order, "d.seq ASC", "d.created_at ASC, d.seq ASC")
	}
	q := `SELECT ` + rowCols + ` FROM deliveries d ` + cond + prio + ` ORDER BY ` + order
	args := []any{seat}
	if limit >= 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rs, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var r Row
		var fin int
		if err := rs.Scan(&r.Seq, &r.ID, &r.Sender, &r.ConversationID, &r.ParentID, &r.Subject, &r.Priority, &fin, &r.CreatedText, &r.createdMilli, &r.Body, &r.Noticed, &r.Answered); err != nil {
			return nil, err
		}
		r.Final = fin == 1
		out = append(out, r)
	}
	if len(out) > 200 {
		return nil, ErrPageRequired
	}
	return out, rs.Err()
}

// byRankCreated is unread_json's order, which the CLI keeps when the list is within the row cap: p0, then p1, then p2, each oldest first.
func byRankCreated(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Priority != rows[j].Priority {
			return rows[i].Priority < rows[j].Priority
		}
		if rows[i].createdMilli != rows[j].createdMilli {
			return rows[i].createdMilli < rows[j].createdMilli
		}
		return rows[i].Seq < rows[j].Seq
	})
}

func byCreated(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].createdMilli != rows[j].createdMilli {
			return rows[i].createdMilli < rows[j].createdMilli
		}
		return rows[i].Seq < rows[j].Seq
	})
}

// capped is cap_unread: every p0 and the newest n p1, oldest first. cond is the list's condition (unread or pending).
func capped(db *sql.Tx, cond, seat string, n int) ([]Row, error) {
	p0, err := viewRows(db, cond, seat, ` AND m.priority = 0`, `d.seq ASC`, 201)
	if err != nil {
		return nil, err
	}
	p1, err := viewRows(db, cond, seat, ` AND m.priority = 1`, `d.seq DESC`, n)
	if err != nil {
		return nil, err
	}
	out := append(p0, p1...)
	if len(out) > 200 {
		return nil, ErrPageRequired
	}
	byCreated(out)
	return out, nil
}

// InboxView is bin/openagentinbox inbox --json: unread mail. limit >= 0 is --limit N: every p0 and the newest N p1, p2 counted and never shown. limit < 0 is
// no --limit: when the unread total is within rowcap every row (p2 too) is shown, otherwise every p0 and the newest others up to rowcap in all.
func (s *Store) InboxView(seat string, limit, rowcap int) (View, error) {
	var v View
	db, err := s.readDB().Begin()
	if err != nil {
		return v, err
	}
	defer db.Rollback() // a read snapshot: total and rows describe the same committed state
	if err := db.QueryRow(`SELECT COALESCE((SELECT unread FROM mailbox_counts WHERE seat = ?), 0)`, seat).Scan(&v.Total); err != nil {
		return v, err
	}
	if limit >= 0 {
		rows, err := capped(db, unreadCond, seat, limit)
		v.Rows = rows
		return v, err
	}
	if v.Total <= rowcap {
		rows, err := viewRows(db, unreadCond, seat, ``, `d.seq ASC`, 201)
		byRankCreated(rows)
		v.Rows = rows
		return v, err
	}
	p0, err := viewRows(db, unreadCond, seat, ` AND m.priority = 0`, `d.seq ASC`, 201)
	if err != nil {
		return v, err
	}
	room := rowcap - len(p0)
	if room < 0 {
		room = 0
	}
	others, err := viewRows(db, unreadCond, seat, ` AND m.priority > 0`, `d.seq DESC`, room)
	if err != nil {
		return v, err
	}
	v.Rows = append(p0, others...)
	byCreated(v.Rows)
	return v, nil
}

// PendingView is bin/openagentinbox pending --json: p0/p1 requests not answered, read or not. limit is --limit N (1 to 200, default 50 at the caller).
func (s *Store) PendingView(seat string, limit int) (View, error) {
	var v View
	db, err := s.readDB().Begin()
	if err != nil {
		return v, err
	}
	defer db.Rollback() // a read snapshot: total and rows describe the same committed state
	if limit < 1 || limit > 200 {
		return v, fmt.Errorf("pending limit %d: must be at least 1", limit)
	}
	if err := db.QueryRow(`SELECT COALESCE((SELECT pending FROM mailbox_counts WHERE seat = ?), 0)`, seat).Scan(&v.Total); err != nil {
		return v, err
	}
	rows, err := capped(db, pendingCond, seat, limit)
	v.Rows = rows
	return v, err
}
