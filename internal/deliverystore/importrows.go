package deliverystore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// ImportDelivery is one recipient's copy of an imported message with its mutable state, in unix milliseconds; 0 means "not set".
type ImportDelivery struct {
	Seat, AnswerID                              string
	RecordJSON                                  string
	DeliveredMilli, NoticedMilli, AnsweredMilli int64
	// CreatedText and CreatedMilli are THIS recipient's file's created_at: the time the program shows this seat (zero/empty: the message's).
	CreatedText  string
	CreatedMilli int64
}

// ImportRow is one message as read from the file store, for the Phase A importer (store v2 integration design, section 4). Unlike Accept it
// carries timestamps and states: it exists to reproduce what the file store holds, not to accept new mail.
type ImportRow struct {
	ID, Sender, ConversationID, ParentID, Subject string
	RecordJSON                                    string
	Recipients                                    []string
	Priority                                      int
	Final                                         bool // the sender marked it final: it expects no answer, so it is never owed
	CreatedMilli                                  int64
	CreatedText                                   string // the file store's own created_at string, so the REST answer repeats it exactly
	Body                                          []byte
	Deliveries                                    []ImportDelivery
}

// ImportBatch writes rows in the order given (the caller sorts by creation time, so seq follows arrival) in one transaction. It is
// idempotent and refreshes state: a message already present keeps its seq; a delivery already present only moves FORWARD (a stamp
// that was set is never cleared, a missing stamp is filled), so re-importing a newer copy of the file store updates read and answered
// state without duplicating anything. owed follows the CLI's pending rule: p0/p1 and not final (a message to its own sender is owed, as the CLI lists it).
func (s *Store) ImportBatch(rows []ImportRow) (inserted, updated int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	nz := func(v int64) any {
		if v == 0 {
			return nil
		}
		return v
	}
	for _, r := range rows {
		if r.RecordJSON == "" {
			r.RecordJSON = "{}"
		}
		if !json.Valid([]byte(r.RecordJSON)) {
			return 0, 0, fmt.Errorf("invalid post metadata %s", r.ID)
		}
		if len(r.Body) > MaxMessageBytes || len(r.Subject) > MaxSubjectBytes || len(r.RecordJSON) > 64<<10 || len(r.Deliveries) > MaxRecipients {
			return 0, 0, fmt.Errorf("message %s exceeds import bounds", r.ID)
		}
		var existing []byte
		err := tx.QueryRow(`SELECT body FROM messages WHERE id=?`, r.ID).Scan(&existing)
		if err == nil && PayloadSHA256(existing) != PayloadSHA256(r.Body) {
			return 0, 0, fmt.Errorf("message %s body conflict", r.ID)
		}
		if err != nil && err != sql.ErrNoRows {
			return 0, 0, err
		}

		prev := previewPrefix(r.Body)
		res, err := tx.Exec(`INSERT OR IGNORE INTO messages (id, sender, conversation_id, parent_id, subject, priority, created_at, bytes, preview, body, final, created_text, display_body) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Sender, r.ConversationID, r.ParentID, r.Subject, r.Priority, r.CreatedMilli, len(r.Body), prev, r.Body, b2i(r.Final), r.CreatedText, DisplayBody(r.Body))
		if err != nil {
			return 0, 0, fmt.Errorf("message %s: %w", r.ID, err)
		}
		var seq int64
		if n, _ := res.RowsAffected(); n == 1 {
			seq, _ = res.LastInsertId()
			inserted++
		} else if err := tx.QueryRow(`SELECT seq FROM messages WHERE id = ?`, r.ID).Scan(&seq); err != nil {
			return 0, 0, fmt.Errorf("message %s: %w", r.ID, err)
		}
		if _, err := tx.Exec(`UPDATE messages SET payload_sha256=?,record_json=?,recipients=? WHERE seq=?`, PayloadSHA256(r.Body), r.RecordJSON, strings.Join(r.Recipients, ","), seq); err != nil {
			return 0, 0, err
		}
		for _, d := range r.Deliveries {
			if d.RecordJSON == "" {
				d.RecordJSON = "{}"
			}
			if len(d.RecordJSON) > 64<<10 || !json.Valid([]byte(d.RecordJSON)) {
				return 0, 0, fmt.Errorf("invalid delivery metadata %s/%s", r.ID, d.Seat)
			}
			owed := 0
			// no sender exclusion here, and none in Accept (owes) either: the CLI lists a p0/p1 addressed to its own sender as pending (3 such messages in
			// the 26 Oct copy), the Architect ruled 2026-10-26 that the store keeps that contract, and the import must reproduce what the CLI says
			if r.Priority <= 1 && !r.Final {
				owed = 1
			}
			var have int
			err := tx.QueryRow(`SELECT 1 FROM deliveries WHERE seat = ? AND seq = ?`, d.Seat, seq).Scan(&have)
			switch {
			case err == sql.ErrNoRows:
				if _, err := tx.Exec(`INSERT INTO deliveries (seat, seq, accepted_at, delivered_at, noticed_at, owed, answered_at, priority, created_text, created_milli) VALUES (?,?,?,?,?,?,?,?,?,?)`,
					d.Seat, seq, r.CreatedMilli, nz(d.DeliveredMilli), nz(d.NoticedMilli), owed, nz(d.AnsweredMilli), r.Priority, d.CreatedText, nz(d.CreatedMilli)); err != nil {
					return 0, 0, fmt.Errorf("delivery %s/%s: %w", r.ID, d.Seat, err)
				}
			case err != nil:
				return 0, 0, err
			default:
				res, err := tx.Exec(`UPDATE deliveries SET delivered_at = COALESCE(delivered_at, ?), noticed_at = COALESCE(noticed_at, ?), answered_at = COALESCE(answered_at, ?) WHERE seat = ? AND seq = ?
				  AND ((delivered_at IS NULL AND ? IS NOT NULL) OR (noticed_at IS NULL AND ? IS NOT NULL) OR (answered_at IS NULL AND ? IS NOT NULL))`,
					nz(d.DeliveredMilli), nz(d.NoticedMilli), nz(d.AnsweredMilli), d.Seat, seq, nz(d.DeliveredMilli), nz(d.NoticedMilli), nz(d.AnsweredMilli))
				if err != nil {
					return 0, 0, err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					updated++
				}
				// a row imported before the per-delivery time existed gets it once; a time already set is never changed
				if d.CreatedText != "" {
					if _, err := tx.Exec(`UPDATE deliveries SET created_text = ?, created_milli = ? WHERE seat = ? AND seq = ? AND created_text = ''`,
						d.CreatedText, nz(d.CreatedMilli), d.Seat, seq); err != nil {
						return 0, 0, err
					}
				}
			}
			if _, err := tx.Exec(`UPDATE deliveries SET record_json=? WHERE seat=? AND seq=?`, d.RecordJSON, d.Seat, seq); err != nil {
				return 0, 0, err
			}
			if d.AnswerID != "" {
				if _, err := tx.Exec(`UPDATE deliveries SET answer_id=? WHERE seat=? AND seq=? AND (answer_id IS NULL OR answer_id=?)`, d.AnswerID, d.Seat, seq, d.AnswerID); err != nil {
					return 0, 0, err
				}
			}
		}
	}
	return inserted, updated, tx.Commit()
}

// StateDigest is a deterministic digest of what the database says about every delivery: (message id, seat, noticed?, answered?, owed?),
// in id/seat order, plus the counts. The importer's report carries it and the fidelity checks compare it with the file store's own view.
func (s *Store) StateDigest() (messages, deliveries int64, hexsum string, err error) {
	if err = s.readDB().QueryRow(`SELECT count(*) FROM messages`).Scan(&messages); err != nil {
		return
	}
	if err = s.readDB().QueryRow(`SELECT count(*) FROM deliveries`).Scan(&deliveries); err != nil {
		return
	}
	rows, err := s.readDB().Query(`SELECT m.id, d.seat, d.noticed_at IS NOT NULL, d.answered_at IS NOT NULL, d.owed FROM deliveries d JOIN messages m ON m.seq = d.seq ORDER BY m.id, d.seat`)
	if err != nil {
		return
	}
	defer rows.Close()
	h := newDigest()
	for rows.Next() {
		var id, seat string
		var n, a bool
		var o int
		if err = rows.Scan(&id, &seat, &n, &a, &o); err != nil {
			return
		}
		h.line(id, seat, n, a, o)
	}
	return messages, deliveries, h.sum(), rows.Err()
}

type digestW struct {
	h interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
}

func newDigest() *digestW { return &digestW{h: sha256.New()} }
func (d *digestW) line(id, seat string, n, a bool, o int) {
	fmt.Fprintf(d.h, "%d:%s|%d:%s|%t|%t|%d\n", len(id), id, len(seat), seat, n, a, o)
}
func (d *digestW) sum() string { return fmt.Sprintf("%x", d.h.Sum(nil)) }

// PruneSeat removes the deliveries of seat whose message id is not in keep, and returns how many it removed. It exists for the shadow mirror: a file
// that left the file store (archived, deleted) must leave the mirror, or the mirror keeps answering for mail the CLI no longer lists. Messages left
// without any delivery stay (another seat may hold one, and the mirror's digest is not used for anything that needs them gone).
func (s *Store) PruneSeat(seat string, keep map[string]bool) (int, error) {
	rows, err := s.readDB().Query(`SELECT d.seq, m.id FROM deliveries d JOIN messages m ON m.seq = d.seq WHERE d.seat = ?`, seat)
	if err != nil {
		return 0, err
	}
	var drop []int64
	for rows.Next() {
		var seq int64
		var id string
		if err := rows.Scan(&seq, &id); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[id] {
			drop = append(drop, seq)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(drop) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, seq := range drop {
		if _, err := tx.Exec(`DELETE FROM deliveries WHERE seat = ? AND seq = ?`, seat, seq); err != nil {
			return 0, err
		}
	}
	return len(drop), tx.Commit()
}
