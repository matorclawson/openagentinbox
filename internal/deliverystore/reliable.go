package deliverystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rodriveracom/openagentinbox/delivery"
	"time"
)

// Migration runs in the schema transaction. Every delivery mutation also writes
// a durable event. Even late imports of old message sequences get NEW event IDs.
func migrateReliable(ctx context.Context, c *sql.Conn) error {
	_, err := c.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS delivery_outbox (
  seat TEXT NOT NULL, seq INTEGER NOT NULL, receipt TEXT, last_attempt INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(seat,seq)
 ) WITHOUT ROWID;
 CREATE INDEX IF NOT EXISTS outbox_pending ON delivery_outbox(last_attempt,seat,seq) WHERE receipt IS NULL;
 CREATE TABLE IF NOT EXISTS mailbox_events (
  event INTEGER PRIMARY KEY AUTOINCREMENT, seat TEXT NOT NULL, message_id TEXT NOT NULL,
  kind TEXT NOT NULL, noticed INTEGER NOT NULL, answered INTEGER NOT NULL
 );
 CREATE INDEX IF NOT EXISTS mailbox_events_seat ON mailbox_events(seat,event);
 CREATE TABLE IF NOT EXISTS sync_floor(seat TEXT PRIMARY KEY, floor INTEGER NOT NULL) WITHOUT ROWID;
 CREATE TABLE IF NOT EXISTS message_links(seat TEXT NOT NULL,seq INTEGER NOT NULL,task_ref TEXT NOT NULL,PRIMARY KEY(seat,seq),FOREIGN KEY(seat,seq) REFERENCES deliveries(seat,seq) ON DELETE CASCADE) WITHOUT ROWID;
 CREATE TABLE IF NOT EXISTS reliable_version(version INTEGER PRIMARY KEY);
 INSERT OR IGNORE INTO delivery_outbox(seat,seq,receipt) SELECT seat,seq,NULL FROM deliveries WHERE noticed_at IS NULL AND NOT EXISTS(SELECT 1 FROM reliable_version);
 INSERT INTO mailbox_events(seat,message_id,kind,noticed,answered)
 SELECT d.seat,m.id,'snapshot',d.noticed_at IS NOT NULL,d.answered_at IS NOT NULL
 FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE NOT EXISTS(SELECT 1 FROM reliable_version);
 INSERT OR IGNORE INTO reliable_version VALUES(1);
 CREATE TRIGGER IF NOT EXISTS reliable_insert AFTER INSERT ON deliveries BEGIN
 INSERT INTO delivery_outbox(seat,seq,receipt) SELECT NEW.seat,NEW.seq,NULL WHERE NEW.noticed_at IS NULL;
 INSERT INTO mailbox_events(seat,message_id,kind,noticed,answered)
 VALUES(NEW.seat,(SELECT id FROM messages WHERE seq=NEW.seq),'added',NEW.noticed_at IS NOT NULL,NEW.answered_at IS NOT NULL);
 END;
 CREATE TRIGGER IF NOT EXISTS reliable_update AFTER UPDATE OF noticed_at,answered_at ON deliveries
 WHEN OLD.noticed_at IS NOT NEW.noticed_at OR OLD.answered_at IS NOT NEW.answered_at BEGIN
 INSERT INTO mailbox_events(seat,message_id,kind,noticed,answered)
 VALUES(NEW.seat,(SELECT id FROM messages WHERE seq=NEW.seq),'state',NEW.noticed_at IS NOT NULL,NEW.answered_at IS NOT NULL);
 END;
 CREATE TRIGGER IF NOT EXISTS reliable_receipt AFTER UPDATE OF receipt ON delivery_outbox
 WHEN OLD.receipt IS NULL AND NEW.receipt IS NOT NULL BEGIN
 INSERT INTO mailbox_events(seat,message_id,kind,noticed,answered)
 SELECT d.seat,m.id,'runtime_received',d.noticed_at IS NOT NULL,d.answered_at IS NOT NULL
 FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=NEW.seat AND d.seq=NEW.seq;
 END;
 CREATE TRIGGER IF NOT EXISTS reliable_delete BEFORE DELETE ON deliveries BEGIN
 INSERT INTO mailbox_events(seat,message_id,kind,noticed,answered)
 VALUES(OLD.seat,(SELECT id FROM messages WHERE seq=OLD.seq),'removed',OLD.noticed_at IS NOT NULL,OLD.answered_at IS NOT NULL);
 DELETE FROM delivery_outbox WHERE seat=OLD.seat AND seq=OLD.seq;
 END;`)
	return err
}

// DispatchPending does not hold a database transaction over a network call.
// Concurrent dispatchers can repeat an ID; the receiver must deduplicate it.
func (s *Store) DispatchPending(ctx context.Context, receiver delivery.Receiver, limit int) (int, error) {
	if receiver == nil {
		return 0, errors.New("Runtime receiver unavailable")
	}
	if limit < 1 || limit > 50 {
		return 0, errors.New("limit must be 1..50")
	}
	rows, err := s.readDB().QueryContext(ctx, `SELECT o.seat,o.seq,m.id FROM delivery_outbox o JOIN messages m ON m.seq=o.seq WHERE o.receipt IS NULL ORDER BY o.last_attempt,o.seat,o.seq LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	type pending struct {
		envelope delivery.Envelope
		seq      int64
	}
	batch := []pending{}
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.envelope.Seat, &p.seq, &p.envelope.MessageID); err != nil {
			rows.Close()
			return 0, err
		}
		p.envelope.ID = fmt.Sprintf("mail:%x", sha256.Sum256([]byte(p.envelope.MessageID+"\x00"+p.envelope.Seat)))
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	completed := 0
	var failures []error
	for _, p := range batch {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		if _, e := s.db.ExecContext(ctx, `UPDATE delivery_outbox SET last_attempt=? WHERE seat=? AND seq=?`, time.Now().UnixMilli(), p.envelope.Seat, p.seq); e != nil {
			failures = append(failures, e)
			continue
		}
		r, e := receiver.Receive(ctx, p.envelope)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if r.ID != p.envelope.ID || r.MessageID != p.envelope.MessageID || r.Seat != p.envelope.Seat || r.Assignment == "" || r.Generation < 1 || r.State != "received" {
			failures = append(failures, ErrConflict)
			continue
		}
		raw, _ := json.Marshal(r)
		result, e := s.db.ExecContext(ctx, `UPDATE delivery_outbox SET receipt=? WHERE seat=? AND seq=? AND receipt IS NULL`, string(raw), r.Seat, p.seq)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		n, _ := result.RowsAffected()
		completed += int(n)
	}
	return completed, errors.Join(failures...)
}

