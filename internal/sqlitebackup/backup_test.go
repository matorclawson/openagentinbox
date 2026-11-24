package sqlitebackup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// legacyStore is shaped like a store from before the native schema: it has no messages table.
func legacyStore(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "console.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE letters(seq INTEGER PRIMARY KEY, body TEXT); INSERT INTO letters(body) VALUES('a'),('b')`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSnapshotCopiesAStoreThatCreateRefuses(t *testing.T) {
	dir := t.TempDir()
	src := legacyStore(t, dir)
	out := filepath.Join(dir, "snaps")
	if err := os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), src, filepath.Join(out, "create.db")); err == nil {
		t.Fatal("Create accepted a store without the native tables")
	}
	m, err := Snapshot(context.Background(), src, filepath.Join(out, "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Bytes == 0 || len(m.SHA256) != 64 || m.Path != filepath.Join(out, "snap.db") {
		t.Fatalf("manifest %+v", m)
	}
	db, err := sql.Open("sqlite", m.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM letters`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("copy has %d rows, err %v", n, err)
	}
}

func TestSnapshotRefusesAnExistingFileAndAnOpenDirectory(t *testing.T) {
	dir := t.TempDir()
	src := legacyStore(t, dir)
	private := filepath.Join(dir, "private")
	open := filepath.Join(dir, "open")
	for p, mode := range map[string]os.FileMode{private: 0700, open: 0755} {
		if err := os.Mkdir(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Snapshot(context.Background(), src, filepath.Join(open, "s.db")); err == nil {
		t.Fatal("snapshot went into a directory that others can read")
	}
	if _, err := Snapshot(context.Background(), src, filepath.Join(private, "s.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(context.Background(), src, filepath.Join(private, "s.db")); err == nil {
		t.Fatal("snapshot replaced an existing file")
	}
}
