package console

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

// The preview binds addresses, mailbox IDs and credential generations. A rotation
// between preview and apply requires a new preview. Only current addresses qualify.
func (s *server) revokeBatch(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		Names   []string
		Preview string
	}
	if err := input(r, &q); err != nil {
		return nil, err
	}
	if len(q.Names) == 0 || len(q.Names) > 512 {
		return nil, failure{400, "Supply 1 to 512 current addresses"}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	digest := sha256.New()
	rows := make([]object, 0, len(q.Names))
	active := []string{}
	conflicts := 0
	for _, name := range q.Names {
		if !nameRE.MatchString(name) || name == s.operator || seen[name] {
			return nil, failure{400, "Invalid, duplicate or protected mailbox name"}
		}
		seen[name] = true
		var id, box, current string
		var key sql.NullString
		err = tx.QueryRowContext(r.Context(), `SELECT i.id,a.box,c.address,g.key FROM addresses a JOIN mailbox_ids i USING(box)
      JOIN addresses c ON c.box=a.box AND c.current=1 JOIN agents g ON g.name=a.box WHERE a.address=?`, name).Scan(&id, &box, &current, &key)
		status := "will revoke"
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status = "unknown address"
			conflicts++
		case err != nil:
			return nil, err
		case current != name:
			status = "historical alias"
			conflicts++
		case !key.Valid:
			status = "already revoked"
		default:
			active = append(active, box)
		}
		fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%s\x00", name, id, current, key.String)
		rows = append(rows, object{"name": name, "mailbox_id": id, "current": current, "status": status})
	}
	preview := fmt.Sprintf("%x", digest.Sum(nil))
	applied := q.Preview != ""
	if applied {
		if conflicts > 0 || q.Preview != preview {
			return nil, failure{409, "Mailbox list changed or contains conflicts. Preview again."}
		}
		for _, box := range active {
			if _, err = tx.ExecContext(r.Context(), "UPDATE agents SET key=NULL WHERE name=?", box); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		if len(active) > 0 {
			s.live.signal()
		}
	}
	return object{"rows": rows, "preview": preview, "count": len(active), "conflicts": conflicts, "applied": applied}, nil
}
