package deliverystore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGroupCommitIsolatesFailedReceiptAndRetry(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	makeWrite := func(key string) *mailWrite {
		return &mailWrite{ctx: context.Background(), key: key, at: time.Now(), msg: Message{ID: key, Sender: "a", Recipients: []string{"b"}, Body: []byte("test")}}
	}
	good, bad, retry, conflict := makeWrite("ok"), makeWrite("bad"), makeWrite("ok"), makeWrite("ok")
	rejected := errors.New("reject receipt")
	bad.record = func(*sql.Tx) error { return rejected }
	conflict.msg.Body = []byte("other")
	if err = s.commitMail([]*mailWrite{good, bad, retry, conflict}); err != nil {
		t.Fatal(err)
	}
	if good.err != nil || !good.inserted || retry.err != nil || retry.id != good.id || retry.inserted || bad.err != rejected || !errors.Is(conflict.err, ErrConflict) {
		t.Fatal(good, retry, bad, conflict)
	}
	var n int
	for _, table := range []string{"messages", "deliveries", "operations"} {
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatal(table, n, err)
		}
	}
}

func TestQueuedSendsWaitForCommitAndRespectCancellation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.WatchSeat("worker", false)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	type result struct {
		id  string
		err error
	}
	submit := func(ctx context.Context, id string, record func(*sql.Tx) error) <-chan result {
		c := make(chan result, 1)
		go func() {
			got, err := s.AcceptMail(ctx, id, Message{ID: id, Sender: "a", Recipients: []string{"worker"}, Body: []byte("hello")}, time.Now(), record)
			c <- result{got, err}
		}()
		return c
	}
	entered, release := make(chan struct{}), make(chan struct{})
	first := submit(context.Background(), "first", func(*sql.Tx) error { close(entered); <-release; return nil })
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := submit(ctx, "cancelled", nil)
	third := submit(context.Background(), "third", nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mailWrites.mu.Lock()
		count := len(s.mailWrites.pending)
		s.mailWrites.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("sends did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-w.Changes():
		close(release)
		t.Fatal("notified before commit")
	default:
	}
	cancel()
	close(release)
	if f := <-first; f.err != nil || f.id != "first" {
		t.Fatal(f)
	}
	if v := <-second; !errors.Is(v.err, context.Canceled) || v.id != "" {
		t.Fatal(v)
	}
	if v := <-third; v.err != nil || v.id != "third" {
		t.Fatal(v)
	}
	select {
	case <-w.Changes():
	default:
		t.Fatal("missing wake after commit")
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM messages").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
