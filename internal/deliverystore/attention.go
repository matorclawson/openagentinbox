package deliverystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Attention is a change detector, not a delivery receipt or a wake decision.
// Counts cover the entire mailbox, even when the preview cannot fit more rows.
// P2, receipt retries and activity heartbeats do not invalidate this view.
type Attention struct {
	Schema   string        `json:"schema"`
	Revision string        `json:"revision"`
	Urgent   int           `json:"pending_p0"`
	Requests int           `json:"pending_p1"`
	Unread   int           `json:"unread_actionable"`
	Entries  []DigestEntry `json:"entries"`
	More     bool          `json:"has_more"`
}

func migrateAttention(ctx context.Context, c *sql.Conn) error {
	var exists int
	if err := c.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='attention_state'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := c.ExecContext(ctx, `CREATE TABLE attention_state (
   seat TEXT PRIMARY KEY, revision TEXT NOT NULL,
   p0 INTEGER NOT NULL CHECK(p0>=0), p1 INTEGER NOT NULL CHECK(p1>=0), unread INTEGER NOT NULL CHECK(unread>=0)
  ) WITHOUT ROWID;
  INSERT INTO attention_state SELECT d.seat,lower(hex(randomblob(16))),
   sum(m.priority=0 AND d.owed=1 AND d.answered_at IS NULL),
   sum(m.priority=1 AND d.owed=1 AND d.answered_at IS NULL),sum(m.priority<2 AND d.noticed_at IS NULL)
   FROM deliveries d JOIN messages m ON m.seq=d.seq GROUP BY d.seat;`); err != nil {
			return err
		}
	}
	// Read immutable priority from messages, so old binaries that omit the
	// deliveries.priority projection and trigger ordering remain safe.
	add := `INSERT INTO attention_state(seat,revision,p0,p1,unread)
 SELECT NEW.seat,lower(hex(randomblob(16))),
  m.priority=0 AND NEW.owed=1 AND NEW.answered_at IS NULL,
  m.priority=1 AND NEW.owed=1 AND NEW.answered_at IS NULL,NEW.noticed_at IS NULL
 FROM messages m WHERE m.seq=NEW.seq AND m.priority<2
 ON CONFLICT(seat) DO UPDATE SET revision=excluded.revision,
 p0=p0+excluded.p0,p1=p1+excluded.p1,unread=unread+excluded.unread;`
	remove := `UPDATE attention_state SET revision=lower(hex(randomblob(16))),
 p0=p0-((SELECT priority FROM messages WHERE seq=OLD.seq)=0 AND OLD.owed=1 AND OLD.answered_at IS NULL),
 p1=p1-((SELECT priority FROM messages WHERE seq=OLD.seq)=1 AND OLD.owed=1 AND OLD.answered_at IS NULL),
 unread=unread-(OLD.noticed_at IS NULL)
 WHERE seat=OLD.seat AND (SELECT priority FROM messages WHERE seq=OLD.seq)<2;`
	_, err := c.ExecContext(ctx, fmt.Sprintf(`
 CREATE TRIGGER IF NOT EXISTS attention_insert AFTER INSERT ON deliveries BEGIN %s END;
 CREATE TRIGGER IF NOT EXISTS attention_delete BEFORE DELETE ON deliveries BEGIN %s END;
 CREATE TRIGGER IF NOT EXISTS attention_update AFTER UPDATE OF seat,noticed_at,owed,answered_at ON deliveries
 WHEN OLD.seat IS NOT NEW.seat OR OLD.noticed_at IS NOT NEW.noticed_at OR OLD.owed IS NOT NEW.owed OR OLD.answered_at IS NOT NEW.answered_at
 BEGIN %s %s END;
 CREATE TRIGGER IF NOT EXISTS attention_link_insert AFTER INSERT ON message_links BEGIN
 UPDATE attention_state SET revision=lower(hex(randomblob(16))) WHERE seat=NEW.seat AND EXISTS(SELECT 1 FROM deliveries WHERE seat=NEW.seat AND seq=NEW.seq AND owed=1 AND answered_at IS NULL); END;
 CREATE TRIGGER IF NOT EXISTS attention_link_update AFTER UPDATE ON message_links WHEN OLD.task_ref IS NOT NEW.task_ref BEGIN
 UPDATE attention_state SET revision=lower(hex(randomblob(16))) WHERE seat=NEW.seat AND EXISTS(SELECT 1 FROM deliveries WHERE seat=NEW.seat AND seq=NEW.seq AND owed=1 AND answered_at IS NULL); END;
 CREATE TRIGGER IF NOT EXISTS attention_link_delete AFTER DELETE ON message_links BEGIN
 UPDATE attention_state SET revision=lower(hex(randomblob(16))) WHERE seat=OLD.seat AND EXISTS(SELECT 1 FROM deliveries WHERE seat=OLD.seat AND seq=OLD.seq AND owed=1 AND answered_at IS NULL); END;
 `, add, remove, remove, add))
	return err
}

func (s *Store) Attention(ctx context.Context, seat string) (Attention, error) {
	a := Attention{Schema: "openagentinbox.attention/v1", Revision: "empty", Entries: []DigestEntry{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT revision,p0,p1,unread FROM attention_state WHERE seat=?`, seat).Scan(&a.Revision, &a.Urgent, &a.Requests, &a.Unread)
	if err != nil && err != sql.ErrNoRows {
		return a, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.sender,substr(m.subject,1,160),d.priority,COALESCE(l.task_ref,'') FROM deliveries d INDEXED BY deliveries_pending_priority JOIN messages m ON m.seq=d.seq LEFT JOIN message_links l ON l.seat=d.seat AND l.seq=d.seq WHERE d.seat=? AND d.owed=1 AND d.answered_at IS NULL ORDER BY d.priority,d.seq LIMIT 5`, seat)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var r DigestEntry
		if err = rows.Scan(&r.ID, &r.From, &r.Subject, &r.Priority, &r.TaskRef); err != nil {
			return a, err
		}
		r.Subject = clip(r.Subject, 160)
		a.Entries = append(a.Entries, r)
		raw, _ := json.Marshal(a)
		if len(raw) > 8000 {
			a.Entries = a.Entries[:len(a.Entries)-1]
			break
		}
	}
	a.More = a.Urgent+a.Requests > len(a.Entries)
	return a, rows.Err()
}
