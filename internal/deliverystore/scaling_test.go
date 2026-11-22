package deliverystore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func assertCounts(t *testing.T, s *Store, seat string) {
	t.Helper()
	var unread, pending int
	if err := s.db.QueryRow(`SELECT count(*) FROM deliveries WHERE seat=? AND noticed_at IS NULL`, seat).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM deliveries WHERE seat=? AND owed=1 AND answered_at IS NULL`, seat).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	u, e := s.UnreadCount(seat)
	if e != nil || u != unread {
		t.Fatalf("unread %d != %d: %v", u, unread, e)
	}
	p, e := s.PendingCount(seat)
	if e != nil || p != pending {
		t.Fatalf("pending %d != %d: %v", p, pending, e)
	}
}
func TestCountsStayExactAcrossMutationsAndRollback(t *testing.T) {
	s := open(t)
	for i := 0; i < 60; i++ {
		m := req(i, i%3, "a", "b", "c")
		m.Final = i%7 == 0
		if _, err := s.Accept(fmt.Sprint(i), m, t0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("m%d", i)
		if i%2 == 0 {
			if err := s.Notice("b", id, t0); err != nil {
				t.Fatal(err)
			}
		}
		if i%3 == 0 {
			if err := s.Answer("b", id, t0); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, seat := range []string{"b", "c", "missing"} {
		assertCounts(t, s, seat)
	}
	// A partially executed multi-recipient accept must roll back counters too.
	if _, err := s.Accept("bad", req(100, 0, "a", "b", "b"), t0); err == nil {
		t.Fatal("duplicate recipients must fail")
	}
	assertCounts(t, s, "b")
	if err := s.NoticeBefore("c", 1000, t0); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, s, "c")
	if _, err := s.PruneSeat("b", map[string]bool{"m1": true}); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, s, "b")
	rows := []ImportRow{{ID: "import", Sender: "a", Priority: 0, Body: []byte("x"), Deliveries: []ImportDelivery{{Seat: "b"}}}}
	if _, _, err := s.ImportBatch(rows); err != nil {
		t.Fatal(err)
	}
	rows[0].Deliveries[0].NoticedMilli = 1
	rows[0].Deliveries[0].AnsweredMilli = 2
	for i := 0; i < 2; i++ {
		if _, _, err := s.ImportBatch(rows); err != nil {
			t.Fatal(err)
		}
		assertCounts(t, s, "b")
	}
}
func TestReadIndexMigrationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store?#.db")
	// Create a database using the previous schema, without projections.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(filepath.Dir(path), "old.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO messages(id,sender,priority,created_at,bytes,body) VALUES('x','a',0,1,1,'x'); INSERT INTO deliveries(seat,seq,accepted_at,owed) VALUES('b',1,1,1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(filepath.Join(filepath.Dir(path), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	assertCounts(t, s, "b")
	var p int
	if err := s.db.QueryRow(`SELECT priority FROM deliveries`).Scan(&p); err != nil || p != 0 {
		t.Fatalf("backfill %d %v", p, err)
	}
	// Older writers omit the new priority field: the DB must preserve the index.
	if _, err := s.db.Exec(`INSERT INTO deliveries(seat,seq,accepted_at,owed) VALUES('c',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT priority FROM deliveries WHERE seat='c'`).Scan(&p); err != nil || p != 0 {
		t.Fatalf("old writer %d %v", p, err)
	}
	s.Close()
	s, err = Open(filepath.Join(filepath.Dir(path), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertCounts(t, s, "b")
	assertCounts(t, s, "c")
	unusual, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	unusual.Close()
}
func TestDurabilityAndForeignKeysSurviveConnectionReplacement(t *testing.T) {
	s := open(t)
	s.db.SetMaxIdleConns(0) // every query opens a new connection
	for _, q := range []struct {
		name string
		want int
	}{{"synchronous", 2}, {"foreign_keys", 1}, {"busy_timeout", 5000}, {"fullfsync", 1}} {
		var got int
		if err := s.db.QueryRow("PRAGMA " + q.name).Scan(&got); err != nil || got != q.want {
			t.Fatalf("%s=%d want %d: %v", q.name, got, q.want, err)
		}
	}
}
func TestContextCancellationDoesNotQueueBehindBusyStore(t *testing.T) {
	s := open(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Each call runs with its own bound: a read that ignores the canceled context blocks behind the held transaction, and the test must FAIL by itself then, not hang until
	// the test binary's timeout (reviewer finding on PR 145: the mutant was "killed" only by a timeout).
	within := func(name string, f func() error) {
		done := make(chan error, 1)
		go func() { done <- f() }()
		select {
		case err := <-done:
			if err != context.Canceled {
				t.Fatalf("%s: got %v, want context.Canceled", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: a canceled request waited on the busy connection", name)
		}
	}
	within("UnreadContext", func() error { _, _, err := s.UnreadContext(ctx, "b", 0, 50); return err })
	within("AcceptContext", func() error { _, err := s.AcceptContext(ctx, "k", req(1, 1, "a", "b"), t0); return err })
}
func TestPriorityQueriesSeekBothSeatAndPriority(t *testing.T) {
	s := open(t)
	for _, name := range []string{"unread", "pending"} {
		predicate := "noticed_at IS NULL"
		if name == "pending" {
			predicate = "owed=1 AND answered_at IS NULL"
		}
		q := `SELECT seq FROM deliveries INDEXED BY deliveries_` + name + `_priority WHERE seat=? AND priority=? AND ` + predicate + ` ORDER BY seq DESC LIMIT 5`
		p := plan(t, s, q, "b", 0)
		if !strings.Contains(p, "seat=? AND priority=?") || strings.Contains(p, "SCAN") || strings.Contains(p, "TEMP B-TREE") {
			t.Fatal(p)
		}
	}
}
func TestBatchPreservesThreadRelationships(t *testing.T) {
	s := open(t)
	m := req(1, 1, "a", "b")
	m.ConversationID = "thread"
	m.ParentID = "parent"
	if err := s.AcceptBatch([]Message{m}, t0); err != nil {
		t.Fatal(err)
	}
	v, err := s.InboxView("b", 5, 100)
	if err != nil || len(v.Rows) != 1 || v.Rows[0].ConversationID != "thread" || v.Rows[0].ParentID != "parent" {
		t.Fatalf("%+v %v", v, err)
	}
}
