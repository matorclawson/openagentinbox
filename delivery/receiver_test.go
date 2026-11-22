package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

type authority struct {
	mu         sync.Mutex
	generation int64
	fail       bool
}

func (a *authority) WithCurrent(ctx context.Context, seat string, f func(Binding, func() error) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail || seat != "reader" {
		return ErrFence
	}
	return f(Binding{"assignment", a.generation}, func() error {
		if a.fail {
			return ErrFence
		}
		return ctx.Err()
	})
}
func TestDurableReceiver(t *testing.T) {
	a := &authority{generation: 1}
	path := filepath.Join(t.TempDir(), "private", "inbox.db")
	i, e := OpenInbox(path, a)
	if e != nil {
		t.Fatal(e)
	}
	env := Envelope{"stable", "message", "reader"}
	r, e := i.Receive(context.Background(), env)
	if e != nil || r.State != "received" {
		t.Fatal(r, e)
	}
	i.Close()
	i, e = OpenInbox(path, a)
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	again, e := i.Receive(context.Background(), env)
	if e != nil || r != again {
		t.Fatal(again, e)
	}
	changed := env
	changed.MessageID = "other"
	if _, e = i.Receive(context.Background(), changed); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	a.fail = true
	if _, e = i.Receive(context.Background(), env); !errors.Is(e, ErrFence) {
		t.Fatal("revoked authority replay", e)
	}
	a.fail = false
	a.generation = 2
	// Received custody survives replacement: replay never invents a second effect.
	again, e = i.Receive(context.Background(), env)
	if e != nil || again.Generation != 1 {
		t.Fatal(again, e)
	}
	env.ID = "new"
	again, e = i.Receive(context.Background(), env)
	if e != nil || again.Generation != 2 {
		t.Fatal(again, e)
	}
}
func TestReceiverConcurrentRetries(t *testing.T) {
	a := &authority{generation: 1}
	i, e := OpenInbox(filepath.Join(t.TempDir(), "private", "db"), a)
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := i.Receive(context.Background(), Envelope{"key", "message", "reader"}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	var n int
	if e := i.db.QueryRow(`SELECT count(*) FROM received`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestReceivedInspectionDoesNotPromotePredecessor(t *testing.T) {
	a := &authority{generation: 1}
	i, e := OpenInbox(filepath.Join(t.TempDir(), "private", "db"), a)
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	if _, e = i.Receive(context.Background(), Envelope{"key", "message", "reader"}); e != nil {
		t.Fatal(e)
	}
	a.generation = 2
	rows, e := i.List(context.Background(), "reader", 0, 5)
	if e != nil || len(rows) != 1 || rows[0].CurrentAssignment {
		t.Fatal(rows, e)
	}
	if _, e = i.List(context.Background(), "foreign", 0, 5); !errors.Is(e, ErrFence) {
		t.Fatal(e)
	}
}
