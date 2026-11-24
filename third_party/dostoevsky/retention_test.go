package dostoevsky

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRetentionRecovery(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	for _, id := range []string{"a", "b", "c", "d"} {
		publish(t, n, id)
	}
	if e := n.CommitBatch(ctx, p, "fast", 0, 3, []byte("ok")); e != nil {
		t.Fatal(e)
	}
	if e := n.Commit(ctx, p, "slow", 0, []byte("slow")); e != nil {
		t.Fatal(e)
	}
	if e := n.Retain(ctx, map[Partition]int64{p: 2}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := n.Commit(ctx, p, "slow", 1, []byte("advanced")); e != nil {
		t.Fatal(e)
	}
	before, _ := os.Stat(filepath.Join(dir, "journal.v1"))
	if e := n.Retain(ctx, map[Partition]int64{p: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e := n.Read(ctx, p, 1, false); e != ErrExpired {
		t.Fatal(e)
	}
	if r, e := n.Read(ctx, p, 2, false); e != nil || r.ID != "c" {
		t.Fatal(r, e)
	}
	duplicate := publish(t, n, "c")
	if duplicate.Offset != 2 {
		t.Fatal(duplicate)
	}
	if r := publish(t, n, "a"); r.Offset != 4 {
		t.Fatal(r)
	}
	n.Close()
	n = openTest(t, dir, config())
	if _, e := n.Read(ctx, p, 0, false); e != ErrExpired {
		t.Fatal(e)
	}
	pos, e := n.Position(ctx, p, "fast")
	if e != nil || pos.Next != 3 || string(pos.State) != "ok" {
		t.Fatal(pos, e)
	}
	if r := publish(t, n, "c"); r.Offset != 2 {
		t.Fatal(r)
	}
	if r := publish(t, n, "new"); r.Offset != 5 {
		t.Fatal(r)
	}
	after, _ := os.Stat(filepath.Join(dir, "journal.v1"))
	if before.Size() == 0 || after.Size() == 0 {
		t.Fatal("empty journal")
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if e = n.Backup(ctx, backup); e != nil {
		t.Fatal(e)
	}
	copy := openTest(t, backup, config())
	if _, e = copy.Read(ctx, p, 1, false); e != ErrExpired {
		t.Fatal(e)
	}
}
func TestRetentionCancelAndEmpty(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	publish(t, n, "a")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e := n.Retain(cancelled, map[Partition]int64{p: 1}); e != context.Canceled {
		t.Fatal(e)
	}
	if e := n.Retain(ctx, map[Partition]int64{p: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := n.Read(ctx, p, 0, false); e != ErrExpired {
		t.Fatal(e)
	}
	n.Close()
	n = openTest(t, dir, config())
	if r := publish(t, n, "again"); r.Offset != 1 {
		t.Fatal(r)
	}
	if e := n.Retain(ctx, map[Partition]int64{p: 2}); e != nil {
		t.Fatal(e)
	}
	n.Close()
	n = openTest(t, dir, config())
	if r := publish(t, n, "next"); r.Offset != 2 {
		t.Fatal(r)
	}
}

func TestRepeatedRetentionWithoutReopen(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	for _, id := range []string{"a", "b", "c"} {
		publish(t, n, id)
	}
	for _, floor := range []int64{1, 2} {
		if err := n.Retain(ctx, map[Partition]int64{p: floor}); err != nil {
			t.Fatal(err)
		}
	}
	if r := publish(t, n, "d"); r.Offset != 3 {
		t.Fatal(r)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	n = openTest(t, dir, config())
	if _, err := n.Read(ctx, p, 1, false); err != ErrExpired {
		t.Fatal(err)
	}
	if r, err := n.Read(ctx, p, 3, false); err != nil || r.ID != "d" {
		t.Fatal(r, err)
	}
}
