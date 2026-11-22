package deliverystore

import (
	"context"
	"database/sql"
)

// Serialize schema changes across processes before inspecting the schema.
func (s *Store) nativeMigration(fn func(*sql.Conn) error) error {
	ctx := context.Background()
	c, e := s.db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, `BEGIN IMMEDIATE`); e != nil {
		return e
	}
	defer c.ExecContext(ctx, `ROLLBACK`)
	if e = fn(c); e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, `COMMIT`)
	return e
}
func connHasColumn(c *sql.Conn, table, col string) (bool, error) {
	var n int
	e := c.QueryRowContext(context.Background(), `SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n)
	return n == 1, e
}
