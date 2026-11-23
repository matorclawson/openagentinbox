package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

const trackingSchema = `
CREATE TABLE IF NOT EXISTS mail_receipts(id TEXT NOT NULL,seat TEXT NOT NULL,opened INTEGER,replied INTEGER,state TEXT NOT NULL DEFAULT 'fyi',due INTEGER,relayed INTEGER NOT NULL DEFAULT 0,escalated INTEGER,PRIMARY KEY(id,seat)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS mail_due ON mail_receipts(due) WHERE due IS NOT NULL AND escalated IS NOT due;
CREATE INDEX IF NOT EXISTS overdue_by_time ON mail_receipts(due) WHERE due IS NOT NULL AND escalated=due;
CREATE TABLE IF NOT EXISTS mail_alerts(id TEXT NOT NULL,seat TEXT NOT NULL,due INTEGER NOT NULL,target TEXT NOT NULL,notice TEXT,PRIMARY KEY(id,seat,due,target)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS unsent_alerts ON mail_alerts(target,seat,due) WHERE notice IS NULL;
CREATE INDEX IF NOT EXISTS target_alerts ON mail_alerts(target,due DESC);
CREATE TABLE IF NOT EXISTS host_probes(host TEXT PRIMARY KEY,marker TEXT NOT NULL,issued INTEGER NOT NULL,started INTEGER NOT NULL,confirmed INTEGER NOT NULL DEFAULT 0,incident INTEGER NOT NULL DEFAULT 0);
CREATE TRIGGER IF NOT EXISTS forget_host_probe AFTER DELETE ON paired_cli BEGIN DELETE FROM host_probes WHERE host=OLD.id; END;
CREATE TRIGGER IF NOT EXISTS forget_mail_tracking AFTER DELETE ON messages BEGIN
 DELETE FROM mail_receipts WHERE id=OLD.id; DELETE FROM mail_alerts WHERE id=OLD.id;
END;
CREATE TABLE IF NOT EXISTS listener_checks(seat TEXT PRIMARY KEY,at INTEGER NOT NULL,mode TEXT NOT NULL);
`

