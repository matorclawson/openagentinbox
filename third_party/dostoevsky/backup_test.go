package dostoevsky

import (
	"path/filepath"
	"testing"
)

func TestBackupRecovery(t *testing.T) {
	n := openTest(t, t.TempDir(), config())
	publish(t, n, "one")
	if err := n.Commit(ctx, p, "g", 0, []byte("state")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "backup")
	if err := n.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	publish(t, n, "two")
	restored := openTest(t, dst, config())
	pos, err := restored.Position(ctx, p, "g")
	if err != nil || pos.Next != 1 || string(pos.State) != "state" {
		t.Fatal(pos, err)
	}
	if _, err = restored.Read(ctx, p, 1, false); err != ErrEmpty {
		t.Fatal(err)
	}
	if err = n.Backup(ctx, dst); err == nil {
		t.Fatal("existing directory overwritten")
	}
}
