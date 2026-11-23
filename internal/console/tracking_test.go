package console

import (
	"context"
	"database/sql"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeadlineOutageAndRestart(t *testing.T) {
	ctx := context.Background()
	dbpath := filepath.Join(t.TempDir(), "inbox.db")
	open := func() *server {
		st, err := deliverystore.Open(dbpath)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", dbpath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err = db.Exec(schema + liveSchema + pairSchema + addressSchema + onboardingSchema + trackingSchema); err != nil {
			t.Fatal(err)
		}
		return &server{db: db, store: st, operator: "operator"}
	}
	closeDB := func(s *server) { s.db.Close(); s.store.Close() }
	s := open()
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	due := at.Add(15 * time.Minute).UnixMilli()
	wire := func(id, seat string) {
		_, err := s.store.AcceptWithReceipt(ctx, id, deliverystore.Message{ID: id, Sender: "operator", Recipients: []string{seat}, Subject: "Review", Body: []byte("request")}, at, func(tx *sql.Tx) error {
			_, e := tx.Exec("INSERT INTO mail_receipts(id,seat,state,due) VALUES(?,?,'pending',?)", id, seat, due)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	wire("first", "worker")
	wire("second", "worker")
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	check(s.recordOverdue(ctx, due-1))
	closeDB(s)
	s = open()
	defer closeDB(s)
	late := at.Add(3*time.Hour + 22*time.Minute).UnixMilli()
	for i := 0; i < 3; i++ {
		check(s.recordOverdue(ctx, late))
		_, err := s.sendAlert(ctx)
		check(err)
	}
	var n int
	for query, want := range map[string]int{"SELECT count(*) FROM mail_alerts": 2, "SELECT count(DISTINCT notice) FROM mail_alerts": 1, "SELECT count(*) FROM deliveries WHERE seat='operator'": 1, "SELECT count(*) FROM messages WHERE id='first'": 1, "SELECT count(*) FROM deliveries WHERE seat NOT IN ('operator','worker')": 0} {
		check(s.db.QueryRow(query).Scan(&n))
		if n != want {
			t.Fatalf("%s = %d, want %d", query, n, want)
		}
	}
	// An urgent request for a muted seat is routed early; reaching the deadline cannot duplicate it.
	_, err := s.db.Exec("INSERT INTO listener_checks VALUES('worker',?,'mute')", late)
	check(err)
	_, err = s.store.AcceptWithReceipt(ctx, "urgent", deliverystore.Message{ID: "urgent", Sender: "operator", Subject: "Help", Body: []byte("urgent request"), Priority: 0, Recipients: []string{"worker"}}, time.UnixMilli(late), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO mail_receipts(id,seat,state,due) VALUES('urgent','worker','pending',?)", late+120000)
		return err
	})
	check(err)
	check(s.recordOverdue(ctx, late))
	sent, err := s.sendAlert(ctx)
	check(err)
	if !sent {
		t.Fatal("urgent fallback delayed")
	}
	check(s.recordOverdue(ctx, late+120000))
	sent, err = s.sendAlert(ctx)
	check(err)
	if sent {
		t.Fatal("urgent fallback duplicated")
	}
	// A missed probe produces one incident, survives another sweep, and a valid current confirmation clears it.
	cred := token()
	_, err = s.db.Exec("INSERT INTO paired_cli(id,label,deadline,paired_at,credential_hash) VALUES('host','test',0,1,?)", hash(cred))
	check(err)
	check(s.advanceProbes(ctx, at.UnixMilli()))
	check(s.advanceProbes(ctx, late))
	check(s.advanceProbes(ctx, late+5000))
	var incident int64
	var marker string
	check(s.db.QueryRow("SELECT incident,marker FROM host_probes").Scan(&incident, &marker))
	if incident != late {
		t.Fatal(incident)
	}
	confirm := func(marker string) error {
		r := httptest.NewRequest("POST", "/api/agent/probe/confirm", strings.NewReader(`{"marker":"`+marker+`"}`))
		r.Header.Set("Authorization", "Bearer "+cred)
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("op", "confirm")
		_, err := s.probe(nil, r)
		return err
	}
	if confirm("expired") == nil {
		t.Fatal("stale probe confirmed")
	}
	check(confirm(marker))
	check(s.db.QueryRow("SELECT incident FROM host_probes").Scan(&incident))
	if incident != 0 {
		t.Fatal("incident still open")
	}
}
