package deliverystore

import (
	"context"
	"database/sql"
)

func migratePriorityCounts(ctx context.Context, c *sql.Conn) error {
	var n int
	if e := c.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='priority_counts'`).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return nil
	}
	_, e := c.ExecContext(ctx, `
 CREATE TABLE priority_counts(seat TEXT NOT NULL,priority INTEGER NOT NULL,n INTEGER NOT NULL CHECK(n>=0),PRIMARY KEY(seat,priority)) WITHOUT ROWID;
 INSERT INTO priority_counts SELECT seat,priority,count(*) FROM deliveries WHERE noticed_at IS NULL GROUP BY seat,priority;
 CREATE TRIGGER priority_count_insert AFTER INSERT ON deliveries WHEN NEW.noticed_at IS NULL BEGIN
 INSERT INTO priority_counts VALUES(NEW.seat,NEW.priority,1) ON CONFLICT(seat,priority) DO UPDATE SET n=n+1;END;
 CREATE TRIGGER priority_count_delete AFTER DELETE ON deliveries WHEN OLD.noticed_at IS NULL BEGIN
 UPDATE priority_counts SET n=n-1 WHERE seat=OLD.seat AND priority=OLD.priority;END;
 CREATE TRIGGER priority_count_update AFTER UPDATE OF noticed_at,seat,priority ON deliveries BEGIN
 UPDATE priority_counts SET n=n-1 WHERE seat=OLD.seat AND priority=OLD.priority AND OLD.noticed_at IS NULL;
 INSERT INTO priority_counts SELECT NEW.seat,NEW.priority,1 WHERE NEW.noticed_at IS NULL ON CONFLICT(seat,priority) DO UPDATE SET n=n+1;END;
 `)
	return e
}