func (s *server) requestAction(_ http.ResponseWriter, r *http.Request) (any, error) {
	seat, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	var q struct {
		ID, Action string
		Until      int64
	}
	if err = input(r, &q); err != nil {
		return nil, err
	}
	var next string
	switch q.Action {
	case "accept":
		next = "accepted"
	case "defer":
		next = "deferred"
	case "decline":
		next = "declined"
	case "resolve":
		next = "resolved"
	default:
		return nil, failure{400, "Unknown action"}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var previous string
	var due int64
	if err = tx.QueryRow("SELECT state,coalesce(due,0) FROM mail_receipts WHERE id=? AND seat=?", q.ID, seat).Scan(&previous, &due); err != nil {
		return nil, err
	}
	if previous == next && (due == q.Until || q.Until == 0 && next == "accepted") {
		return object{"state": next, "due": due}, nil
	}
	if previous == "fyi" || previous == "resolved" || previous == "declined" {
		return nil, failure{409, "Request is closed or informational"}
	}
	if next == "accepted" && q.Until == 0 {
		q.Until = time.Now().Add(time.Hour).UnixMilli()
	}
	if next == "accepted" || next == "deferred" {
		if q.Until <= now() {
			return nil, failure{400, "Supply a future deadline in Unix milliseconds"}
		}
	} else if q.Until != 0 {
		return nil, failure{400, "A closed request has no deadline"}
	}
	if _, err = tx.Exec("UPDATE mail_receipts SET state=?,due=nullif(?,0) WHERE id=? AND seat=?", next, q.Until, q.ID, seat); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.live.signal()
	return object{"state": next, "due": q.Until}, nil
}

func (s *server) alerts(_ http.ResponseWriter, r *http.Request) (any, error) {
	target, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Reader().QueryContext(r.Context(), `SELECT a.id,a.seat,a.due FROM mail_alerts a JOIN mail_receipts r USING(id,seat) WHERE target=? AND r.due=a.due ORDER BY a.due DESC LIMIT 100`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []object{}
	for rows.Next() {
		var id, seat string
		var due int64
		if err = rows.Scan(&id, &seat, &due); err != nil {
			return nil, err
		}
		out = append(out, object{"id": id, "recipient": seat, "due": due})
	}
	return out, rows.Err()
}

func (s *server) recordOverdue(ctx context.Context, at int64) error {
	// Most ticks have no deadline close enough to process. Only reserve the
	// writer when the deadline index contains a candidate. Recheck inside the
	// transaction because a recipient may have replied since this snapshot.
	var pending bool
	err := s.store.Reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail_receipts
 WHERE due IS NOT NULL AND escalated IS NOT due AND due<=?)`, at+120000).Scan(&pending)
	if err != nil || !pending {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.seat,r.due,m.sender FROM mail_receipts r JOIN messages m ON m.id=r.id
 WHERE r.due IS NOT NULL AND r.escalated IS NOT r.due AND r.due<=?1+120000
 AND (r.due<=?1 OR (m.priority=0 AND r.state='pending' AND r.relayed=0 AND (
 EXISTS(SELECT 1 FROM listener_checks l WHERE l.seat=r.seat AND (l.mode IN ('busy','focus','mute') OR l.at<=?1-180000)) OR
 EXISTS(SELECT 1 FROM mailbox_ids b JOIN onboarding o ON o.mailbox=b.id AND o.verified>0 JOIN host_probes h ON h.host=o.host WHERE b.box=r.seat AND h.incident>0))))
 ORDER BY r.due LIMIT 128`, at)
	if err != nil {
		return err
	}
	type item struct {
		id, seat, sender string
		due              int64
	}
	var batch []item
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.seat, &v.due, &v.sender); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	fallback := s.operator
	if name := env("INBOX_ESCALATION_MAILBOX", ""); name != "" && len(batch) > 0 {
		err := tx.QueryRow("SELECT box FROM addresses JOIN agents ON agents.name=box WHERE address=? AND agents.key IS NOT NULL", name).Scan(&fallback)
		if errors.Is(err, sql.ErrNoRows) {
			fallback = s.operator
		} else if err != nil {
			return err
		}
	}
	for _, v := range batch {
		targets := []string{fallback}
		if v.due <= at {
			targets = append(targets, v.sender)
		}
		for _, target := range targets {
			if _, err = tx.Exec("INSERT OR IGNORE INTO mail_alerts(id,seat,due,target) VALUES(?,?,?,?)", v.id, v.seat, v.due, target); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("UPDATE mail_receipts SET relayed=1,escalated=CASE WHEN due<=? THEN due ELSE escalated END WHERE id=? AND seat=?", at, v.id, v.seat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *server) sendAlert(ctx context.Context) (bool, error) {
	var target, seat string
	err := s.store.Reader().QueryRowContext(ctx, "SELECT target,seat FROM mail_alerts WHERE notice IS NULL LIMIT 1").Scan(&target, &seat)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rows, err := s.store.Reader().QueryContext(ctx, "SELECT id,due FROM mail_alerts WHERE target=? AND seat=? AND notice IS NULL ORDER BY due,id LIMIT 100", target, seat)
	if err != nil {
		return false, err
	}
	type ref struct {
		id  string
		due int64
	}
	var refs []ref
	var body strings.Builder
	for rows.Next() {
		var v ref
		if err = rows.Scan(&v.id, &v.due); err != nil {
			rows.Close()
			return false, err
		}
		refs = append(refs, v)
		fmt.Fprintf(&body, "%s/messages/%s · due %s\n", s.origin, v.id, time.UnixMilli(v.due).UTC().Format(time.RFC3339))
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(refs) == 0 {
		return false, err
	}
	msg := deliverystore.Message{ID: uuid.NewString(), Sender: s.operator, Recipients: []string{target}, Priority: 1, Final: true, Subject: fmt.Sprintf("Requests needing attention for %s (%d)", seat, len(refs)), Body: []byte(body.String())}
	_, err = s.store.AcceptWithReceipt(ctx, "deadline/"+msg.ID, msg, time.Now(), func(tx *sql.Tx) error {
		for _, v := range refs {
			res, e := tx.Exec("UPDATE mail_alerts SET notice=? WHERE target=? AND seat=? AND id=? AND due=? AND notice IS NULL", msg.ID, target, seat, v.id, v.due)
			if e != nil {
				return e
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return deliverystore.ErrConflict
			}
		}
		return nil
	})
	if err == nil {
		s.live.signal()
	}
	return err == nil, err
}

func (s *server) deliveryWorker(ctx context.Context) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if err := s.recordOverdue(ctx, now()); err != nil && ctx.Err() == nil {
			log.Printf("deadline check: %v", err)
		}
		for i := 0; i < 16; i++ {
			sent, err := s.sendAlert(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("deadline notification: %v", err)
			}
			if !sent {
				break
			}
		}
		if err := s.advanceProbes(ctx, now()); err != nil && ctx.Err() == nil {
			log.Printf("delivery probe: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (s *server) receipt(_ http.ResponseWriter, r *http.Request) (any, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	var opened, replied, due int64
	var state, seat string
	err = s.store.Reader().QueryRowContext(r.Context(), `SELECT d.seat,coalesce(r.opened,0),coalesce(r.replied,0),coalesce(r.state,'fyi'),coalesce(r.due,0) FROM messages m JOIN deliveries d USING(seq) LEFT JOIN mail_receipts r ON r.id=m.id AND r.seat=d.seat WHERE m.id=? AND (m.sender=? OR d.seat=? OR ?)`, r.PathValue("id"), actor, actor, strings.HasPrefix(r.URL.Path, "/api/desk/")).Scan(&seat, &opened, &replied, &state, &due)
	return object{"id": r.PathValue("id"), "recipient": seat, "opened": opened, "replied": replied, "due": due, "state": state}, err
}
