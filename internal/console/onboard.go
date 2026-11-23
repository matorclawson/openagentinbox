package console

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

const onboardingSchema = `
CREATE TABLE IF NOT EXISTS onboarding(
 id TEXT PRIMARY KEY, host TEXT NOT NULL, name TEXT NOT NULL, mailbox TEXT NOT NULL,
 owner TEXT NOT NULL, issued INTEGER NOT NULL, expires INTEGER NOT NULL, verified INTEGER NOT NULL DEFAULT 0,problem TEXT NOT NULL DEFAULT '',
 UNIQUE(host,name));
CREATE TRIGGER IF NOT EXISTS remove_onboarding AFTER DELETE ON paired_cli BEGIN
 DELETE FROM onboarding WHERE host=OLD.id; END;
CREATE TRIGGER IF NOT EXISTS invalidate_onboarding AFTER UPDATE OF key ON agents WHEN NEW.key IS NOT OLD.key BEGIN
 UPDATE onboarding SET verified=0 WHERE mailbox=(SELECT id FROM mailbox_ids WHERE box=NEW.name); END;
`

func (s *server) authorizeOnboarding(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		ID    string
		Names []string
	}
	if err := input(r, &q); err != nil {
		return nil, err
	}
	if len(q.Names) == 0 || len(q.Names) > 512 {
		return nil, failure{400, "Choose 1 to 512 seats"}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var paired bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM paired_cli WHERE id=? AND paired_at IS NOT NULL)", q.ID).Scan(&paired); err != nil {
		return nil, err
	}
	if !paired {
		return nil, failure{404, "Paired client not found"}
	}
	expires := now() + 86400000
	if err = s.assignOnboarding(tx, q.ID, q.Names, expires); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.live.signal()
	return object{"authorized": len(q.Names), "expires": expires}, nil
}
func (s *server) assignOnboarding(tx *sql.Tx, host string, names []string, expires int64) error {
	if len(names) > 512 {
		return failure{400, "At most 512 seats per request"}
	}
	var owner string
	if err := tx.QueryRow("SELECT username FROM owner,paired_cli WHERE paired_cli.id=?", host).Scan(&owner); err != nil {
		return err
	}
	issued := now()
	var err error
	seen := map[string]bool{}
	for _, name := range names {
		if !nameRE.MatchString(name) || name == s.operator || seen[name] {
			return failure{400, "Invalid or duplicate seat name"}
		}
		seen[name] = true
		var mailbox string
		err = tx.QueryRow("SELECT id FROM addresses JOIN mailbox_ids USING(box) WHERE address=? AND current=1", name).Scan(&mailbox)
		if errors.Is(err, sql.ErrNoRows) {
			var existing int
			if err = tx.QueryRow("SELECT count(*) FROM roster WHERE name=? AND host=? AND expires>?", name, host, issued).Scan(&existing); err != nil {
				return err
			}
			if existing == 0 {
				if err = reserveNames(tx, host, s.operator, []string{name}, expires); err != nil {
					return err
				}
			} else if _, err = tx.Exec("UPDATE roster SET expires=? WHERE name=? AND host=?", expires, name, host); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO onboarding(id,host,name,mailbox,owner,issued,expires) VALUES(?,?,?,?,?,?,?)
    ON CONFLICT(host,name) DO UPDATE SET id=excluded.id,mailbox=excluded.mailbox,owner=excluded.owner,issued=excluded.issued,expires=excluded.expires,verified=0,problem=''`, uuid.NewString(), host, name, mailbox, owner, issued, expires)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) onboardingWork(_ http.ResponseWriter, r *http.Request) (any, error) {
	cred := bearer(r)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var host string
	if err = tx.QueryRow("SELECT id FROM paired_cli WHERE credential_hash=?", hash(cred)).Scan(&host); errors.Is(err, sql.ErrNoRows) {
		return nil, failure{401, "CLI login required"}
	} else if err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT id,name,mailbox FROM onboarding WHERE host=? AND expires>? AND verified=0 ORDER BY name", host, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	work := []object{}
	for rows.Next() {
		var id, name, mailbox string
		if err = rows.Scan(&id, &name, &mailbox); err != nil {
			return nil, err
		}
		work = append(work, object{"id": id, "name": name, "mailbox_id": mailbox})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, w := range work {
		if w["mailbox_id"] == "" {
			name := w["name"].(string)
			ticket := rosterTicket(cred, name)
			res, e := tx.Exec("UPDATE roster SET ticket=? WHERE host=? AND name=? AND expires>?", hash(ticket), host, name, now())
			if e != nil {
				return nil, e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return nil, e
			}
			if n == 1 {
				w["ticket"] = ticket
			}
		}
	}
	return object{"seats": work}, tx.Commit()
}

func (s *server) confirmOnboarding(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ ID, MailboxID, HostCredential string }
	if err := input(r, &q); err != nil {
		return nil, err
	}
	// Both credentials must still be active, and the operator must have approved this exact host and current seat.
	res, err := s.db.ExecContext(r.Context(), `UPDATE onboarding SET problem='',verified=CASE WHEN verified=0 THEN ?1 ELSE verified END,mailbox=?2
 WHERE id=?3 AND expires>?1 AND (mailbox='' OR mailbox=?2)
 AND host=(SELECT id FROM paired_cli WHERE credential_hash=?4)
 AND name=(SELECT address FROM agents JOIN mailbox_ids ON box=agents.name JOIN addresses USING(box)
 WHERE mailbox_ids.id=?2 AND addresses.current=1 AND agents.key=?5)`, now(), q.MailboxID, q.ID, hash(q.HostCredential), hash(bearer(r)))
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, failure{403, "Authorization or identity no longer matches"}
	}
	s.live.signal()
	return object{"verified": true}, nil
}
func (s *server) seatAuthorization(_ http.ResponseWriter, r *http.Request) (any, error) {
	box, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	var owner, host, name, id, grantID, hostID string
	var issued, verified, expires int64
	err = s.db.QueryRowContext(r.Context(), `SELECT o.id,c.id,o.owner,c.label,o.name,o.mailbox,o.issued,o.verified,o.expires
 FROM onboarding o JOIN paired_cli c ON c.id=o.host JOIN mailbox_ids i ON i.id=o.mailbox JOIN addresses a ON a.box=i.box AND a.current=1 AND a.address=o.name
 WHERE i.box=? ORDER BY o.issued DESC LIMIT 1`, box).Scan(&grantID, &hostID, &owner, &host, &name, &id, &issued, &verified, &expires)
	return object{"authorization_id": grantID, "paired_client_id": hostID, "authorized_by": owner, "host": host, "name": name, "mailbox_id": id, "issued_at": issued, "expires_at": expires, "verified_at": verified, "scope": "native CLI enrollment and inbox access verification"}, err
}

func (s *server) onboardingProblem(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ ID, Reason string }
	if err := input(r, &q); err != nil {
		return nil, err
	}
	switch q.Reason {
	case "invalid-seat", "profile-missing", "profile-conflict", "identity-failed", "enrollment-failed", "inbox-unavailable", "confirmation-failed":
	default:
		return nil, failure{400, "Unknown status"}
	}
	result, err := s.db.ExecContext(r.Context(), `UPDATE onboarding SET problem=? WHERE id=? AND host=(SELECT id FROM paired_cli WHERE credential_hash=?) AND expires>? AND verified=0 AND problem<>?`, q.Reason, q.ID, hash(bearer(r)), now(), q.Reason)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.live.signal()
	}
	return object{"ok": true}, nil
}
