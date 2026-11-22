package deliverystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpgradeConsole moves the old web mailbox into the native store in place.
// Accounts, credential hashes, setup codes and sessions retain their identities.
// Conversion and retirement of the old table commit together.
func (s *Store) UpgradeConsole(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='letters'").Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		return nil
	}
	var cursor int64
	for {
		var seq int64
		var id, src, dst, subject, body, stamp, key string
		e = tx.QueryRowContext(ctx, "SELECT seq,id,src,dst,subject,body,at,operation FROM letters WHERE seq>? ORDER BY seq LIMIT 1", cursor).Scan(&seq, &id, &src, &dst, &subject, &body, &stamp, &key)
		if errors.Is(e, sql.ErrNoRows) {
			break
		}
		if e != nil {
			return e
		}
		at, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			return fmt.Errorf("invalid timestamp at sequence %d", seq)
		}
		_, inserted, e := s.acceptTx(ctx, tx, "console/"+src+"/"+key, Message{ID: id, Sender: src, Recipients: []string{dst}, Subject: subject, Body: []byte(body), Priority: 1}, at, seq)
		if e != nil {
			return e
		}
		if !inserted {
			return fmt.Errorf("operation collision at sequence %d", seq)
		}
		cursor = seq
	}
	var missing int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM letters l WHERE NOT EXISTS (SELECT 1 FROM messages m JOIN deliveries d USING(seq) WHERE m.seq=l.seq AND m.id=l.id AND m.sender=l.src AND d.seat=l.dst AND m.subject=l.subject AND m.body=CAST(l.body AS BLOB))`).Scan(&missing); e != nil {
		return e
	}
	if missing != 0 {
		return fmt.Errorf("%d unmatched console records", missing)
	}
	if _, e = tx.ExecContext(ctx, "DROP TABLE letters"); e != nil {
		return e
	}
	return tx.Commit()
}
