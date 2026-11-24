package console

import (
	"net/http"
	"strconv"
	"time"
)

// Only envelopes visible to this mailbox contribute to the outline.
func (s *server) threadOutline(r *http.Request, box, root string) (any, error) {
	if root == "" {
		return nil, failure{400, "Conversation required"}
	}
	q := r.URL.Query()
	level := q.Get("outline")
	if level == "summary" {
		return threadMetadata(r.Context(), s.store.Reader(), root, box)
	}
	at, err := strconv.ParseInt(q.Get("at"), 10, 64)
	if err != nil && q.Get("at") != "" || at < 0 || at > 253402300799999 {
		return nil, failure{400, "Invalid period"}
	}
	const members = " FROM thread_members t CROSS JOIN messages m USING(seq) WHERE t.root=? AND (m.sender=? OR EXISTS(SELECT 1 FROM deliveries d WHERE d.seq=m.seq AND d.seat=?))"
	args := []any{root, box, box}
	where := members
	var bucket string
	switch level {
	case "months":
		bucket = "CAST(strftime('%s',m.created_at/1000,'unixepoch','start of month') AS INTEGER)*1000"
		if at > 0 {
			where += " AND m.created_at<?"
			args = append(args, at)
		}
	case "days", "hours":
		day := time.UnixMilli(at).UTC()
		begin := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := begin.AddDate(0, 1, 0)
		bucket = "(m.created_at/86400000)*86400000"
		if level == "hours" {
			begin = day.Truncate(24 * time.Hour)
			end = begin.AddDate(0, 0, 1)
			bucket = "(m.created_at/3600000)*3600000"
		}
		where += " AND m.created_at>=? AND m.created_at<?"
		args = append(args, begin.UnixMilli(), end.UnixMilli())
	default:
		return nil, failure{400, "Unknown outline level"}
	}
	rows, err := s.store.Reader().QueryContext(r.Context(), "SELECT "+bucket+",count(*),max(m.created_at)"+where+" GROUP BY 1 ORDER BY 1 DESC LIMIT 33", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []object{}
	var next int64
	for rows.Next() {
		if len(items) == 32 {
			next = items[31]["start"].(int64)
			break
		}
		var start, n, latest int64
		if err = rows.Scan(&start, &n, &latest); err != nil {
			return nil, err
		}
		items = append(items, object{"start": start, "count": n, "latest": latest})
	}
	return object{"items": items, "next": next}, rows.Err()
}
