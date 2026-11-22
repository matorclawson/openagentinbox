package deliverystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrPageRequired = errors.New("mailbox exceeds the legacy response bound; use cursor-paginated /v1/page (no mail was acknowledged)")

const MaxMessageBytes = 1 << 20
const MaxRecipients = 400
const MaxSubjectBytes = 4096

func previewPrefix(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

// DisplayBody preserves the first header values and first visible line only.
// It is computed once at ingestion, never by loading bodies during list queries.
func DisplayBody(body []byte) string {
	rest := string(body)
	var out strings.Builder
	seen := map[string]bool{}
	for rest != "" {
		line, tail, _ := strings.Cut(rest, "\n")
		key := ""
		for _, k := range []string{"Subject: ", "Priority: ", "Final: ", "Kind: ", "Record-Ref: ", "Record-Branch: ", "Probe: ", "Auto: "} {
			if strings.HasPrefix(line, k) {
				key = k
				break
			}
		}
		if key == "" {
			break
		}
		rest = tail
		if !seen[key] {
			if len(line) > MaxSubjectBytes+16 {
				line = line[:MaxSubjectBytes+16]
			}
			out.WriteString(line)
			out.WriteByte('\n')
			seen[key] = true
		}
	}
	out.WriteByte('\n')
	for rest != "" {
		line, tail, _ := strings.Cut(rest, "\n")
		rest = tail
		if line == "" {
			continue
		}
		n := 0
		for pos := range line {
			if n == 100 {
				line = line[:pos]
				break
			}
			n++
		}
		out.WriteString(line)
		break
	}
	return out.String()
}

// Backfill one body at a time. Opening an existing DB does not materialize history.
func (s *Store) migrateDisplay() error {
	return s.nativeMigration(func(c *sql.Conn) error {
		ctx := context.Background()
		yes, e := connHasColumn(c, "messages", "display_body")
		if e != nil {
			return e
		}
		if yes {
			return nil
		}
		if _, e = c.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN display_body TEXT NOT NULL DEFAULT ''`); e != nil {
			return e
		}
		var after int64
		for {
			var seq int64
			var b []byte
			e = c.QueryRowContext(ctx, `SELECT seq,body FROM messages WHERE seq>? ORDER BY seq LIMIT 1`, after).Scan(&seq, &b)
			if e == sql.ErrNoRows {
				break
			}
			if e != nil {
				return e
			}
			if len(b) > MaxMessageBytes {
				return fmt.Errorf("message seq %d exceeds migration body bound", seq)
			}
			if _, e = c.ExecContext(ctx, `UPDATE messages SET display_body=? WHERE seq=?`, DisplayBody(b), seq); e != nil {
				return e
			}
			after = seq
		}
		return nil
	})
}

func (s *Store) UnreadCounts(seat string) (c [3]int, err error) {
	rows, err := s.readDB().Query(`SELECT priority,n FROM priority_counts WHERE seat=?`, seat)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var p, n int
		if err = rows.Scan(&p, &n); err != nil {
			return c, err
		}
		if p >= 0 && p < 3 {
			c[p] = n
		}
	}
	return c, rows.Err()
}

type MailPage struct {
	Rows    []Row `json:"messages"`
	Next    int64 `json:"next_cursor"`
	Through int64 `json:"through"`
	Total   int   `json:"total"`
}

// A fixed upper sequence prevents continuously arriving mail extending a scan forever.
func (s *Store) Page(ctx context.Context, seat, mode string, after, through int64, limit int) (MailPage, error) {
	p := MailPage{Rows: []Row{}}
	if limit < 1 || limit > 200 || after < 0 || through < 0 {
		return p, errors.New("invalid page bounds")
	}
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	if through == 0 {
		if e = tx.QueryRowContext(ctx, `SELECT coalesce(max(seq),0) FROM messages`).Scan(&through); e != nil {
			return p, e
		}
	}
	p.Through = through
	cond := ""
	switch mode {
	case "all":
	case "unread":
		cond = " AND d.noticed_at IS NULL"
	case "pending":
		cond = " AND d.owed=1 AND d.answered_at IS NULL"
	default:
		return p, errors.New("invalid page mode")
	}
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM deliveries d WHERE d.seat=? AND d.seq<=?`+cond, seat, through).Scan(&p.Total); e != nil {
		return p, e
	}
	q, e := tx.QueryContext(ctx, `SELECT `+rowCols+` FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND d.seq>? AND d.seq<=?`+cond+` ORDER BY d.seq LIMIT ?`, seat, after, through, limit+1)
	if e != nil {
		return p, e
	}
	defer q.Close()
	pageBytes := 0
	more := false
	for q.Next() {
		var r Row
		var fin int
		if e = q.Scan(&r.Seq, &r.ID, &r.Sender, &r.ConversationID, &r.ParentID, &r.Subject, &r.Priority, &fin, &r.CreatedText, &r.createdMilli, &r.Body, &r.Noticed, &r.Answered); e != nil {
			return p, e
		}
		r.Final = fin == 1
		estimate := len(r.ID) + len(r.Subject) + len(r.ConversationID) + len(r.ParentID) + len(r.Sender) + 300
		if len(p.Rows) > 0 && pageBytes+estimate > 128<<10 {
			more = true
			break
		}
		pageBytes += estimate
		p.Rows = append(p.Rows, r)
	}
	if e = q.Err(); e != nil {
		return p, e
	}
	if more {
		p.Next = p.Rows[len(p.Rows)-1].Seq
	}
	if len(p.Rows) > limit {
		p.Rows = p.Rows[:limit]
		p.Next = p.Rows[limit-1].Seq
	}
	return p, nil
}
