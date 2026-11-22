package deliverystore

import (
	"context"
	"database/sql"
	"slices"
)

// HistoryBefore selects newest keys first and returns the selected page oldest
// first. Milliseconds already include the zone offset. Six additional digits
// distinguish values inside the same millisecond without SQLite date rounding.
func (s *Store) HistoryBefore(ctx context.Context, seat string, limit int, t *int64, id string) (View, bool, error) {
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return View{}, false, e
	}
	defer tx.Rollback()
	var v View
	if e = tx.QueryRowContext(ctx, "SELECT coalesce((SELECT unread FROM mailbox_counts WHERE seat=?),0)", seat).Scan(&v.Total); e != nil {
		return v, false, e
	}
	query := "SELECT " + rowCols + " FROM history_keys h JOIN deliveries d ON (d.seat,d.seq)=(h.seat,h.seq) JOIN messages m ON m.seq=h.seq WHERE h.seat=?"
	args := []any{seat}
	if t != nil {
		query += " AND (h.t,h.id)<(?,?)"
		args = append(args, *t, id)
	}
	q, e := tx.QueryContext(ctx, query+" ORDER BY h.t DESC,h.id DESC LIMIT ?", append(args, limit+1)...)
	if e != nil {
		return v, false, e
	}
	defer q.Close()
	for q.Next() {
		var r Row
		if e = q.Scan(&r.Seq, &r.ID, &r.Sender, &r.ConversationID, &r.ParentID, &r.Subject, &r.Priority, &r.Final, &r.CreatedText, &r.createdMilli, &r.Body, &r.Noticed, &r.Answered); e != nil {
			return v, false, e
		}
		v.Rows = append(v.Rows, r)
	}
	more := len(v.Rows) > limit
	if more {
		v.Rows = v.Rows[:limit]
	}
	slices.Reverse(v.Rows)
	return v, more, q.Err()
}

// installHistoryKeys builds the covering position index once. Imports may
// preserve a different timestamp on each recipient's copy of the same post.
func installHistoryKeys(ctx context.Context, c *sql.Conn) error {
	var exists bool
	if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='history_keys')").Scan(&exists); err != nil || exists {
		return err
	}
	const timestamp = "coalesce(nullif(d.created_text,''),m.created_text)"
	// Remove the zone suffix before padding short fractional seconds with zeroes.
	const fraction = "rtrim(substr(" + timestamp + ",21),'Z')"
	const digits = "substr(" + fraction + ",1,case when instr(" + fraction + ",'+')>0 then instr(" + fraction + ",'+')-1 when instr(" + fraction + ",'-')>0 then instr(" + fraction + ",'-')-1 else length(" + fraction + ") end)"
	const nanos = "(coalesce(d.created_milli,m.created_at)*1000000 + CASE WHEN substr(" + timestamp + ",20,1)='.' THEN cast(substr(" + digits + "||'000000000',4,6) as integer) ELSE 0 END)"

	const keys = "SELECT d.seat,d.seq," + nanos + ",m.id FROM deliveries d JOIN messages m ON m.seq=d.seq"
	const refresh = "INSERT OR REPLACE INTO history_keys " + keys + " WHERE d.seat=NEW.seat AND d.seq=NEW.seq;"
	_, err := c.ExecContext(ctx, `CREATE TABLE history_keys(seat TEXT NOT NULL,seq INTEGER NOT NULL,t INTEGER NOT NULL,id TEXT NOT NULL,PRIMARY KEY(seat,seq),FOREIGN KEY(seat,seq) REFERENCES deliveries(seat,seq) ON UPDATE CASCADE ON DELETE CASCADE) WITHOUT ROWID;
 INSERT INTO history_keys `+keys+`;
 CREATE UNIQUE INDEX history_positions ON history_keys(seat,t,id);
 CREATE TRIGGER history_insert AFTER INSERT ON deliveries BEGIN `+refresh+` END;
 CREATE TRIGGER history_update AFTER UPDATE OF created_text,created_milli ON deliveries BEGIN `+refresh+` END;`)
	return err
}
