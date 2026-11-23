package console

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const pairSchema = `
CREATE TABLE IF NOT EXISTS paired_cli(
 id TEXT PRIMARY KEY, label TEXT NOT NULL, code_hash TEXT UNIQUE, deadline INTEGER NOT NULL,
 credential_hash TEXT UNIQUE, paired_at INTEGER);
CREATE TRIGGER IF NOT EXISTS remove_cli_roster AFTER DELETE ON paired_cli
 BEGIN DELETE FROM roster WHERE host=OLD.id; END;`

func (s *server) pairRoutes(add func(string, handler)) {
	add("GET /api/agent/probe/{op}", s.probe)
	add("POST /api/agent/probe/confirm", func(w http.ResponseWriter, r *http.Request) (any, error) {
		r.SetPathValue("op", "confirm")
		return s.probe(w, r)
	})
	add("POST /api/agent/listener", s.listenerCheck)
	add("POST /api/desk/onboarding", s.authorizeOnboarding)
	add("POST /api/agent/onboarding/problem", s.onboardingProblem)
	add("POST /api/agent/onboarding/work", s.onboardingWork)
	add("POST /api/agent/onboarding/confirm", s.confirmOnboarding)
	add("GET /api/agent/authorization", s.seatAuthorization)
	add("POST /api/desk/pair", s.createPair)
	add("POST /api/desk/pair/revoke", s.revokePair)
	add("POST /api/desk/pair/roster", s.addPairRoster)
	add("POST /api/agent/pair", s.exchangePair)
	add("GET /api/agent/pair/status", s.pairStatus)
	add("POST /api/agent/pair/tickets", s.pairTickets)
}

// Before a paired client requests its tickets, reserve each name using an unguessable placeholder.
func reserveNames(tx *sql.Tx, id, operator string, names []string, deadline int64) error {
	if len(names) > 512 {
		return failure{400, "Use at most 512 names per roster"}
	}
	if _, e := tx.Exec("DELETE FROM roster WHERE expires<=?", now()); e != nil {
		return e
	}
	for _, n := range names {
		if !nameRE.MatchString(n) || n == operator {
			return failure{400, "Invalid agent name"}
		}
		result, e := tx.Exec("INSERT OR IGNORE INTO roster(name,ticket,host,expires) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM addresses WHERE address=?)", n, hash(token()), id, deadline, n)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count != 1 {
			return failure{409, "Inbox already assigned or reserved: " + n}
		}
	}
	return nil
}
func (s *server) createPair(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Names []string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	deadline := now() + 600000
	id := "cli:" + uuid.NewString()
	code := token()
	if _, e = tx.Exec("DELETE FROM paired_cli WHERE paired_at IS NULL AND deadline<=?", now()); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO paired_cli(id,label,code_hash,deadline) VALUES(?,'',?,?)", id, hash(code), deadline); e != nil {
		return nil, e
	}
	if e = s.assignOnboarding(tx, id, q.Names, deadline); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"id": id, "code": code, "expires": deadline}, nil
}
func (s *server) exchangePair(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Code, Label string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	cred := bearer(r)
	if cred == "" || !bearerRE.MatchString(q.Code) || q.Code == cred {
		return nil, failure{403, "Invalid pairing request"}
	}
	label := strings.TrimSpace(q.Label)
	if label == "" || len(label) > 253 {
		return nil, failure{400, "Invalid client label"}
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id string
	e = tx.QueryRow("UPDATE paired_cli SET label=?,code_hash=NULL,credential_hash=?,paired_at=? WHERE code_hash=? AND deadline>? AND paired_at IS NULL RETURNING id", label, hash(cred), now(), hash(q.Code), now()).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, failure{403, "Pairing token expired, canceled, or already used"}
	}
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE roster SET expires=? WHERE host=?", now()+86400000, id); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("UPDATE onboarding SET expires=? WHERE host=?", now()+86400000, id); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('CLI paired',?)", label); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"id": id, "label": label, "status": "paired"}, nil
}
func (s *server) pairStatus(_ http.ResponseWriter, r *http.Request) (any, error) {
	var id, label string
	e := s.db.QueryRowContext(r.Context(), "SELECT id,label FROM paired_cli WHERE credential_hash=?", hash(bearer(r))).Scan(&id, &label)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, failure{401, "CLI login required"}
	}
	if e != nil {
		return nil, e
	}
	return object{"id": id, "label": label, "status": "paired"}, nil
}
func (s *server) revokePair(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ ID string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var label string
	if e = tx.QueryRow("DELETE FROM paired_cli WHERE id=? RETURNING label", q.ID).Scan(&label); e != nil {
		return nil, e
	}
	if label != "" {
		if _, e = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('CLI access revoked',?)", label); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"ok": true}, nil
}
func (s *server) addPairRoster(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		ID    string
		Names []string
	}
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if len(q.Names) == 0 {
		return nil, failure{400, "Enter at least one name"}
	}
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id string
	if e = tx.QueryRow("SELECT id FROM paired_cli WHERE id=? AND paired_at IS NOT NULL", q.ID).Scan(&id); e != nil {
		return nil, e
	}
	if e = reserveNames(tx, id, s.operator, q.Names, now()+86400000); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	s.live.signal()
	return object{"reserved": len(q.Names)}, nil
}
func (s *server) pairTickets(_ http.ResponseWriter, r *http.Request) (any, error) {
	cred := bearer(r)
	tx, e := s.db.BeginTx(r.Context(), nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id string
	if e = tx.QueryRow("SELECT id FROM paired_cli WHERE credential_hash=?", hash(cred)).Scan(&id); errors.Is(e, sql.ErrNoRows) {
		return nil, failure{401, "CLI login required"}
	} else if e != nil {
		return nil, e
	}
	rows, e := tx.Query("SELECT name FROM roster WHERE host=? AND expires>? ORDER BY name", id, now())
	if e != nil {
		return nil, e
	}
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			rows.Close()
			return nil, e
		}
		names = append(names, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, n := range names {
		digest := hash(rosterTicket(cred, n))
		if _, e = tx.Exec("UPDATE roster SET ticket=? WHERE name=? AND host=? AND ticket!=?", digest, n, id, digest); e != nil {
			return nil, e
		}
	}
	return object{"names": names}, tx.Commit()
}
