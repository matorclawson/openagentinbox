package deliverystore

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func msg(id string, to ...string) Message {
	return Message{ID: id, Sender: "alpha", Subject: "s " + id, Priority: 1, Recipients: to, Body: []byte("body of " + id)}
}

func TestAcceptIsIdempotentAndConflictsOnChange(t *testing.T) {
	s := open(t)
	id, err := s.Accept("k1", msg("m1", "beta"), t0)
	if err != nil || id != "m1" {
		t.Fatal(id, err)
	}
	id, err = s.Accept("k1", msg("m1", "beta"), t0)
	if err != nil || id != "m1" {
		t.Fatalf("retry: %v %v", id, err)
	}
	n, _ := s.UnreadCount("beta")
	if n != 1 {
		t.Fatalf("a retry stored a second delivery: %d", n)
	}
	changed := msg("m1", "beta")
	changed.Body = []byte("different")
	if _, err := s.Accept("k1", changed, t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestUnreadPagesAreBoundedOrderedAndDurable(t *testing.T) {
	s := open(t)
	for i := 0; i < 120; i++ {
		if _, err := s.Accept("k"+itoa(i), msg("m"+pad(i), "beta"), t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	var after int64
	pages := 0
	for {
		rows, next, err := s.Unread("beta", after, 50)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(rows) > 50 {
			t.Fatalf("page of %d", len(rows))
		}
		for _, r := range rows {
			seen = append(seen, r.ID)
		}
		if next == 0 {
			break
		}
		after = next
	}
	if len(seen) != 120 || pages != 3 {
		t.Fatalf("seen %d in %d pages", len(seen), pages)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1] >= seen[i] {
			t.Fatalf("not oldest first: %s then %s", seen[i-1], seen[i])
		}
	}
}

func TestFourStatesAreSeparateAndFetchingABodyChangesNothing(t *testing.T) {
	s := open(t)
	s.Accept("k", msg("m1", "beta"), t0)
	rows, _, _ := s.Unread("beta", 0, 50)
	if len(rows) != 1 || rows[0].Delivered {
		t.Fatalf("accepted is not yet delivered: %+v", rows)
	}
	if _, err := s.Body("beta", "m1"); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.Unread("beta", 0, 50)
	if len(rows) != 1 || rows[0].Delivered {
		t.Fatal("fetching a body must not mark delivered or noticed")
	}
	s.MarkDelivered("beta", []int64{rows[0].Seq}, t0)
	rows, _, _ = s.Unread("beta", 0, 50)
	if len(rows) != 1 || !rows[0].Delivered {
		t.Fatal("delivered not recorded")
	}
	if err := s.Notice("beta", "m1", t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount("beta"); n != 0 {
		t.Fatal("noticed mail is still unread")
	}
}

func TestASeatCannotReadOrNoticeAnotherSeatsMessage(t *testing.T) {
	s := open(t)
	s.Accept("k", msg("m1", "beta"), t0)
	if _, err := s.Body("gamma", "m1"); !errors.Is(err, ErrNoSuch) {
		t.Fatalf("gamma read beta's body: %v", err)
	}
	if err := s.Notice("gamma", "m1", t0); !errors.Is(err, ErrNoSuch) {
		t.Fatalf("gamma noticed beta's message: %v", err)
	}
}

func TestCursorSurvivesReopenAndBackupRestores(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.db")
	s, _ := Open(p)
	s.Accept("k", msg("m1", "beta"), t0)
	s.SetCursor("beta", 7, 9)
	bak := filepath.Join(dir, "bak.db")
	if err := s.Backup(bak); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, path := range []string{p, bak} {
		r, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		pg, sy, _ := r.Cursor("beta")
		n, _ := r.UnreadCount("beta")
		r.Close()
		if pg != 7 || sy != 9 || n != 1 {
			t.Fatalf("%s: cursor %d/%d unread %d", path, pg, sy, n)
		}
	}
}

func itoa(i int) string { return pad(i) }
func pad(i int) string {
	s := "0000" + string(rune('0'+i/1000%10)) + string(rune('0'+i/100%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10))
	return s[4:]
}

func TestWakeIsRefusedBeforeAcceptAndCoalescesAfter(t *testing.T) {
	s := open(t)
	if _, err := s.RequestWake("asg-1", "beta", t0); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("a wake before the accept must be refused: %v", err)
	}
	if _, err := s.Accept("asg-1", msg("m1", "beta"), t0); err != nil {
		t.Fatal(err)
	}
	created := 0
	for i := 0; i < 5; i++ {
		c, err := s.RequestWake("asg-1", "beta", t0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if c {
			created++
		}
	}
	n, _ := s.WakeRequests("asg-1")
	if created != 1 || n != 5 {
		t.Fatalf("five requests for one assignment: created %d times, counted %d", created, n)
	}
}

func TestDeliveryNoticeReportsOnlyMessengersStatesAndTheDigest(t *testing.T) {
	s := open(t)
	if _, err := s.DeliveryNotice("asg-3", "beta"); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("before the accept: %v", err)
	}
	s.Accept("asg-3", msg("m3", "beta"), t0)
	s.RequestWake("asg-3", "beta", t0)
	s.RequestWake("asg-3", "beta", t0)
	n, err := s.DeliveryNotice("asg-3", "beta")
	if err != nil || n.State != "accepted" || n.WakeRequests != 2 || len(n.Digest) != 64 {
		t.Fatalf("%+v %v", n, err)
	}
	rows, _, _ := s.Unread("beta", 0, 50)
	s.MarkDelivered("beta", []int64{rows[0].Seq}, t0)
	if n, _ = s.DeliveryNotice("asg-3", "beta"); n.State != "delivered" {
		t.Fatalf("%+v", n)
	}
	s.Notice("beta", "m3", t0)
	if n, _ = s.DeliveryNotice("asg-3", "beta"); n.State != "noticed" {
		t.Fatalf("%+v", n)
	}
	if _, err := s.DeliveryNotice("asg-3", "gamma"); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("a seat the assignment was not sent to: %v", err)
	}
}

func TestAckBindsTheExactEnvelope(t *testing.T) {
	s := open(t)
	s.Accept("asg-9/r1", msg("m9", "beta"), t0)
	n, _ := s.DeliveryNotice("asg-9/r1", "beta")
	if err := s.NoticeAssignment("asg-9/r1", "00"+n.Digest[2:], "beta", t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("an ACK with another digest must conflict: %v", err)
	}
	if n, _ = s.DeliveryNotice("asg-9/r1", "beta"); n.State == "noticed" {
		t.Fatal("a conflicting ACK changed state")
	}
	if err := s.NoticeAssignment("asg-9/r1", n.Digest, "gamma", t0); !errors.Is(err, ErrNoSuch) {
		t.Fatalf("a seat the assignment was not sent to: %v", err)
	}
	if err := s.NoticeAssignment("asg-9/r1", n.Digest, "beta", t0); err != nil {
		t.Fatal(err)
	}
	if n, _ = s.DeliveryNotice("asg-9/r1", "beta"); n.State != "noticed" {
		t.Fatalf("%+v", n)
	}
	// revision 2 is a NEW envelope under a new key; revision 1 stays acknowledged and unchanged
	s.Accept("asg-9/r2", msg("m9b", "beta"), t0)
	if n, _ = s.DeliveryNotice("asg-9/r2", "beta"); n.State != "accepted" {
		t.Fatalf("a new revision starts as accepted: %+v", n)
	}
	if err := s.NoticeAssignment("asg-9/r3", n.Digest, "beta", t0); !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("an ACK of a revision that was never accepted: %v", err)
	}
}

func TestDigestFramingSeparatesFieldAndRecipientBoundaries(t *testing.T) {
	s := open(t)
	a := Message{ID: "m1", Sender: "alpha", Subject: "a\x00b", ConversationID: "c", Recipients: []string{"beta"}, Body: []byte("x")}
	b := Message{ID: "m1", Sender: "alpha", Subject: "a", ConversationID: "b\x00c", Recipients: []string{"beta"}, Body: []byte("x")}
	if _, err := s.Accept("k", a, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept("k", b, t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("field boundary shifted but digest equal: %v", err)
	}
	c := Message{ID: "m2", Sender: "alpha", Subject: "s", Recipients: []string{"a,b"}, Body: []byte("x")}
	d := Message{ID: "m2", Sender: "alpha", Subject: "s", Recipients: []string{"a", "b"}, Body: []byte("x")}
	if _, err := s.Accept("k2", c, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept("k2", d, t0); !errors.Is(err, ErrConflict) {
		t.Fatalf("recipient list [a,b] equals [a b]: %v", err)
	}
}

func stamps(t *testing.T, s *Store, seat string, seq int64) (del, not int64) {
	var d, n *int64
	if err := s.db.QueryRow(`SELECT delivered_at, noticed_at FROM deliveries WHERE seat = ? AND seq = ?`, seat, seq).Scan(&d, &n); err != nil {
		t.Fatal(err)
	}
	if d != nil {
		del = *d
	}
	if n != nil {
		not = *n
	}
	return
}

func TestMarkDeliveredNeverTouchesAnotherSeatAndStampsOnlyMoveForward(t *testing.T) {
	s := open(t)
	if _, err := s.Accept("k", msg("m1", "beta", "gamma"), t0); err != nil {
		t.Fatal(err)
	}
	var seq int64
	s.db.QueryRow(`SELECT seq FROM messages WHERE id = 'm1'`).Scan(&seq)
	if err := s.MarkDelivered("beta", []int64{seq}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if d, _ := stamps(t, s, "gamma", seq); d != 0 {
		t.Fatal("MarkDelivered for beta stamped gamma's delivery")
	}
	first, _ := stamps(t, s, "beta", seq)
	s.MarkDelivered("beta", []int64{seq}, t0.Add(time.Hour))
	if d, _ := stamps(t, s, "beta", seq); d != first {
		t.Fatalf("delivered_at moved from %d to %d", first, d)
	}
	if err := s.NoticeBefore("beta", seq, t0.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, n1 := stamps(t, s, "beta", seq)
	s.NoticeBefore("beta", seq, t0.Add(time.Hour))
	if _, n2 := stamps(t, s, "beta", seq); n2 != n1 {
		t.Fatalf("noticed_at moved from %d to %d", n1, n2)
	}
	if _, g := stamps(t, s, "gamma", seq); g != 0 {
		t.Fatal("NoticeBefore for beta noticed gamma")
	}
	s.Notice("beta", "m1", t0.Add(time.Hour))
	if _, n3 := stamps(t, s, "beta", seq); n3 != n1 {
		t.Fatal("Notice moved noticed_at")
	}
}

func TestVerifyBackupRefusesCorruptionMissingTablesAndOddPaths(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := s.Accept("k"+itoa(i), msg("m"+itoa(i), "beta"), t0); err != nil {
			t.Fatal(err)
		}
	}
	good := filepath.Join(dir, "good.db")
	if err := s.Backup(good); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if n, err := VerifyBackup(good, true); err != nil || n != 300 {
		t.Fatalf("a good backup: %d %v", n, err)
	}
	// G3: a page of the file is damaged: the verification must refuse it (integrity_check result compared, not just run)
	bad := filepath.Join(dir, "bad.db")
	b, _ := os.ReadFile(good)
	for i := 8192; i < len(b) && i < 8192+4096; i++ {
		b[i] ^= 0xFF
	}
	os.WriteFile(bad, b, 0o600)
	if _, err := VerifyBackup(bad, false); err == nil {
		t.Fatal("a backup with a damaged page verified")
	}
	// G3b: integrity_check ANSWERS with a problem row (no SQL error): the answer must be compared with "ok"
	old := integritySQL
	integritySQL = `SELECT 'row 5 missing from index messages_id'`
	_, err = VerifyBackup(good, false)
	integritySQL = old
	if err == nil || !strings.Contains(err.Error(), "integrity_check") {
		t.Fatalf("a file whose integrity_check reports a problem verified (%v)", err)
	}
	// G4: a valid SQLite file without the schema's tables (each one by name)
	for _, tb := range []string{"messages", "deliveries", "operations", "wake_requests", "cursors"} {
		p := filepath.Join(dir, "no-"+tb+".db")
		c, _ := sql.Open("sqlite", p)
		for _, other := range []string{"messages", "deliveries", "operations", "wake_requests", "cursors"} {
			if other != tb {
				c.Exec("CREATE TABLE " + other + " (x INTEGER)")
			}
		}
		c.Close()
		if _, err := VerifyBackup(p, false); err == nil || !strings.Contains(err.Error(), tb) {
			t.Fatalf("a backup without table %s verified (%v)", tb, err)
		}
	}
	// a path holding ? and # still opens THAT file, not another
	odd := filepath.Join(dir, "a?b#c.db")
	g, _ := os.ReadFile(good)
	os.WriteFile(odd, g, 0o600)
	if n, err := VerifyBackup(odd, true); err != nil || n != 300 {
		t.Fatalf("a path with ? and #: %d %v", n, err)
	}
}
