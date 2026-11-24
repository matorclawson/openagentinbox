package dostoevsky

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishBatchReplay(t *testing.T) {
	dir := t.TempDir()
	c := config()
	c.Topics = map[string]int{"*": 1}
	n := openTest(t, dir, c)
	item := Publication{"fresh", Message{Key: "K", ID: "A", Value: []byte("v")}}
	next := item
	next.Message.ID = "B"
	other := item
	other.Topic = "another"
	items := []Publication{item, next, item, other}
	out, e := n.PublishBatch(ctx, items)
	if e != nil || out[1].Offset != 1 || out[2].Offset != 0 || out[3].Offset != 0 {
		t.Fatal(out, e)
	}
	n.Close()
	n = openTest(t, dir, c)
	again, e := n.PublishBatch(ctx, items)
	if e != nil || again[1].Time != out[1].Time || again[0].Time != out[0].Time {
		t.Fatal(again, e)
	}
	bad := next
	bad.Message.Value = []byte("changed")
	extra := item
	extra.Message.ID = "C"
	if _, e = n.PublishBatch(ctx, []Publication{extra, bad}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = n.Read(ctx, Partition{"fresh", 0}, 2, false); e != ErrEmpty {
		t.Fatal(e)
	}
	if _, e = n.PublishBatch(ctx, []Publication{{"*", item.Message}}); e != ErrInvalid {
		t.Fatal(e)
	}
	wait, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if _, e = n.Read(wait, Partition{"unused", 0}, 0, true); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	if len(n.streams) != 2 {
		t.Fatal("unused topic retained")
	}
}

func TestRecoverPartialBurst(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	items := []Publication{{"words", Message{ID: "1"}}, {"words", Message{ID: "2"}}}
	if _, e := n.PublishBatch(ctx, items); e != nil {
		t.Fatal(e)
	}
	end := n.j.end
	n.Close()
	if e := os.Truncate(filepath.Join(dir, "journal.v1"), end-3); e != nil {
		t.Fatal(e)
	}
	n = openTest(t, dir, config())
	rs, e := n.PublishBatch(ctx, items)
	if e != nil || rs[0].Offset != 0 || rs[1].Offset != 1 {
		t.Fatal(rs, e)
	}
	if _, e = n.Read(ctx, p, 2, false); e != ErrEmpty {
		t.Fatal("unexpected extra record", e)
	}
}

func TestDynamicReadRace(t *testing.T) {
	c := config()
	c.Topics = map[string]int{"*": 1}
	n := openTest(t, t.TempDir(), c)
	limit, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	p := Partition{"live", 0}
	const readers = 8
	done := make(chan error, readers)
	for i := 0; i < readers; i++ {
		go func() {
			for off := int64(0); off < 32; off++ {
				r, e := n.Read(limit, p, off, true)
				if e != nil {
					done <- e
					return
				}
				if r.Offset != off {
					done <- ErrConflict
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < 8; i++ {
		items := make([]Publication, 4)
		for j := range items {
			items[j].Topic = p.Topic
		}
		if _, e := n.PublishBatch(ctx, items); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < readers; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
}
