package deliverystore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func req(id, prio int, from string, to ...string) Message {
	return Message{ID: fmt.Sprintf("m%d", id), Sender: from, Subject: "s", Priority: prio, Recipients: to, Body: []byte("body")}
}

func TestPendingSurvivesReadAndReopenUntilAnswered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []int{0, 1, 2} { // p0, p1, p2
		if _, err := s.Accept(fmt.Sprintf("k%d", i), req(i, p, "alpha", "beta"), t0); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.PendingCount("beta"); n != 2 {
		t.Fatalf("p0 and p1 are owed, p2 is not: %d", n)
	}
	if err := s.Notice("beta", "m0", t0); err != nil { // reading acknowledges it
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount("beta"); n != 2 {
		t.Fatalf("reading removes it from unread: %d", n)
	}
	if n, _ := s.PendingCount("beta"); n != 2 {
		t.Fatalf("reading must NOT remove it from pending: %d", n)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, _, _ := s.Pending("beta", 0, 10)
	if len(rows) != 2 || rows[0].ID != "m0" || rows[1].ID != "m1" {
		t.Fatalf("after a restart pending is still the two requests, oldest first: %+v", rows)
	}
	if err := s.Answer("beta", "m0", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer("beta", "m0", t0.Add(time.Hour)); err != nil {
		t.Fatal("answering twice must be idempotent: ", err)
	}
	if n, _ := s.PendingCount("beta"); n != 1 {
		t.Fatalf("answered leaves pending: %d", n)
	}
	if err := s.Answer("gamma", "m1", t0); err != ErrNoSuch {
		t.Fatalf("another seat cannot answer for beta: %v", err)
	}
	if n, _ := s.PendingCount("alpha"); n != 0 {
		t.Fatalf("alpha has no delivery row at all here (it only sent): %d", n)
	}
}

// Ruling 2026-10-26: a p0/p1 addressed to its own sender IS pending (the CLI lists it; three such messages exist in the live store). This case
// fails if the sender exclusion ever returns: the sender is also a recipient (a group send that includes itself) and owes an answer to its copy.
func TestSelfAddressedRequestIsPendingForTheSenderToo(t *testing.T) {
	s := open(t)
	if _, err := s.Accept("g1", req(1, 1, "alpha", "alpha", "beta"), t0); err != nil { // sender alpha, recipients alpha and beta
		t.Fatal(err)
	}
	if n, _ := s.PendingCount("alpha"); n != 1 {
		t.Fatalf("the sender's own copy of a p1 is pending, as the CLI lists it: %d", n)
	}
	if rows, _, _ := s.Pending("alpha", 0, 10); len(rows) != 1 || rows[0].ID != "m1" {
		t.Fatalf("and it is on the sender's pending page: %+v", rows)
	}
	if n, _ := s.PendingCount("beta"); n != 1 {
		t.Fatalf("the other recipient owes it too: %d", n)
	}
	if err := s.Answer("alpha", "m1", t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.PendingCount("alpha"); n != 0 {
		t.Fatalf("answering clears it: %d", n)
	}
	// a p2 to oneself is still not owed
	if _, err := s.Accept("g2", req(2, 2, "alpha", "alpha"), t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.PendingCount("alpha"); n != 0 {
		t.Fatalf("a courtesy message is never owed: %d", n)
	}
}

// Council on #106 (P3): two processes opening the same pre-pending database at once must both succeed (BEGIN IMMEDIATE serializes them).
func TestConcurrentMigrationOfAnOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(schema, "  owed INTEGER NOT NULL DEFAULT 0,\n  answered_at INTEGER,\n", "", 1)
	if _, err := db.Exec(old); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO messages (id,sender,subject,priority,created_at,bytes,preview,body) VALUES ('a','x','s',0,1,1,'p','b')`)
	db.Exec(`INSERT INTO deliveries (seat,seq,accepted_at) VALUES ('beta',1,1)`)
	db.Close()
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() {
			s, err := Open(path)
			if err == nil {
				s.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 6; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("a concurrent open failed: %v", err)
		}
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.PendingCount("beta"); n != 1 {
		t.Fatalf("backfill happened exactly once: %d", n)
	}
}

func TestPendingPagesAreBoundedAndCursorSafeUnderLateArrivals(t *testing.T) {
	s := open(t)
	for i := 1; i <= 7; i++ {
		s.Accept(fmt.Sprintf("k%d", i), req(i, 1, "alpha", "beta"), t0)
	}
	p1, next, _ := s.Pending("beta", 0, 3)
	if len(p1) != 3 || next != p1[2].Seq {
		t.Fatalf("page 1: %d rows next %d", len(p1), next)
	}
	// a message arrives with an EARLIER wall-clock time than the page already served: the cursor is arrival order, so it is not skipped
	s.Accept("klate", req(99, 1, "alpha", "beta"), t0.Add(-48*time.Hour))
	var got []string
	after := next
	for {
		rows, n, err := s.Pending("beta", after, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			got = append(got, r.ID)
		}
		if n == 0 {
			break
		}
		after = n
	}
	if len(got) != 5 || got[len(got)-1] != "m99" {
		t.Fatalf("the late arrival must come on a later page, never be skipped: %v", got)
	}
}

func TestMigrationOfAPendingLessDatabaseBackfillsOwed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(strings.Replace(schema, "  owed INTEGER NOT NULL DEFAULT 0,\n  answered_at INTEGER,\n", "", 1), "PRAGMA journal_mode = WAL;", "PRAGMA journal_mode = WAL;", 1)
	if strings.Contains(old, "owed") {
		t.Fatal("the fixture did not strip the new columns")
	}
	if _, err := db.Exec(old); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO messages (id,sender,subject,priority,created_at,bytes,preview,body) VALUES ('a','x','s',0,1,1,'p','b'),('b','x','s',2,1,1,'p','b'),('c','beta','s',1,1,1,'p','b')`)
	db.Exec(`INSERT INTO deliveries (seat,seq,accepted_at) VALUES ('beta',1,1),('beta',2,1),('beta',3,1)`)
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, _, _ := s.Pending("beta", 0, 10)
	if len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "c" {
		t.Fatalf("the p0 and the seat's own p1 are owed (p2 is not): %+v", rows)
	}
	s.Close()
	if s, err = Open(path); err != nil { // idempotent
		t.Fatal(err)
	}
}

func plan(t *testing.T, s *Store, q string, args ...any) string {
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, " | ")
}

// The normal query path must use the intended indexes and never scan a whole table.
func TestNormalQueriesUseTheIntendedIndexes(t *testing.T) {
	s := open(t)
	pl := plan(t, s, `SELECT m.seq FROM deliveries d INDEXED BY deliveries_pending JOIN messages m ON m.seq = d.seq WHERE d.seat = ? AND d.owed = 1 AND d.answered_at IS NULL AND d.seq > ? ORDER BY d.seq LIMIT ?`, "beta", 0, 11)
	t.Log("PENDING:", pl)
	if !strings.Contains(pl, "deliveries_pending") || strings.Contains(pl, "SCAN d") || strings.Contains(pl, "USE TEMP B-TREE") {
		t.Fatalf("pending page plan: %s", pl)
	}
	ul := plan(t, s, `SELECT m.seq FROM deliveries d INDEXED BY deliveries_unread JOIN messages m ON m.seq = d.seq WHERE d.seat = ? AND d.noticed_at IS NULL AND d.seq > ? ORDER BY d.seq LIMIT ?`, "beta", 0, 11)
	t.Log("UNREAD: ", ul)
	if !strings.Contains(ul, "deliveries_unread") || strings.Contains(ul, "SCAN d") || strings.Contains(ul, "USE TEMP B-TREE") {
		t.Fatalf("unread page plan: %s", ul)
	}
	bl := plan(t, s, `SELECT m.body FROM messages m JOIN deliveries d ON d.seq = m.seq WHERE m.id = ? AND d.seat = ?`, "m1", "beta")
	t.Log("BODY:   ", bl)
	if strings.Contains(bl, "SCAN m") {
		t.Fatalf("body fetch scans messages: %s", bl)
	}
}

// Representative size: ~100,000 messages across 40 seats, 8 seats busy. Opt-in (OPENAGENTINBOX_STORE_BENCH=1); it uses a synthetic database in a
// temp directory and never the live store. It reports first-page latency, warm (same process, page cache hot) and after reopen (cold
// process, OS cache not flushed), at p50/p95/max over every seat and both views.
func TestFirstPageLatencyAt100kMessages(t *testing.T) {
	if os.Getenv("OPENAGENTINBOX_STORE_BENCH") != "1" {
		t.Skip("set OPENAGENTINBOX_STORE_BENCH=1")
	}
	path := filepath.Join(os.Getenv("OPENAGENTINBOX_STORE_BENCH_DIR"), "bench.db")
	if os.Getenv("OPENAGENTINBOX_STORE_BENCH_DIR") == "" {
		path = filepath.Join(t.TempDir(), "bench.db")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const N, seats = 100000, 40
	var batch []Message
	flush := func() {
		if len(batch) > 0 {
			if err := s.AcceptBatch(batch, t0); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	for i := 0; i < N; i++ {
		to := fmt.Sprintf("seat%02d", i%seats)
		if i%5 == 0 { // a fifth of the traffic goes to the 8 busy seats' backlogs
			to = fmt.Sprintf("seat%02d", i%8)
		}
		batch = append(batch, Message{ID: fmt.Sprintf("msg-%d", i), Sender: "sender", Subject: "s", Priority: i % 3, Recipients: []string{to}, Body: []byte(strings.Repeat("x", 1500))})
		if len(batch) == 5000 {
			flush()
		}
	}
	flush()
	measure := func(label string, st *Store) {
		var ds []time.Duration
		for i := 0; i < seats; i++ {
			seat := fmt.Sprintf("seat%02d", i)
			for _, view := range []string{"unread", "pending"} {
				t1 := time.Now()
				var err error
				if view == "unread" {
					_, _, err = st.Unread(seat, 0, 5)
				} else {
					_, _, err = st.Pending(seat, 0, 5)
				}
				if err != nil {
					t.Fatal(err)
				}
				ds = append(ds, time.Since(t1))
			}
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		t.Logf("%-22s n=%d p50=%v p95=%v max=%v", label, len(ds), ds[len(ds)/2], ds[len(ds)*95/100], ds[len(ds)-1])
		if ds[len(ds)*95/100] > time.Second {
			t.Errorf("%s p95 over one second", label)
		}
	}
	measure("warm (same process)", s)
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	measure("after reopen (process)", s2)
	fi, _ := os.Stat(path)
	t.Logf("database %d MB, %d messages", fi.Size()>>20, N)
}

// The queries the inbox and pending views really run (view.go: every p0, then the newest p1, oldest first) must use the partial indexes, need no sort and
// never scan a table (the external council asked for query plans showing indexed access).
func TestTheViewQueriesUseTheIntendedIndexes(t *testing.T) {
	s := open(t)
	cases := []struct{ name, q, idx string }{
		{"unread p0", `SELECT ` + rowCols + ` FROM deliveries d ` + unreadCond + ` AND m.priority = 0 ORDER BY d.seq ASC`, "deliveries_unread"},
		{"unread newest p1", `SELECT ` + rowCols + ` FROM deliveries d ` + unreadCond + ` AND m.priority = 1 ORDER BY d.seq DESC LIMIT 100`, "deliveries_unread"},
		{"pending p0", `SELECT ` + rowCols + ` FROM deliveries d ` + pendingCond + ` AND m.priority = 0 ORDER BY d.seq ASC`, "deliveries_pending"},
		{"pending newest p1", `SELECT ` + rowCols + ` FROM deliveries d ` + pendingCond + ` AND m.priority = 1 ORDER BY d.seq DESC LIMIT 50`, "deliveries_pending"},
	}
	for _, c := range cases {
		pl := plan(t, s, c.q, "beta")
		t.Logf("%s: %s", c.name, pl)
		if !strings.Contains(pl, c.idx) || strings.Contains(pl, "SCAN d") || strings.Contains(pl, "SCAN m") || strings.Contains(pl, "USE TEMP B-TREE") {
			t.Fatalf("%s: the plan must use %s with no scan and no sort: %s", c.name, c.idx, pl)
		}
	}
}
