// Package channelrequest coordinates ownership of channel-originated requests.
// Callers authenticate an actor and check conversation membership in the SAME
// transaction before applying an action. Names in message bodies grant no rights.
package channelrequest

import (
	"context"
	"database/sql"
	"errors"
)

const Schema = `
CREATE TABLE IF NOT EXISTS channel_request (
 id TEXT PRIMARY KEY, binding TEXT NOT NULL, assignee TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('assigned','taken','closed')),
 revision INTEGER NOT NULL DEFAULT 1, due INTEGER, escalated INTEGER,
 CHECK((state='assigned' AND due IS NOT NULL) OR (state!='assigned' AND due IS NULL))
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS channel_request_due ON channel_request(due,id)
WHERE state='assigned' AND escalated IS NULL;`

var ErrStale = errors.New("request owner, revision or state changed")

// The installing adapter issues one immutable request id for a committed event.
// A conflicting creation is an error; inbound event replay is handled before this call.
func Open(ctx context.Context, tx *sql.Tx, id, binding, assignee string, due int64) error {
	if id == "" || binding == "" || assignee == "" || due <= 0 {
		return errors.New("invalid request")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO channel_request(id,binding,assignee,state,due) VALUES(?,?,?,'assigned',?)", id, binding, assignee, due)
	return err
}

// Takeover cancels the old deadline and invalidates queued agent work. Assignment
// notifications and any cancellation of undispatched replies belong in this tx.
func Takeover(ctx context.Context, tx *sql.Tx, id string, revision int64, human string) error {
	if human == "" {
		return errors.New("missing human identity")
	}
	return change(ctx, tx, `UPDATE channel_request SET assignee=?,state='taken',due=NULL,revision=revision+1 WHERE id=? AND revision=? AND state='assigned'`, human, id, revision)
}

// Handoff also handles release by the human who took a request. The new owner
// receives a fresh deadline and a new revision for their response.
func Handoff(ctx context.Context, tx *sql.Tx, id string, revision int64, actor, next string, due int64) error {
	if next == "" || actor == next || due <= 0 {
		return errors.New("invalid handoff")
	}
	return change(ctx, tx, `UPDATE channel_request SET assignee=?,state='assigned',due=?,escalated=NULL,revision=revision+1 WHERE id=? AND revision=? AND assignee=? AND state!='closed'`, next, due, id, revision, actor)
}

// Resolve fences an answer at local commitment. An already dispatched remote
// effect needs its own receipt/reconciliation before promising a clean takeover.
func Resolve(ctx context.Context, tx *sql.Tx, id string, revision int64, actor string) error {
	return change(ctx, tx, `UPDATE channel_request SET state='closed',due=NULL,revision=revision+1 WHERE id=? AND revision=? AND assignee=? AND state!='closed'`, id, revision, actor)
}

// Escalate must share a transaction with enqueue of the escalation notice.
// Duplicate scans cannot create another notice for this assignment revision.
func Escalate(ctx context.Context, tx *sql.Tx, id string, revision, now int64) error {
	return change(ctx, tx, `UPDATE channel_request SET escalated=? WHERE id=? AND revision=? AND state='assigned' AND escalated IS NULL AND due<=?`, now, id, revision, now)
}

func change(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}
