package dostoevsky

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNotifications(t *testing.T) {
	n := NewNotifications[string](2)
	a, release, e := n.Subscribe("s")
	if e != nil {
		t.Fatal(e)
	}
	b, done, e := n.Subscribe("s")
	if e != nil || a != b {
		t.Fatal("expected shared channel")
	}
	if _, _, e = n.Subscribe("other"); e != ErrLimit {
		t.Fatal(e)
	}
	n.Signal("other")
	select {
	case <-a:
		t.Fatal("wrong key")
	default:
	}
	n.Signal("s")
	n.Signal("s")
	select {
	case <-b:
	default:
		t.Fatal("lost notification")
	}
	done()
	done()
	c, finish, e := n.Subscribe("s")
	if e != nil || c == a {
		t.Fatal("stale channel")
	}
	release()
	finish()
	if len(n.listenersByKey) != 0 {
		t.Fatal("idle entry retained")
	}
}
func BenchmarkPendingSignal8(b *testing.B) {
	n := NewNotifications[string](8)
	for i := 0; i < 8; i++ {
		_, release, _ := n.Subscribe("orders")
		defer release()
	}
	n.Signal("orders")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n.Signal("orders")
	}
}
func TestBatchCheckpointReplay(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	for _, id := range []string{"a", "b", "c"} {
		publish(t, n, id)
	}
	for _, l := range []Limits{{2, 100}, {8, 18}} {
		page, e := n.ReadBatch(ctx, p, 0, l, true)
		if e != nil || len(page.Records) != 2 || page.Next != 2 || page.Records[1].ID != "b" {
			t.Fatal(page, e)
		}
	}
	if _, e := n.ReadBatch(ctx, p, 0, Limits{1, 1}, false); e != ErrLimit {
		t.Fatal(e)
	}
	if e := n.CommitBatch(ctx, p, "g", 0, 2, []byte("ab")); e != nil {
		t.Fatal(e)
	}
	if e := n.CommitBatch(ctx, p, "g", 0, 3, nil); e != ErrConflict {
		t.Fatal(e)
	}
	n.Close()
	n = openTest(t, dir, config())
	pos, e := n.Position(ctx, p, "g")
	if e != nil || pos.Next != 2 || string(pos.State) != "ab" {
		t.Fatal(pos, e)
	}
	stop := errors.New("done")
	e = ConsumePages(ctx, n, p, pos.Next, Limits{10, 100}, func(_ context.Context, prior int64, page Page) error {
		if prior != 2 || page.Next != 3 || page.Records[0].ID != "c" {
			t.Fatal(prior, page)
		}
		return stop
	})
	if e != stop {
		t.Fatal(e)
	}
	w, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, e = n.ReadBatch(w, p, 3, Limits{1, 100}, true); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
}
