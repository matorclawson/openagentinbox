package channelrequest

import (
	"context"
	"database/sql"
	"errors"
	"github.com/rodriveracom/openagentinbox/internal/channelqueue"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestTakeoverHandoffAndEscalationAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	connect := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := connect()
	defer func() { db.Close() }()
	if _, err := db.Exec(Schema + channelqueue.Schema); err != nil {
		t.Fatal(err)
	}
	apply := func(run func(*sql.Tx) error) error {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = run(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	err := apply(func(tx *sql.Tx) error { return Open(ctx, tx, "r", "private", "a", 120) })
	if err != nil {
		t.Fatal(err)
	}
	err = apply(func(tx *sql.Tx) error { return Takeover(ctx, tx, "r", 1, "human") })
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = connect()
	err = apply(func(tx *sql.Tx) error { return Resolve(ctx, tx, "r", 1, "a") })
	if !errors.Is(err, ErrStale) {
		t.Fatalf("late reply: %v", err)
	}
	err = apply(func(tx *sql.Tx) error { return Handoff(ctx, tx, "r", 2, "human", "b", 900) })
	if err != nil {
		t.Fatal(err)
	}
	escalate := func(at int64) error {
		return apply(func(tx *sql.Tx) error {
			if err := Escalate(ctx, tx, "r", 3, at); err != nil {
				return err
			}
			return channelqueue.Enqueue(ctx, tx, channelqueue.Item{Installation: "test", Operation: "r-3-overdue", Binding: "private", Message: "overdue-r-3"}, at)
		})
	}
	if err = escalate(899); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err = escalate(900); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = connect()
	if err = escalate(901); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM channel_outbox").Scan(&count); err != nil || count != 1 {
		t.Fatalf("%d notices: %v", count, err)
	}
	if err = apply(func(tx *sql.Tx) error { return Resolve(ctx, tx, "r", 3, "b") }); err != nil {
		t.Fatal(err)
	}
}
