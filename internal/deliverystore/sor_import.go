package deliverystore

// sor_import.go: the importer's half of the store of record (sor.go). It writes the per-post and per-delivery facts that ImportBatch does not
// carry (the relay's payload hash, the post's recipient order, the answer id of each answered delivery) in ONE transaction with prepared
// statements, so an import of a large store costs one pass and one commit. It needs MigrateSOR first.

import "fmt"

// ImportExtra is one post's store-of-record facts: payload_sha256 and the recipients in the post's own order.
type ImportExtra struct {
	ID, PayloadSHA string
	Recipients     []string
}

// ImportAnswer is one delivery's answer id (the file store's log_answer_message_id).
type ImportAnswer struct {
	Seat, ID, AnswerID string
}

// ApplyImportExtras writes extras and answers. A re-import of a newer copy overwrites them (the file store is the source until the cutover).
// It returns how many message rows and delivery rows it changed; a fact for a message or delivery the database does not hold changes nothing.
func (s *Store) ApplyImportExtras(extras []ImportExtra, answers []ImportAnswer) (msgs, dels int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	pm, err := tx.Prepare(`UPDATE messages SET payload_sha256 = ?, recipients = ? WHERE id = ?`)
	if err != nil {
		return 0, 0, err
	}
	defer pm.Close()
	for _, e := range extras {
		rs := ""
		for i, r := range e.Recipients {
			if i > 0 {
				rs += ","
			}
			rs += r
		}
		res, err := pm.Exec(e.PayloadSHA, rs, e.ID)
		if err != nil {
			return 0, 0, fmt.Errorf("message %s: %w", e.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			msgs++
		}
	}
	pd, err := tx.Prepare(`UPDATE deliveries SET answer_id = ? WHERE seat = ? AND seq = (SELECT seq FROM messages WHERE id = ?)`)
	if err != nil {
		return 0, 0, err
	}
	defer pd.Close()
	for _, a := range answers {
		res, err := pd.Exec(a.AnswerID, a.Seat, a.ID)
		if err != nil {
			return 0, 0, fmt.Errorf("delivery %s of %s: %w", a.ID, a.Seat, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			dels++
		}
	}
	return msgs, dels, tx.Commit()
}
