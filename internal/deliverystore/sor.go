package deliverystore

// sor.go: the store OF RECORD (operator, 19 Nov 2026: "i want that we completely phase out the bash script with files today").
// Until now this package was the shadow and the mirror; with openagentinboxd -store sqlite it is the only store. What the file store kept that
// the shadow schema did not is added here, idempotently, by MigrateSOR:
//
//	seats            the registry (instances/<seat>.json): generation, seat_type, status, and the record as it was, for whoami and seats
//	meta             one row per key; 'fence' holds the store-moved text (the file store's FENCE file): every write refuses with exit 9
//	deliveries.answer_id      the id of the message that answered this delivery (the file store's log_answer_message_id)
//	messages.payload_sha256   the sha256 of the stored body exactly as the relay hashed it (sent/v1 payload_sha256)
//
// Nothing here changes how the shadow and the mirror use the store: every column is nullable and every table is new.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const sorSchema = `
CREATE TABLE IF NOT EXISTS seats (
  seat TEXT PRIMARY KEY,
  generation INTEGER NOT NULL DEFAULT 1,
  seat_type TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  record BLOB NOT NULL DEFAULT '{}'
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS away_state(seat TEXT PRIMARY KEY REFERENCES seats(seat),until_ms INTEGER NOT NULL,note TEXT NOT NULL,forward TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS auto_once(sender TEXT NOT NULL,seat TEXT NOT NULL,kind TEXT NOT NULL,day TEXT NOT NULL,PRIMARY KEY(sender,seat,kind,day)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS watchdog_ops(id TEXT PRIMARY KEY, request TEXT NOT NULL, result TEXT NOT NULL, previous TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS wake_map(seat TEXT PRIMARY KEY REFERENCES seats(seat),host TEXT NOT NULL,folder TEXT NOT NULL,generation INTEGER NOT NULL) WITHOUT ROWID;
CREATE UNIQUE INDEX IF NOT EXISTS wake_map_host_folder ON wake_map(host,folder);
CREATE TABLE IF NOT EXISTS seat_presence(seat TEXT PRIMARY KEY REFERENCES seats(seat) ON DELETE CASCADE, at INTEGER NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS notice_usage(sender TEXT NOT NULL,at INTEGER NOT NULL,used INTEGER NOT NULL,PRIMARY KEY(sender,at)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS p0_budget(sender TEXT NOT NULL,recipient TEXT NOT NULL,day TEXT NOT NULL,used INTEGER NOT NULL,PRIMARY KEY(sender,recipient,day)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS seat_state(seat TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(seat,key)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS told(seat TEXT NOT NULL,seq INTEGER NOT NULL,at INTEGER NOT NULL,PRIMARY KEY(seat,seq), FOREIGN KEY(seat,seq) REFERENCES deliveries(seat,seq) ON DELETE CASCADE) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS deliveries_seq ON deliveries(seq,seat);
CREATE INDEX IF NOT EXISTS messages_sender_created ON messages (sender, created_at);
`

// MigrateSOR adds the store-of-record tables and columns. It is idempotent and safe on a database another version made.
func (s *Store) MigrateSOR() error {
	return s.nativeMigration(func(c *sql.Conn) error {
		ctx := context.Background()
		if e := migratePriorityCounts(ctx, c); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, sorSchema); e != nil {
			return e
		}
		for _, col := range []struct{ table, name, decl string }{{"deliveries", "created_text", "TEXT NOT NULL DEFAULT ''"}, {"deliveries", "created_milli", "INTEGER"}, {"deliveries", "answer_id", "TEXT"}, {"messages", "payload_sha256", "TEXT"}, {"messages", "recipients", "TEXT"}, {"messages", "record_json", "TEXT NOT NULL DEFAULT '{}'"}, {"deliveries", "record_json", "TEXT NOT NULL DEFAULT '{}'"}} {
			yes, e := connHasColumn(c, col.table, col.name)
			if e != nil {
				return e
			}
			if !yes {
				if _, e = c.ExecContext(ctx, `ALTER TABLE `+col.table+` ADD COLUMN `+col.name+` `+col.decl); e != nil {
					return e
				}
			}
		}
		if err := installHistoryKeys(ctx, c); err != nil {
			return err
		}
		// The fence protects all writes, including older helper APIs. The meta fence itself remains writable by the offline operator tool.
		for _, table := range []string{"messages", "deliveries", "seats", "seat_state", "told", "operations", "wake_requests", "cursors"} {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				q := `CREATE TRIGGER IF NOT EXISTS fence_` + table + `_` + op + ` BEFORE ` + op + ` ON ` + table + ` WHEN EXISTS(SELECT 1 FROM meta WHERE key='fence') BEGIN SELECT RAISE(ABORT,'store fenced'); END;`
				if _, e := c.ExecContext(ctx, q); e != nil {
					return e
				}
			}
		}
		return nil
	})
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	q, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer q.Close()
	for q.Next() {
		var n string
		if err := q.Scan(&n); err != nil {
			return false, err
		}
		if n == col {
			return true, nil
		}
	}
	return false, q.Err()
}

// SeatRec is one registered seat.
type SeatRec struct {
	Seat, SeatType, Status string
	Generation             int
	Record                 []byte
}

// PutSeat writes one seat (the importer and the registry); a later write replaces it.
func (s *Store) PutSeat(r SeatRec) error {
	if r.Generation < 1 {
		r.Generation = 1
	}
	if len(r.Record) == 0 {
		r.Record = []byte("{}")
	}
	_, err := s.db.Exec(`INSERT INTO seats (seat, generation, seat_type, status, record) VALUES (?,?,?,?,?)
	  ON CONFLICT(seat) DO UPDATE SET generation = excluded.generation, seat_type = excluded.seat_type, status = excluded.status, record = excluded.record`,
		r.Seat, r.Generation, r.SeatType, r.Status, r.Record)
	return err
}

// Seat reads one seat; ok is false when it is not registered.
func (s *Store) Seat(seat string) (r SeatRec, ok bool, err error) {
	err = s.readDB().QueryRow(`SELECT seat, generation, seat_type, status, record FROM seats WHERE seat = ?`, seat).Scan(&r.Seat, &r.Generation, &r.SeatType, &r.Status, &r.Record)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}

// Seats lists every seat, ordered by name (the file store's instances/*.json glob order).
func (s *Store) Seats() ([]SeatRec, error) {
	q, err := s.readDB().Query(`SELECT seat, generation, seat_type, status, record FROM seats ORDER BY seat`)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []SeatRec
	for q.Next() {
		var r SeatRec
		if err := q.Scan(&r.Seat, &r.Generation, &r.SeatType, &r.Status, &r.Record); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, q.Err()
}

// Fence returns the store-moved text, and whether the store is fenced.
func (s *Store) Fence() (string, bool, error) {
	var v string
	err := s.readDB().QueryRow(`SELECT value FROM meta WHERE key = 'fence'`).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetFence fences the store (the move step); every write then refuses.
func (s *Store) SetFence(text string) error {
	_, err := s.db.Exec(`INSERT INTO meta (key, value) VALUES ('fence', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, text)
	return err
}

// PayloadSHA256 is the hash sent/v1 reports: the sha256 of the stored body, hex.
func PayloadSHA256(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// ErrNotRegistered: a recipient is not a registered seat.
var ErrNotRegistered = errors.New("no such seat")

// Stored is the outcome of a native send.
type Stored struct {
	ID       string
	Repeated bool // an identical retry of a message already stored: nothing new was written
}

// AcceptRecord stores a message as the store of record. The message id IS the idempotency key (the CLI's msg-<seat>-<KEY>): the same id with
// the same sender, recipients, parent, final flag and body is an identical retry (Repeated, nothing written); the same id with anything else
// is ErrConflict (exit 8). This also covers ids imported from the file store, which have no operations row. Every recipient must be a
// registered seat; a fenced store refuses everything (ErrFenced).
func (s *Store) AcceptRecord(ctx context.Context, m Message, now time.Time) (Stored, error) {
	return s.AcceptRecordBudget(ctx, m, now, 0)
}
func (s *Store) AcceptRecordBudget(ctx context.Context, m Message, now time.Time, budget int) (Stored, error) {
	return s.acceptWithLimits(ctx, m, now, budget, false)
}

var ErrNoticeRate = errors.New("notice limit is 120 recipient deliveries per hour")

func (s *Store) AcceptNotice(ctx context.Context, m Message, now time.Time) (Stored, error) {
	if m.Priority != 2 || m.Final || m.ParentID != "" {
		return Stored{}, errors.New("a notice must be a non-final p2")
	}
	return s.acceptWithLimits(ctx, m, now, 0, true)
}
func (s *Store) acceptWithLimits(ctx context.Context, m Message, now time.Time, budget int, notice bool) (Stored, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Stored{}, err
	}
	defer tx.Rollback()
	result, err := s.acceptRecordTx(ctx, tx, m, now, budget)
	if err != nil {
		return Stored{}, err
	}
	if notice && !result.Repeated {
		if _, err = tx.ExecContext(ctx, "DELETE FROM notice_usage WHERE sender=? AND at<=?", m.Sender, now.Add(-time.Hour).UnixMilli()); err != nil {
			return Stored{}, err
		}
		var used int
		if err = tx.QueryRowContext(ctx, "SELECT coalesce(sum(used),0) FROM notice_usage WHERE sender=?", m.Sender).Scan(&used); err != nil {
			return Stored{}, err
		}
		if used+len(m.Recipients) > 120 {
			return Stored{}, ErrNoticeRate
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO notice_usage VALUES(?,?,?) ON CONFLICT(sender,at) DO UPDATE SET used=used+excluded.used", m.Sender, now.UnixMilli(), len(m.Recipients)); err != nil {
			return Stored{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Stored{}, err
	}
	if !result.Repeated {
		s.signal(m.Recipients)
	}
	return result, nil
}
func checkFenceTx(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta WHERE key='fence'`).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrFenced
	}
	return nil
}
func (s *Store) acceptRecordTx(ctx context.Context, tx *sql.Tx, m Message, now time.Time, budget int) (Stored, error) {
	if err := checkFenceTx(ctx, tx); err != nil {
		return Stored{}, err
	}
	if m.ID == "" || m.Sender == "" || len(m.ID) > 160 || len(m.Sender) > 160 || len(m.ConversationID) > 200 || len(m.ParentID) > 200 || len(m.Subject) > MaxSubjectBytes || len(m.Body) == 0 || len(m.Body) > MaxMessageBytes || m.Priority < 0 || m.Priority > 2 || len(m.Recipients) < 1 || len(m.Recipients) > MaxRecipients {
		return Stored{}, errors.New("invalid message bounds")
	}
	seen := map[string]bool{}
	for _, r := range m.Recipients {
		if r == "" || seen[r] {
			return Stored{}, errors.New("invalid or duplicate recipient")
		}
		seen[r] = true
	}
	// Replay is checked BEFORE the daily budget: an identical retry consumes no budget.
	var seq int64
	var sender, parent, subject, sha, recipients string
	var final, priority int
	err := tx.QueryRowContext(ctx, `SELECT seq,sender,coalesce(parent_id,''),subject,priority,final,coalesce(payload_sha256,''),coalesce(recipients,'') FROM messages WHERE id=?`, m.ID).Scan(&seq, &sender, &parent, &subject, &priority, &final, &sha, &recipients)
	if err == nil {
		if sha == "" {
			var b []byte
			if err = tx.QueryRowContext(ctx, `SELECT body FROM messages WHERE seq=?`, seq).Scan(&b); err != nil {
				return Stored{}, err
			}
			sha = PayloadSHA256(b)
		}
		if recipients == "" {
			q, e := tx.QueryContext(ctx, `SELECT seat FROM deliveries WHERE seq=? ORDER BY seat`, seq)
			if e != nil {
				return Stored{}, e
			}
			var rs []string
			for q.Next() {
				var r string
				if e = q.Scan(&r); e != nil {
					q.Close()
					return Stored{}, e
				}
				rs = append(rs, r)
			}
			e = q.Err()
			q.Close()
			if e != nil {
				return Stored{}, e
			}
			recipients = strings.Join(rs, ",")
		}
		have := strings.Split(recipients, ",")
		want := append([]string(nil), m.Recipients...)
		sortStrings(have)
		sortStrings(want)
		if sender != m.Sender || parent != m.ParentID || subject != m.Subject || priority != m.Priority || final != b2i(m.Final) || sha != PayloadSHA256(m.Body) || strings.Join(have, ",") != strings.Join(want, ",") {
			return Stored{}, ErrConflict
		}
		return Stored{ID: m.ID, Repeated: true}, nil
	}
	if err != sql.ErrNoRows {
		return Stored{}, err
	}
	for _, seat := range append([]string{m.Sender}, m.Recipients...) {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM seats WHERE seat=? AND status IN ('','active')`, seat).Scan(&n); err != nil {
			return Stored{}, err
		}
		if n != 1 {
			return Stored{}, ErrNotRegistered
		}
	}
	if budget > 0 && m.Priority == 0 {
		day := now.UTC().Format("2006-01-02")
		for _, seat := range m.Recipients {
			var n int
			if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT used FROM p0_budget WHERE sender=? AND recipient=? AND day=?),0)`, m.Sender, seat, day).Scan(&n); err != nil {
				return Stored{}, err
			}
			if n >= budget {
				return Stored{}, ErrBudget
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO p0_budget VALUES(?,?,?,1) ON CONFLICT(sender,recipient,day) DO UPDATE SET used=used+1`, m.Sender, seat, day); err != nil {
				return Stored{}, err
			}
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO messages(id,sender,conversation_id,parent_id,subject,priority,created_at,bytes,preview,body,final,created_text,display_body,payload_sha256,recipients) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Sender, m.ConversationID, m.ParentID, m.Subject, m.Priority, now.UnixMilli(), len(m.Body), previewPrefix(m.Body), m.Body, b2i(m.Final), now.UTC().Format(time.RFC3339Nano), DisplayBody(m.Body), PayloadSHA256(m.Body), strings.Join(m.Recipients, ","))
	if err != nil {
		return Stored{}, err
	}
	seq, err = res.LastInsertId()
	if err != nil {
		return Stored{}, err
	}
	delivery := tx.StmtContext(ctx, s.insertDelivery)
	for _, seat := range m.Recipients {
		if _, err = delivery.ExecContext(ctx, seat, seq, now.UnixMilli(), now.UnixMilli(), owes(m, seat), m.Priority, now.UnixMilli()); err != nil {
			return Stored{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE deliveries SET record_json=json_object('log_delivered_at',?) WHERE seq=?`, now.UTC().Format("2006-01-02T15:04:05.000000000Z"), seq); err != nil {
		return Stored{}, err
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO operations(op_key,digest,message_id,created_at) VALUES(?,?,?,?)`, m.ID, digest(m), m.ID, now.UnixMilli()); err != nil {
		return Stored{}, err
	}
	return Stored{ID: m.ID}, nil
}

var ErrBudget = errors.New("daily p0 budget exhausted; send as p1")

// ReplyRecord commits the answer, its recipients, outbox and original state together.
func (s *Store) ReplyRecord(ctx context.Context, seat, original string, m Message, now time.Time, budget int) (Stored, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Stored{}, e
	}
	defer tx.Rollback()
	if e = checkFenceTx(ctx, tx); e != nil {
		return Stored{}, e
	}
	var sender, answer string
	e = tx.QueryRowContext(ctx, `SELECT m.sender,coalesce(d.answer_id,'') FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND m.id=?`, seat, original).Scan(&sender, &answer)
	if e == sql.ErrNoRows {
		return Stored{}, ErrNoSuch
	}
	if e != nil {
		return Stored{}, e
	}
	if m.Sender != seat || m.ParentID != original || len(m.Recipients) != 1 || m.Recipients[0] != sender {
		return Stored{}, ErrConflict
	}
	if answer != "" && answer != m.ID {
		return Stored{}, ErrConflict
	}
	result, e := s.acceptRecordTx(ctx, tx, m, now, budget)
	if e != nil {
		return Stored{}, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE deliveries SET answered_at=coalesce(answered_at,?),noticed_at=coalesce(noticed_at,?),answer_id=? WHERE seat=? AND seq=(SELECT seq FROM messages WHERE id=?)`, now.UnixMilli(), now.UnixMilli(), m.ID, seat, original)
	if e != nil {
		return Stored{}, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE deliveries SET record_json=json_set(record_json,
 '$.log_state','answered','$.log_acked_at',coalesce(json_extract(record_json,'$.log_acked_at'),?),
 '$.log_ack_generation',coalesce(json_extract(record_json,'$.log_ack_generation'),(SELECT generation FROM seats WHERE seat=?)),
 '$.log_answered_at',coalesce(json_extract(record_json,'$.log_answered_at'),?),
 '$.log_answer_generation',coalesce(json_extract(record_json,'$.log_answer_generation'),(SELECT generation FROM seats WHERE seat=?)),
 '$.log_answer_message_id',?,'$.log_answer_operation_id',?,'$.log_answer_sha256',?)
 WHERE seat=? AND seq=(SELECT seq FROM messages WHERE id=?)`, now.UTC().Format("2006-01-02T15:04:05.000000000Z"), seat, now.UTC().Format("2006-01-02T15:04:05.000000000Z"), seat, m.ID, "op-"+original+"-"+seat, PayloadSHA256(m.Body), seat, original)
	if e != nil {
		return Stored{}, e
	}
	if err := tx.Commit(); err != nil {
		return Stored{}, err
	}
	if !result.Repeated {
		s.signal(m.Recipients)
	}
	return result, nil
}

// ErrFenced: the store has moved; nothing is written.
var ErrFenced = errors.New("store fenced")

// sameAsStored compares m with a stored message of the same id: sender, parent, final, body and the exact recipient set.
func (s *Store) sameAsStored(ctx context.Context, m Message) (same, found bool, err error) {
	var seq int64
	var sender, parent string
	var final int
	var body []byte
	err = s.readDB().QueryRowContext(ctx, `SELECT seq, sender, COALESCE(parent_id,''), final, body FROM messages WHERE id = ?`, m.ID).Scan(&seq, &sender, &parent, &final, &body)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if sender != m.Sender || parent != m.ParentID || (final == 1) != m.Final || string(body) != string(m.Body) {
		return false, true, nil
	}
	q, err := s.readDB().QueryContext(ctx, `SELECT seat FROM deliveries WHERE seq = ? ORDER BY seat`, seq)
	if err != nil {
		return false, true, err
	}
	defer q.Close()
	var got []string
	for q.Next() {
		var r string
		if err := q.Scan(&r); err != nil {
			return false, true, err
		}
		got = append(got, r)
	}
	if err := q.Err(); err != nil {
		return false, true, err
	}
	want := append([]string(nil), m.Recipients...)
	sortStrings(want)
	return strings.Join(got, "\x00") == strings.Join(want, "\x00"), true, nil
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// Delivered is one seat's copy of one message, with its state, for read/show and sent.
type Delivered struct {
	RecordJSON                                                                       string
	ID, Sender, ParentID, Subject, CreatedText, AnswerID, PayloadSHA, ConversationID string
	Priority                                                                         int
	Final                                                                            bool
	Body                                                                             []byte
	DeliveredMilli, NoticedMilli, AnsweredMilli                                      int64
	Recipient                                                                        string
}

// LogState is the file store's vocabulary: delivered (unread), acknowledged (read), answered.
func (d Delivered) LogState() string {
	switch {
	case d.AnsweredMilli != 0:
		return "answered"
	case d.NoticedMilli != 0:
		return "acknowledged"
	}
	return "delivered"
}

const deliveredCols = `coalesce(m.conversation_id,''), m.id, m.sender, COALESCE(m.parent_id,''), m.subject, coalesce(nullif(d.created_text,''),m.created_text), COALESCE(d.answer_id,''),
  COALESCE(m.payload_sha256,''), m.priority, m.final, m.body, COALESCE(d.delivered_at,0), COALESCE(d.noticed_at,0), COALESCE(d.answered_at,0), d.seat, d.record_json`

func scanDelivered(sc interface{ Scan(...any) error }) (Delivered, error) {
	var d Delivered
	var final int
	err := sc.Scan(&d.ConversationID, &d.ID, &d.Sender, &d.ParentID, &d.Subject, &d.CreatedText, &d.AnswerID, &d.PayloadSHA, &d.Priority, &final, &d.Body,
		&d.DeliveredMilli, &d.NoticedMilli, &d.AnsweredMilli, &d.Recipient, &d.RecordJSON)
	d.Final = final == 1
	return d, err
}

// ForSeat reads one message as seat's copy (read/show). ErrNoSuch when seat has no copy.
func (s *Store) ForSeat(seat, id string) (Delivered, error) {
	d, err := scanDelivered(s.readDB().QueryRow(`SELECT `+deliveredCols+` FROM deliveries d JOIN messages m ON m.seq = d.seq WHERE m.id = ? AND d.seat = ?`, id, seat))
	if err == sql.ErrNoRows {
		return d, ErrNoSuch
	}
	return d, err
}

// SentBy reads a message seat SENT and every recipient's copy (sent/v1). found is false when the id is unknown or another seat sent it:
// the caller answers both the same way, so the verb cannot tell whether another seat's message exists.
func (s *Store) SentBy(seat, id string) (copies []Delivered, found bool, err error) {
	var sender string
	err = s.readDB().QueryRow(`SELECT sender FROM messages WHERE id = ?`, id).Scan(&sender)
	if err == sql.ErrNoRows || (err == nil && sender != seat) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	q, err := s.readDB().Query(`SELECT `+strings.Replace(deliveredCols, "m.body", "CAST('' AS BLOB)", 1)+` FROM deliveries d JOIN messages m ON m.seq = d.seq WHERE m.id = ?`, id)
	if err != nil {
		return nil, true, err
	}
	defer q.Close()
	bySeat := map[string]Delivered{}
	for q.Next() {
		d, err := scanDelivered(q)
		if err != nil {
			return nil, true, err
		}
		bySeat[d.Recipient] = d
	}
	if err := q.Err(); err != nil {
		return nil, true, err
	}
	q.Close()
	// the post's own recipient order (messages.recipients); a recipient with no copy is reported with no state, as the file store does
	var list string
	if err := s.readDB().QueryRow(`SELECT COALESCE(recipients,'') FROM messages WHERE id = ?`, id).Scan(&list); err != nil {
		return nil, true, err
	}
	seen := map[string]bool{}
	for _, r := range strings.Split(list, ",") {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		d, ok := bySeat[r]
		if !ok {
			d = Delivered{ID: id, Recipient: r}
		}
		copies = append(copies, d)
	}
	var rest []string
	for r := range bySeat {
		if !seen[r] {
			rest = append(rest, r)
		}
	}
	sortStrings(rest)
	for _, r := range rest {
		copies = append(copies, bySeat[r])
	}
	return copies, true, nil
}

// AckSeat marks seat's copy read (acknowledged); it reports whether it was unread before.
func (s *Store) AckSeat(seat, id string, now time.Time) (bool, error) {
	ctx := context.Background()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if e = checkFenceTx(ctx, tx); e != nil {
		return false, e
	}
	var noticed sql.NullInt64
	e = tx.QueryRow(`SELECT noticed_at FROM deliveries WHERE seat=? AND seq=(SELECT seq FROM messages WHERE id=?)`, seat, id).Scan(&noticed)
	if e == sql.ErrNoRows {
		return false, ErrNoSuch
	}
	if e != nil {
		return false, e
	}
	if !noticed.Valid {
		_, e = tx.Exec(`UPDATE deliveries SET noticed_at=?,delivered_at=coalesce(delivered_at,?),record_json=json_set(record_json,'$.log_state','acknowledged','$.log_acked_at',?,'$.log_ack_generation',(SELECT generation FROM seats WHERE seat=?)) WHERE seat=? AND seq=(SELECT seq FROM messages WHERE id=?)`, now.UnixMilli(), now.UnixMilli(), now.UTC().Format("2006-01-02T15:04:05.000000000Z"), seat, seat, id)
		if e != nil {
			return false, e
		}
	}
	return !noticed.Valid, tx.Commit()
}

// AckBacklog applies the filter in SQL in one transaction, independent of inbox page size.
// A dry-run lists at most 200 IDs and returns the complete matched count.
func (s *Store) AckBacklog(ctx context.Context, seat, before, from string, priority int, answers, dry bool, now time.Time) (int, []string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, nil, e
	}
	defer tx.Rollback()
	if !dry {
		if e = checkFenceTx(ctx, tx); e != nil {
			return 0, nil, e
		}
	}
	cond := `seat=? AND noticed_at IS NULL AND seq IN (SELECT m.seq FROM messages m WHERE m.created_text<? AND (?='' OR m.sender=?) AND ((?=-1 AND m.priority>0) OR m.priority=?) AND (?=0 OR (substr(m.id,1,4)='ans_' AND EXISTS(SELECT 1 FROM messages orig WHERE orig.id=m.parent_id AND orig.sender=?))))`
	args := []any{seat, before, from, from, priority, priority, b2i(answers), seat}
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM deliveries WHERE `+cond, args...).Scan(&n); e != nil {
		return 0, nil, e
	}
	ids := []string{}
	if dry {
		q, err := tx.QueryContext(ctx, `SELECT m.id FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seq IN(SELECT seq FROM deliveries WHERE `+cond+`) AND d.seat=? ORDER BY d.seq LIMIT 200`, append(args, seat)...)
		if err != nil {
			return 0, nil, err
		}
		for q.Next() {
			var id string
			if err = q.Scan(&id); err != nil {
				q.Close()
				return 0, nil, err
			}
			ids = append(ids, id)
		}
		e = q.Err()
		q.Close()
		if e != nil {
			return 0, nil, e
		}
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE deliveries SET noticed_at=?,delivered_at=coalesce(delivered_at,?),record_json=json_set(record_json,'$.log_state','acknowledged','$.log_acked_at',?,'$.log_ack_generation',(SELECT generation FROM seats WHERE seat=?)) WHERE `+cond, append([]any{now.UnixMilli(), now.UnixMilli(), now.UTC().Format("2006-01-02T15:04:05.000000000Z"), seat}, args...)...)
		if e != nil {
			return 0, nil, e
		}
	}
	return n, ids, tx.Commit()
}

// AnsweredBy records that seat answered origID with answerID (reply): ANSWERED and read, with the answer's id.
func (s *Store) AnsweredBy(seat, origID, answerID string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE deliveries SET answered_at = COALESCE(answered_at, ?), noticed_at = COALESCE(noticed_at, ?), delivered_at = COALESCE(delivered_at, ?),
	  answer_id = COALESCE(answer_id, ?) WHERE seat = ? AND seq = (SELECT seq FROM messages WHERE id = ?)`,
		now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), answerID, seat, origID)
	return err
}

// SentToday counts the messages sender sent to recipient since dayStart, of priority prio (-1: any): the 'N from you today' line and the p0 budget.
func (s *Store) SentToday(sender, recipient string, prio int, dayStart time.Time) (n int, err error) {
	err = s.readDB().QueryRow(`SELECT count(*) FROM messages m JOIN deliveries d ON d.seq = m.seq WHERE m.sender = ? AND d.seat = ? AND m.created_at >= ?
	  AND (? < 0 OR m.priority = ?)`, sender, recipient, dayStart.UnixMilli(), prio, prio).Scan(&n)
	return n, err
}

// ReplyAlready returns the id of seat's earlier answer to origID, if it answered already (reply is once per message).
func (s *Store) ReplyAlready(seat, origID string) (string, error) {
	var a string
	err := s.readDB().QueryRow(`SELECT COALESCE(d.answer_id,'') FROM deliveries d JOIN messages m ON m.seq = d.seq WHERE m.id = ? AND d.seat = ?`, origID, seat).Scan(&a)
	if err == sql.ErrNoRows {
		return "", ErrNoSuch
	}
	return a, err
}

// ClearFence is an offline administrative compare-and-delete, never an HTTP operation.
func (s *Store) ClearFence(expected string) error {
	r, e := s.db.Exec(`DELETE FROM meta WHERE key='fence' AND value=?`, expected)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// ExactStamp preserves imported sub-millisecond precision and uses normalized state
// to reject stale metadata if another supported writer has advanced the row.
func ExactStamp(raw, key string, ms int64) *string {
	if ms == 0 {
		return nil
	}
	r, e := record(raw)
	if e == nil {
		var value string
		if json.Unmarshal(r[key], &value) == nil {
			t, e := time.Parse(time.RFC3339Nano, value)
			if e == nil && t.UnixMilli() == ms {
				return &value
			}
		}
	}
	v := time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000000000Z")
	return &v
}

// AcceptGroup stores one post per recipient, with a single commit and shared
// payload. Directory or quota changes can refuse the whole group before any
// recipient observes a partial result.
func (s *Store) AcceptGroup(ctx context.Context, base Message, recipients []string, aliases map[string]string, now time.Time, budget int) error {
	if len(recipients) < 1 || len(recipients) > MaxRecipients {
		return errors.New("group size exceeds store bounds")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var changed []string
	for _, r := range recipients {
		m := base
		m.ID = base.ID + "-" + r
		m.ConversationID = "conv_" + m.ID
		m.Recipients = []string{r}
		if a := aliases[r]; a != "" && a != r {
			m.Body = append(append([]byte{}, base.Body...), []byte("\nSent-To-Alias: "+a+" -> "+r)...)
		}
		stored, e := s.acceptRecordTx(ctx, tx, m, now, budget)
		if e != nil {
			return e
		}
		if !stored.Repeated {
			changed = append(changed, r)
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.signal(changed)
	return nil
}
