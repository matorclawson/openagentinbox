// Package channelqueue stores external delivery obligations. The adapter host
// installs the schema when enabling the first adapter. An email-only install
// continues to use its existing schema.
package channelqueue

import (
	"context"
	"database/sql"
	"errors"
)

const Schema = `
CREATE TABLE IF NOT EXISTS channel_outbox (
  installation TEXT NOT NULL, operation TEXT NOT NULL, binding TEXT NOT NULL,
  message TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued'
    CHECK(state IN ('queued','sending','uncertain','delivered','failed')),
  due INTEGER NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, evidence TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(installation,operation)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS channel_due ON channel_outbox(installation,due,operation) WHERE state='queued';`

type Item struct {
	Installation, Operation, Binding, Message string
	Attempt                                   int
}

var ErrStale = errors.New("delivery attempt or state changed")
var ErrConflict = errors.New("operation already bound to different content")

// Enqueue runs in the same transaction as the immutable message revision it
// references. Reusing an operation with another destination or revision fails.
func Enqueue(ctx context.Context, tx *sql.Tx, x Item, due int64) error {
	if x.Installation == "" || x.Operation == "" || x.Binding == "" || x.Message == "" {
		return errors.New("missing delivery identity")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO channel_outbox(installation,operation,binding,message,due)
	VALUES(?,?,?,?,?) ON CONFLICT(installation,operation) DO NOTHING`, x.Installation, x.Operation, x.Binding, x.Message, due)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 1 {
		return err
	}
	var binding, message string
	err = tx.QueryRowContext(ctx, "SELECT binding,message FROM channel_outbox WHERE installation=? AND operation=?", x.Installation, x.Operation).Scan(&binding, &message)
	if err == nil && (binding != x.Binding || message != x.Message) {
		return ErrConflict
	}
	return err
}

// Claim returns sql.ErrNoRows when this installation has nothing due.
func Claim(ctx context.Context, db *sql.DB, installation string, now int64) (Item, error) {
	var x Item
	err := db.QueryRowContext(ctx, `UPDATE channel_outbox SET state='sending',attempt=attempt+1
	WHERE installation=?1 AND operation=(SELECT operation FROM channel_outbox WHERE installation=?1 AND state='queued' AND due<=?2 ORDER BY due,operation LIMIT 1)
	RETURNING installation,operation,binding,message,attempt`, installation, now).Scan(&x.Installation, &x.Operation, &x.Binding, &x.Message, &x.Attempt)
	return x, err
}

// Recover follows exclusive acquisition of the installation worker lock. A
// crash may have happened on either side of remote acceptance.
func Recover(ctx context.Context, db *sql.DB, installation string) error {
	_, err := db.ExecContext(ctx, "UPDATE channel_outbox SET state='uncertain' WHERE installation=? AND state='sending'", installation)
	return err
}

type Outcome uint8

const (
	Ambiguous Outcome = iota
	Confirmed
	Rejected
	Retryable // positive evidence that the remote did not accept the operation
)

func Finish(ctx context.Context, db *sql.DB, x Item, outcome Outcome, due int64) error {
	return settle(ctx, db, x, "sending", outcome, due, "")
}

// Reconcile requires a stable non-secret protocol or operator evidence reference.
func Reconcile(ctx context.Context, db *sql.DB, x Item, outcome Outcome, due int64, reference string) error {
	if reference == "" || len(reference) > 256 || outcome == Ambiguous {
		return errors.New("missing conclusive evidence")
	}
	return settle(ctx, db, x, "uncertain", outcome, due, reference)
}

func settle(ctx context.Context, db *sql.DB, x Item, from string, result Outcome, due int64, evidence string) error {
	var state string
	switch result {
	case Ambiguous:
		state = "uncertain"
	case Confirmed:
		state = "delivered"
	case Rejected:
		state = "failed"
	case Retryable:
		state = "queued"
	default:
		return errors.New("invalid delivery outcome")
	}
	res, err := db.ExecContext(ctx, `UPDATE channel_outbox SET state=?,due=?,evidence=? WHERE installation=? AND operation=? AND attempt=? AND state=?`, state, due, evidence, x.Installation, x.Operation, x.Attempt, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrStale
	}
	return err
}
