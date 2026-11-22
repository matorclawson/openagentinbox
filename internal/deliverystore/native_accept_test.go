package deliverystore

import (
	"context"
	"errors"
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/sqlitebackup"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func nativeStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "mail.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	for _, seat := range []string{"a", "b", "c"} {
		if e = s.PutSeat(SeatRec{Seat: seat}); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func nativeMessage(id string) Message {
	return Message{ID: id, Sender: "a", Recipients: []string{"b"}, Priority: 1, Body: []byte("Priority: p1\n\nhello")}
}
func TestNativeRetryBudgetAndAtomicReply(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	now := time.Now()
	m := nativeMessage("msg-a-1")
	m.Priority = 0
	if _, e := s.AcceptRecordBudget(ctx, m, now, 1); e != nil {
		t.Fatal(e)
	}
	if r, e := s.AcceptRecordBudget(ctx, m, now, 1); e != nil || !r.Repeated {
		t.Fatalf("retry %+v %v", r, e)
	}
	m.ID = "msg-a-2"
	if _, e := s.AcceptRecordBudget(ctx, m, now, 1); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	reply := Message{ID: "answer", Sender: "b", Recipients: []string{"a"}, ParentID: "msg-a-1", Body: []byte("reply"), Priority: 1}
	// Inject a real SQL failure after answer insertion. Both answer and original state must roll back.
	if _, e := s.db.Exec(`CREATE TRIGGER fail_answer BEFORE UPDATE OF answer_id ON deliveries BEGIN SELECT RAISE(ABORT,'injected'); END;`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReplyRecord(ctx, "b", "msg-a-1", reply, now, 1); e == nil {
		t.Fatal("expected injected failure")
	}
	if _, e := s.ForSeat("a", "answer"); !errors.Is(e, ErrNoSuch) {
		t.Fatal("partial answer", e)
	}
	orig, e := s.ForSeat("b", "msg-a-1")
	if e != nil || orig.AnsweredMilli != 0 {
		t.Fatal(orig, e)
	}
	s.db.Exec(`DROP TRIGGER fail_answer`)
	if _, e = s.ReplyRecord(ctx, "b", "msg-a-1", reply, now, 1); e != nil {
		t.Fatal(e)
	}
	orig, e = s.ForSeat("b", "msg-a-1")
	if e != nil || orig.AnswerID != "answer" || orig.NoticedMilli == 0 {
		t.Fatal(orig, e)
	}
	reply.Body = []byte("different")
	if _, e = s.ReplyRecord(ctx, "b", "msg-a-1", reply, now, 1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestNativeConcurrentRetryAndFence(t *testing.T) {
	s := nativeStore(t)
	m := nativeMessage("same")
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.AcceptRecord(context.Background(), m, time.Now()); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	if e := s.SetFence("moved"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AckSeat("b", "same", time.Now()); !errors.Is(e, ErrFenced) {
		t.Fatal(e)
	}
	m.ID = "new"
	if _, e := s.AcceptRecord(context.Background(), m, time.Now()); !errors.Is(e, ErrFenced) {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE deliveries SET noticed_at=1`); e == nil {
		t.Fatal("fence bypass")
	}
}
func TestNativePageAndBulkAckBeyondDisplayCap(t *testing.T) {
	s := nativeStore(t)
	now := time.Now()
	var batch []Message
	for i := 0; i < 350; i++ {
		m := nativeMessage(fmt.Sprintf("msg-%04d", i))
		m.Body = []byte(strings.Repeat("large", 4096))
		batch = append(batch, m)
	}
	if e := s.AcceptBatch(batch, now); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	var after, through int64
	seen := map[string]bool{}
	for {
		p, e := s.Page(ctx, "b", "all", after, through, 31)
		if e != nil {
			t.Fatal(e)
		}
		through = p.Through
		for _, r := range p.Rows {
			if seen[r.ID] {
				t.Fatal("duplicate")
			}
			seen[r.ID] = true
			if len(r.Body) > 500 {
				t.Fatal("body loaded into summary")
			}
		}
		if p.Next == 0 {
			break
		}
		after = p.Next
	}
	if len(seen) != 350 {
		t.Fatal(len(seen))
	}
	n, _, e := s.AckBacklog(ctx, "b", now.Add(time.Hour).UTC().Format(time.RFC3339Nano), "", -1, false, false, now)
	if e != nil || n != 350 {
		t.Fatal(n, e)
	}
	u, e := s.UnreadCount("b")
	if e != nil || u != 0 {
		t.Fatal(u, e)
	}
}
func TestNativeBackupAndConcurrentReader(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	m := nativeMessage("first")
	if _, e := s.AcceptRecord(ctx, m, time.Now()); e != nil {
		t.Fatal(e)
	}
	tx, e := s.readDB().Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	var n int
	tx.QueryRow(`SELECT count(*) FROM messages`).Scan(&n)
	done := make(chan error, 1)
	go func() { _, e := s.AckSeat("b", "first", time.Now()); done <- e }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader blocked writer")
	}
	var source string
	s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&source)
	backupDir := t.TempDir()
	os.Chmod(backupDir, 0700)
	out := filepath.Join(backupDir, "backup.db")
	man, e := sqlitebackup.Create(ctx, source, out)
	if e != nil {
		t.Fatal(e)
	}
	if man.Messages != 1 || man.Deliveries != 1 {
		t.Fatal(man)
	}
	if _, e = sqlitebackup.Create(ctx, source, out); e == nil {
		t.Fatal("overwrote backup")
	}
	restored, e := Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	d, e := restored.ForSeat("b", "first")
	if e != nil || d.NoticedMilli == 0 {
		t.Fatal("ack absent in backup", d, e)
	}
}
func TestNativeNoCourtesyWakeAndUrgentDuringFocus(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	now := time.Now()
	s.SetState(ctx, "b", "focus", fmt.Sprintf(`{"until":%q}`, now.Add(time.Hour).Format(time.RFC3339Nano)))
	for i := 0; i < 3; i++ {
		m := nativeMessage(fmt.Sprint(i))
		m.Priority = i
		if _, e := s.AcceptRecord(ctx, m, now); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := s.Notifications(ctx, "b", false, now)
	if e != nil || len(rows) != 1 || rows[0].Priority != 0 {
		t.Fatal(rows, e)
	}
}
func TestNativeWatchBoundAndCommitSignal(t *testing.T) {
	s := nativeStore(t)
	var releases []func()
	for i := 0; i < 8; i++ {
		watch, e := s.WatchSeat("b", true)
		if e != nil {
			t.Fatal(e)
		}
		releases = append(releases, watch.Close)
	}
	if _, e := s.WatchSeat("b", true); e == nil {
		t.Fatal("seat waiter limit missing")
	}
	for _, r := range releases {
		r()
		r()
	}
	watch, e := s.WatchSeat("b", true)
	if e != nil {
		t.Fatal(e)
	}
	defer watch.Close()
	ch := watch.Changes()
	if _, e = s.AcceptRecord(context.Background(), nativeMessage("wake"), time.Now()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("committed send did not wake reader")
	}
	d, e := s.ForSeat("b", "wake")
	if e != nil || string(d.Body) == "" {
		t.Fatal("wake preceded durable message", e)
	}
}
func TestNativeRollbackSnapshotIncludesOldStateChanges(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	m := nativeMessage("before-cutover")
	if _, e := s.AcceptRecord(ctx, m, time.Now().Add(-24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AckSeat("b", m.ID, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := s.SetFence("rollback-test"); e != nil {
		t.Fatal(e)
	}
	var source string
	s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&source)
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	out := filepath.Join(dir, "rollback.db")
	if _, e := sqlitebackup.Create(ctx, source, out); e != nil {
		t.Fatal(e)
	}
	r, e := Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	d, e := r.ForSeat("b", m.ID)
	if e != nil || d.NoticedMilli == 0 {
		t.Fatal(d, e)
	}
	if e = r.ClearFence("wrong-reason"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = r.ClearFence("rollback-test"); e != nil {
		t.Fatal(e)
	}
}
