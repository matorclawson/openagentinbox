// Package channelbinding stores operator-approved installation bindings. Adapter
// workers never get a database handle or permission to create these grants.
package channelbinding

import (
	"context"
	"database/sql"
	"errors"
)

const Schema = `
CREATE TABLE IF NOT EXISTS channel_installation(id TEXT PRIMARY KEY,enabled INTEGER NOT NULL CHECK(enabled IN (0,1))) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS channel_identity(installation TEXT NOT NULL,subject TEXT NOT NULL,participant TEXT NOT NULL,
 PRIMARY KEY(installation,subject)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS channel_binding(installation TEXT NOT NULL,remote TEXT NOT NULL,conversation TEXT NOT NULL,
 seat TEXT NOT NULL,active INTEGER NOT NULL CHECK(active IN (0,1)),PRIMARY KEY(installation,remote,seat)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS channel_membership(installation TEXT NOT NULL,remote TEXT NOT NULL,subject TEXT NOT NULL,
 PRIMARY KEY(installation,remote,subject)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS channel_disclosure(installation TEXT NOT NULL,remote TEXT NOT NULL,seat TEXT NOT NULL,
 audience_version TEXT NOT NULL, PRIMARY KEY(installation,remote,seat,audience_version)) WITHOUT ROWID;`

type Source struct{ Installation, Remote, Subject, Seat string }
type Audience struct{ Conversation, Participant string }

// Lookup is performed inside the message acceptance transaction. Empty participant
// is an admitted external sender with no approved authority. Missing membership
// or an inactive binding refuses even a previously mapped sender.
func Lookup(ctx context.Context, tx *sql.Tx, s Source) (Audience, error) {
	var a Audience
	err := tx.QueryRowContext(ctx, `SELECT b.conversation,coalesce(p.participant,'') FROM channel_binding b
	JOIN channel_installation i ON i.id=b.installation AND i.enabled=1
	JOIN channel_membership m ON m.installation=b.installation AND m.remote=b.remote AND m.subject=?3
	LEFT JOIN channel_identity p ON p.installation=b.installation AND p.subject=m.subject
	WHERE b.installation=?1 AND b.remote=?2 AND b.seat=?4 AND b.active=1`, s.Installation, s.Remote, s.Subject, s.Seat).Scan(&a.Conversation, &a.Participant)
	return a, err
}

// FirstContact reserves a disclosure for a particular audience revision. Call
// only after checking current membership; enqueue the frozen introductory text
// in this transaction. Rolling back the message releases the reservation.
func FirstContact(ctx context.Context, tx *sql.Tx, installation, remote, seat, audienceVersion string) (bool, error) {
	if installation == "" || remote == "" || seat == "" || audienceVersion == "" {
		return false, errors.New("missing disclosure audience")
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO channel_disclosure VALUES(?,?,?,?) ON CONFLICT DO NOTHING", installation, remote, seat, audienceVersion)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
