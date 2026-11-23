package console

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// Fixed UTC intervals make shared time links independent of the viewer's zone.
const hourMillis int64 = 3600000
const dayMillis = 24 * hourMillis
const timeFilter = " WHERE t.root=? AND m.created_at>=? AND m.created_at<? AND (m.sender=? OR EXISTS(SELECT 1 FROM deliveries v WHERE v.seq=m.seq AND v.seat=?))"

const timeMembers = " FROM messages m INDEXED BY message_sent_time CROSS JOIN thread_members t ON t.seq=m.seq" + timeFilter

func (s *server) threadTime(r *http.Request, box, root string) (any, error) {
	q := r.URL.Query()
	var span, width int64
	switch q.Get("zoom") {
	case "hour":
		span, width = hourMillis, hourMillis/12
	case "day":
		span, width = dayMillis, hourMillis
	case "month":
		span, width = 30*dayMillis, dayMillis
	default:
		return nil, failure{400, "Unknown time scale"}
	}
	start, err := strconv.ParseInt(q.Get("start"), 10, 64)
	if err != nil || start < 0 || start > 253402300799999-span || root == "" || q.Get("q") != "" || q.Get("before") != "" && q.Get("after") != "" {
		return nil, failure{400, "Invalid time window"}
	}
	end := start + span
	base := []any{root, start, end, box, box}
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	buckets := make([]int, span/width)
	rows, err := tx.QueryContext(ctx, "SELECT (m.created_at-?)/?,count(*)"+timeMembers+" GROUP BY 1", append([]any{start, width}, base...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var i, n int
		if err = rows.Scan(&i, &n); err != nil {
			rows.Close()
			return nil, err
		}
		buckets[i] = n
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	cursor := q.Get("before")
	asc := q.Get("after") != ""
	cmp, order := "<", " DESC"
	if asc {
		cursor = q.Get("after")
		cmp, order = ">", " ASC"
	}
	args := append([]any{box, box, box, box}, base...)
	// Resolve receipts for the mailbox or the sole recipient of outgoing mail.
	query := `SELECT m.seq,m.created_at,m.id,m.sender,CASE WHEN m.sender=? THEN (SELECT group_concat(seat,', ') FROM deliveries WHERE seq=m.seq) ELSE ? END,
 m.subject,m.created_text,substr(m.display_body,1,768),coalesce(r.opened,0),coalesce(r.replied,0),coalesce(r.due,0)
 FROM messages m INDEXED BY message_sent_time CROSS JOIN thread_members t ON t.seq=m.seq
 LEFT JOIN mail_receipts r ON r.id=m.id AND r.seat=CASE WHEN m.sender=? THEN (SELECT min(seat) FROM deliveries WHERE seq=m.seq HAVING count(*)=1) ELSE ? END` + timeFilter
	if cursor != "" {
		a, b, ok := strings.Cut(cursor, ".")
		ms, e1 := strconv.ParseInt(a, 10, 64)
		seq, e2 := strconv.ParseInt(b, 10, 64)
		if !ok || e1 != nil || e2 != nil || ms < start || ms >= end || seq <= 0 {
			return nil, failure{400, "Invalid time cursor"}
		}
		query += " AND (m.created_at,m.seq)" + cmp + "(?,?)"
		args = append(args, ms, seq)
	}
	rows, err = tx.QueryContext(ctx, query+" ORDER BY m.created_at"+order+",m.seq"+order+" LIMIT 41", args...)
	if err != nil {
		return nil, err
	}
	result := []object{}
	positions := []string{}
	more := false
	for rows.Next() {
		if len(result) == 40 {
			more = true
			break
		}
		var seq, ms, opened, replied, due int64
		var id, src, sub, at, preview string
		var dst sql.NullString
		if err = rows.Scan(&seq, &ms, &id, &src, &dst, &sub, &at, &preview, &opened, &replied, &due); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, object{"id": id, "thread": root, "position": strconv.FormatInt(seq, 10), "src": src, "dst": dst.String, "at": at, "sent_ms": ms, "subject": sub, "display": preview, "opened": opened, "replied": replied, "due": due})
		positions = append(positions, strconv.FormatInt(ms, 10)+"."+strconv.FormatInt(seq, 10))
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var older, newer any
	if len(result) > 0 {
		first, last := positions[0], positions[len(positions)-1]
		if asc {
			for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
				result[i], result[j] = result[j], result[i]
			}
			older = first
			if more {
				newer = last
			}
		} else {
			if more {
				older = last
			}
			if cursor != "" {
				newer = first
			}
		}
	}
	var title string
	err = tx.QueryRowContext(ctx, "SELECT coalesce(nullif(m.subject,''),substr(m.display_body,1,512)) FROM thread_members t CROSS JOIN messages m USING(seq) WHERE t.root=? AND (m.sender=? OR EXISTS(SELECT 1 FROM deliveries d WHERE d.seq=t.seq AND d.seat=?)) ORDER BY t.seq LIMIT 1", root, box, box).Scan(&title)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var in, out int
	if err = tx.QueryRowContext(ctx, "SELECT coalesce(sum(CASE folder WHEN 'inbox' THEN n END),0),coalesce(sum(CASE folder WHEN 'sent' THEN n END),0) FROM thread_heads WHERE box=? AND root=?", box, root).Scan(&in, &out); err != nil {
		return nil, err
	}
	return object{"rows": result, "next": older, "newer": newer, "buckets": buckets, "title": title, "inbox_count": in, "sent_count": out}, nil
}
