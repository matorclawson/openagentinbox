package console

import (
	"database/sql"
	"errors"
	"net/http"
)

const addressSchema = `
CREATE TABLE IF NOT EXISTS mailbox_ids(box TEXT PRIMARY KEY,id TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS addresses(address TEXT PRIMARY KEY,box TEXT NOT NULL,current INTEGER NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS current_address ON addresses(box) WHERE current=1;
CREATE TRIGGER IF NOT EXISTS reserve_alias BEFORE INSERT ON agents WHEN EXISTS(SELECT 1 FROM addresses WHERE address=NEW.name AND box<>NEW.name) BEGIN SELECT RAISE(ABORT,'Reserved mailbox address'); END;
CREATE TRIGGER IF NOT EXISTS mailbox_identity AFTER INSERT ON agents BEGIN
 INSERT INTO mailbox_ids VALUES(NEW.name,lower(hex(randomblob(16))));
 INSERT INTO addresses VALUES(NEW.name,NEW.name,1);
END;
INSERT OR IGNORE INTO mailbox_ids SELECT name,lower(hex(randomblob(16))) FROM agents WHERE name NOT IN(SELECT box FROM mailbox_ids);
INSERT OR IGNORE INTO addresses SELECT name,name,1 FROM agents WHERE name NOT IN(SELECT address FROM addresses);
`

func (s *server) mailboxIdentity(box string) (any, error) {
	var id, name string
	err := s.store.Reader().QueryRow("SELECT id,address FROM mailbox_ids JOIN addresses USING(box) WHERE box=? AND current=1", box).Scan(&id, &name)
	result := object{"mailbox_id": id, "name": name, "original_address": box}
	if domain := s.primaryDomain(); err == nil && domain != "" {
		if !validEmail(name + "@" + domain) {
			name = "agent+" + id
		}
		result["email_address"] = name + "@" + domain
		result["external_send_enabled"] = s.maySend(box)
	}
	return result, err
}

type addressMove struct{ From, To string }

// A preview runs the same writes and validations as apply inside a rolled-back transaction.
func (s *server) reconcile(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		Moves []addressMove
		Apply bool
	}
	if err := input(r, &q); err != nil {
		return nil, err
	}
	if len(q.Moves) == 0 || len(q.Moves) > 512 {
		return nil, failure{400, "Supply 1 to 512 mappings"}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	targets := map[string]bool{}
	rows := []object{}
	conflicts := 0
	changed := 0
	for _, m := range q.Moves {
		if !nameRE.MatchString(m.From) || !nameRE.MatchString(m.To) || m.To == s.operator {
			return nil, failure{400, "Invalid mailbox mapping"}
		}
		var box, id, current string
		status := "rename"
		err = tx.QueryRow(`SELECT a.box,i.id,c.address FROM addresses a JOIN mailbox_ids i USING(box)
     JOIN addresses c ON c.box=a.box AND c.current=1 WHERE a.address=?`, m.From).Scan(&box, &id, &current)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status = "source missing"
		case err != nil:
			return nil, err
		case seen[box] || targets[m.To]:
			status = "duplicate mapping"
		case current == m.To:
			status = "already current"
		case current != m.From:
			status = "source is a historical alias"
		default:
			var occupied bool
			err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM addresses WHERE address=?1 AND box<>?2 UNION ALL
     SELECT 1 FROM enrollments WHERE name=?1 AND expires>?3 UNION ALL SELECT 1 FROM roster WHERE name=?1 AND expires>?3)`, m.To, box, now()).Scan(&occupied)
			if err != nil {
				return nil, err
			}
			if occupied {
				status = "target occupied or reserved"
			}
		}
		seen[box] = true
		targets[m.To] = true
		if status == "rename" {
			if _, err = tx.Exec("UPDATE addresses SET current=0 WHERE box=? AND current=1", box); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("INSERT INTO addresses VALUES(?,?,1) ON CONFLICT(address) DO UPDATE SET current=1", m.To, box); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("INSERT INTO lifecycle(kind,name) VALUES('Renamed',?)", m.From+" → "+m.To); err != nil {
				return nil, err
			}
			changed++
		} else if status != "already current" {
			conflicts++
		}
		rows = append(rows, object{"from": m.From, "to": m.To, "mailbox_id": id, "status": status})
	}
	applied := q.Apply && conflicts == 0
	if applied {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		if changed > 0 {
			s.live.signal()
		}
	}
	return object{"rows": rows, "conflicts": conflicts, "applied": applied}, nil
}
