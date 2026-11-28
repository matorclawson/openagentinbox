package channelbinding

import (
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

func TestBindingAuthorizationAndSharedIdentity(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(Schema + `
	INSERT INTO channel_installation VALUES('one',1),('two',1);
	INSERT INTO channel_binding VALUES('one','private','shared','support',1),('two','dm','shared','support',1);
	INSERT INTO channel_membership VALUES('one','private','u1'),('one','private','visitor'),('two','dm','u2');
	INSERT INTO channel_identity VALUES('one','u1','human'),('two','u2','human');`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first := Source{"one", "private", "u1", "support"}
	second := Source{"two", "dm", "u2", "support"}
	a, err := Lookup(ctx, tx, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Lookup(ctx, tx, second)
	if err != nil || a != b || a.Participant != "human" || a.Conversation != "shared" {
		t.Fatal(a, b, err)
	}
	visitor := first
	visitor.Subject = "visitor"
	guest, err := Lookup(ctx, tx, visitor)
	if err != nil || guest.Participant != "" {
		t.Fatal(guest, err)
	}
	outsider := first
	outsider.Seat = "elsewhere"
	if _, err = Lookup(ctx, tx, outsider); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("private binding exposed", err)
	}
	for _, want := range []bool{true, false} {
		got, err := FirstContact(ctx, tx, "one", "private", "support", "v1")
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	if _, err = tx.Exec("DELETE FROM channel_identity WHERE installation='one'"); err != nil {
		t.Fatal(err)
	}
	a, err = Lookup(ctx, tx, first)
	if err != nil || a.Participant != "" {
		t.Fatal("revoked mapping", a, err)
	}
	if _, err = tx.Exec("UPDATE channel_binding SET active=0 WHERE installation='one'"); err != nil {
		t.Fatal(err)
	}
	if _, err = Lookup(ctx, tx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if b, err = Lookup(ctx, tx, second); err != nil || b.Participant != "human" {
		t.Fatal("other installation affected", b, err)
	}
}
