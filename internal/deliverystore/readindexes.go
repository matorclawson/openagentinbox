package deliverystore

import (
	"context"
	"database/sql"
)

// migrateReadIndexes runs under migrate's IMMEDIATE transaction. The additive
// index projection is backfilled once, never on every restart. Triggers maintain
// exact counters in the SAME transaction as deliveries, including imports,
// pruning, bulk acknowledgements and rollbacks; there is no eventual cache.
func migrateReadIndexes(ctx context.Context, conn *sql.Conn, have map[string]bool) error {
	if !have["priority"] {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE deliveries ADD COLUMN priority INTEGER NOT NULL DEFAULT 1;
UPDATE deliveries SET priority = (SELECT priority FROM messages WHERE messages.seq = deliveries.seq);`); err != nil {
			return err
		}
	}
	if !have["created_at"] {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE deliveries ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0; UPDATE deliveries SET created_at=(SELECT created_at FROM messages WHERE messages.seq=deliveries.seq)`); err != nil {
			return err
		}
	}

	var exists int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='mailbox_counts'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := conn.ExecContext(ctx, `CREATE TABLE mailbox_counts (
seat TEXT PRIMARY KEY, unread INTEGER NOT NULL CHECK(unread >= 0), pending INTEGER NOT NULL CHECK(pending >= 0)
) WITHOUT ROWID;
INSERT INTO mailbox_counts SELECT seat, sum(noticed_at IS NULL), sum(owed = 1 AND answered_at IS NULL) FROM deliveries GROUP BY seat;`); err != nil {
			return err
		}
	}
	_, err := conn.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS deliveries_unread_priority_created ON deliveries(seat,priority,created_at,seq) WHERE noticed_at IS NULL;
CREATE INDEX IF NOT EXISTS deliveries_pending_priority_created ON deliveries(seat,priority,created_at,seq) WHERE owed=1 AND answered_at IS NULL;
CREATE INDEX IF NOT EXISTS deliveries_unread_other_created ON deliveries(seat,created_at,seq) WHERE noticed_at IS NULL AND priority>0;
CREATE TRIGGER IF NOT EXISTS deliveries_created_insert AFTER INSERT ON deliveries
WHEN NEW.created_at != (SELECT created_at FROM messages WHERE seq=NEW.seq) BEGIN
 UPDATE deliveries SET created_at=(SELECT created_at FROM messages WHERE seq=NEW.seq) WHERE seat=NEW.seat AND seq=NEW.seq;
END;
CREATE INDEX IF NOT EXISTS deliveries_unread_priority ON deliveries(seat, priority, seq) WHERE noticed_at IS NULL;
CREATE INDEX IF NOT EXISTS deliveries_pending_priority ON deliveries(seat, priority, seq) WHERE owed = 1 AND answered_at IS NULL;
CREATE INDEX IF NOT EXISTS deliveries_unread_other ON deliveries(seat, seq) WHERE noticed_at IS NULL AND priority > 0;
-- Keep the additive projection correct for an older binary that omits priority.
CREATE TRIGGER IF NOT EXISTS deliveries_priority_insert AFTER INSERT ON deliveries
WHEN NEW.priority != (SELECT priority FROM messages WHERE seq=NEW.seq) BEGIN
 UPDATE deliveries SET priority=(SELECT priority FROM messages WHERE seq=NEW.seq) WHERE seat=NEW.seat AND seq=NEW.seq;
END;
CREATE TRIGGER IF NOT EXISTS deliveries_count_insert AFTER INSERT ON deliveries BEGIN
 INSERT INTO mailbox_counts(seat,unread,pending) VALUES(NEW.seat, NEW.noticed_at IS NULL, NEW.owed=1 AND NEW.answered_at IS NULL)
 ON CONFLICT(seat) DO UPDATE SET unread=unread+excluded.unread, pending=pending+excluded.pending;
END;
CREATE TRIGGER IF NOT EXISTS deliveries_count_delete AFTER DELETE ON deliveries BEGIN
 UPDATE mailbox_counts SET unread=unread-(OLD.noticed_at IS NULL), pending=pending-(OLD.owed=1 AND OLD.answered_at IS NULL) WHERE seat=OLD.seat;
END;
CREATE TRIGGER IF NOT EXISTS deliveries_count_update AFTER UPDATE OF seat,noticed_at,owed,answered_at ON deliveries BEGIN
 UPDATE mailbox_counts SET unread=unread-(OLD.noticed_at IS NULL), pending=pending-(OLD.owed=1 AND OLD.answered_at IS NULL) WHERE seat=OLD.seat;
 INSERT INTO mailbox_counts(seat,unread,pending) VALUES(NEW.seat,NEW.noticed_at IS NULL,NEW.owed=1 AND NEW.answered_at IS NULL)
 ON CONFLICT(seat) DO UPDATE SET unread=unread+excluded.unread, pending=pending+excluded.pending;
END;`)
	return err
}
