package channelqueue

import (
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestCrashAfterRemoteAcceptance(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "store.db")
	open := func() *sql.DB {
		db, e := sql.Open("sqlite", p)
		if e != nil {
			t.Fatal(e)
		}
		return db
	}
	db := open()
	if _, err := db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	x := Item{Installation: "first", Operation: "reply", Binding: "dm", Message: "revision1"}
	y := x
	y.Installation = "second"
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []Item{x, x, y} {
		if err = Enqueue(ctx, tx, op, 0); err != nil {
			t.Fatal(err)
		}
	}
	changed := x
	changed.Binding = "public"
	if err = Enqueue(ctx, tx, changed, 0); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, err := Claim(ctx, db, x.Installation, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The fake peer accepts one POST, then loses its connection before acknowledgement.
	posted := map[string]int{first.Operation: 1}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	if err = Recover(ctx, db, x.Installation); err != nil {
		t.Fatal(err)
	}
	if _, err = Claim(ctx, db, x.Installation, 1000); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unsafe retry %v", err)
	}
	second, err := Claim(ctx, db, y.Installation, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = Finish(ctx, db, second, Retryable, 30); err != nil {
		t.Fatal(err)
	}
	if _, err = Claim(ctx, db, y.Installation, 29); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	retry, err := Claim(ctx, db, y.Installation, 30)
	if err != nil {
		t.Fatal(err)
	}
	if err = Finish(ctx, db, second, Confirmed, 0); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err = Finish(ctx, db, retry, Confirmed, 0); err != nil {
		t.Fatal(err)
	}
	if posted[first.Operation] != 1 {
		t.Fatal("missing remote receipt")
	}
	if err = Reconcile(ctx, db, first, Confirmed, 0, "receipt-1"); err != nil {
		t.Fatal(err)
	}
	if err = Reconcile(ctx, db, first, Retryable, 0, "stale-decision"); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	var evidence string
	if err = db.QueryRow("SELECT evidence FROM channel_decisions WHERE installation=? AND operation=? AND attempt=?", first.Installation, first.Operation, first.Attempt).Scan(&evidence); err != nil || evidence != "receipt-1" {
		t.Fatal("missing decision audit", evidence, err)
	}
}