type Change struct {
	Event     int64  `json:"event"`
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
	Read      bool   `json:"read"`
	Answered  bool   `json:"answered"`
}
type Changes struct {
	Schema string   `json:"schema"`
	Events []Change `json:"events"`
	Cursor int64    `json:"cursor"`
	Floor  int64    `json:"retained_after"`
	Resync bool     `json:"resync_required"`
	More   bool     `json:"has_more"`
}

func (s *Store) Changes(ctx context.Context, seat string, after int64, limit int) (Changes, error) {
	p := Changes{Schema: "openagentinbox.changes/v1", Events: []Change{}, Cursor: after}
	if after < 0 || limit < 1 || limit > 50 {
		return p, errors.New("invalid cursor or limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT floor FROM sync_floor WHERE seat=?),0)`, seat).Scan(&p.Floor)
	if err != nil {
		return p, err
	}
	if after < p.Floor {
		p.Resync = true
		return p, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT event,message_id,kind,noticed,answered FROM mailbox_events WHERE seat=? AND event>? ORDER BY event LIMIT ?`, seat, after, limit+1)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Change
		if err = rows.Scan(&e.Event, &e.MessageID, &e.Kind, &e.Read, &e.Answered); err != nil {
			return p, err
		}
		if len(p.Events) == limit {
			p.More = true
			break
		}
		p.Events = append(p.Events, e)
		p.Cursor = e.Event
	}
	return p, rows.Err()
}

// PruneChanges removes a bounded contiguous event prefix for ONE seat. It never
// removes a message or outstanding obligation. Clients behind the floor must resync.
func (s *Store) PruneChanges(ctx context.Context, seat string, through int64, limit int) (int, error) {
	if through < 0 || limit < 1 || limit > 1000 {
		return 0, errors.New("invalid retention bound")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var end int64
	e = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(event),0) FROM (SELECT event FROM mailbox_events WHERE seat=? AND event<=? ORDER BY event LIMIT ?)`, seat, through, limit).Scan(&end)
	if e != nil || end == 0 {
		return 0, e
	}
	r, e := tx.ExecContext(ctx, `DELETE FROM mailbox_events WHERE seat=? AND event<=?`, seat, end)
	if e != nil {
		return 0, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO sync_floor VALUES(?,?) ON CONFLICT(seat) DO UPDATE SET floor=MAX(floor,excluded.floor)`, seat, end)
	if e != nil {
		return 0, e
	}
	n, _ := r.RowsAffected()
	return int(n), tx.Commit()
}

// Activity is a current declaration, not conversational mail or proof of success.
type Activity struct {
	Key       string `json:"key"`
	TaskRef   string `json:"task_ref"`
	State     string `json:"state"`
	UpdatedAt int64  `json:"updated_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// ErrActivityRefused wraps every refusal of an activity that is the CALLER's doing (bounds, state, capacity); any other error from PutActivity is the store's and is not for the caller.
var ErrActivityRefused = errors.New("invalid activity")

func (s *Store) PutActivity(ctx context.Context, seat string, a Activity, now time.Time) error {
	if len(a.Key) < 1 || len(a.Key) > 128 || len(a.TaskRef) > 512 || a.ExpiresAt <= now.UnixMilli() || a.ExpiresAt > now.Add(24*time.Hour).UnixMilli() {
		return fmt.Errorf("%w bounds", ErrActivityRefused)
	}
	switch a.State {
	case "working", "blocked", "completed", "cancelled":
	default:
		return fmt.Errorf("%w state", ErrActivityRefused)
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM activity WHERE seat=? AND activity_key!=? AND expires_at>?`, seat, a.Key, now.UnixMilli()).Scan(&count)
	if e != nil {
		return e
	}
	if count >= 100 {
		return fmt.Errorf("%w capacity", ErrActivityRefused)
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM activity WHERE seat=? AND expires_at<=?`, seat, now.UnixMilli())
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO activity VALUES(?,?,?,?,?,?) ON CONFLICT(seat,activity_key) DO UPDATE SET task_ref=excluded.task_ref,state=excluded.state,updated_at=excluded.updated_at,expires_at=excluded.expires_at`, seat, a.Key, a.TaskRef, a.State, now.UnixMilli(), a.ExpiresAt)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Activities(ctx context.Context, seat string, now time.Time) ([]Activity, error) {
	rows, e := s.readDB().QueryContext(ctx, `SELECT activity_key,task_ref,state,updated_at,expires_at FROM activity WHERE seat=? AND expires_at>? ORDER BY updated_at DESC,activity_key LIMIT 100`, seat, now.UnixMilli())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if e = rows.Scan(&a.Key, &a.TaskRef, &a.State, &a.UpdatedAt, &a.ExpiresAt); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Receipt reads only the addressed seat's durable Runtime custody receipt.
func (s *Store) Receipt(ctx context.Context, seat, id string) (*delivery.Receipt, error) {
	var raw sql.NullString
	e := s.readDB().QueryRowContext(ctx, `SELECT o.receipt FROM delivery_outbox o JOIN messages m ON m.seq=o.seq WHERE o.seat=? AND m.id=?`, seat, id).Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, ErrNoSuch
	}
	if e != nil {
		return nil, e
	}
	if !raw.Valid {
		return nil, nil
	}
	var receipt delivery.Receipt
	e = json.Unmarshal([]byte(raw.String), &receipt)
	return &receipt, e
}
