package backupflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ephemeral"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	must(t, os.Mkdir(remote, 0700))
	t.Setenv("TEST_BACKUP_REMOTE", remote)
	c := Config{Mode: "backup-run", StateDir: filepath.Join(root, "state"), Instance: "mail", Window: "2026-11-22", Source: filepath.Join(root, "mail.db"), Age: filepath.Join(root, "test-codec"), Adapter: filepath.Join(root, "test-transport"), Recipient: "age1" + strings.Repeat("q", 58)}
	// Fixture-only reversible codec and local immutable transport. Neither is shipped as a command.
	must(t, os.WriteFile(c.Age, []byte("#!/bin/sh\nset -eu\ncase $1 in --encrypt) exec gzip -c \"$4\";; --decrypt) exec gzip -dc \"$4\";; *) exit 2;; esac\n"), 0700))
	must(t, os.WriteFile(c.Adapter, []byte(`#!/bin/sh
set -eu
f="$TEST_BACKUP_REMOTE/$2"
case "$1" in
get) [ -f "$f" ] || exit 44; cat "$f";;
put) cat > "$f.tmp"; test "$(wc -c < "$f.tmp")" -eq "$3"; ln "$f.tmp" "$f"; rm "$f.tmp"; echo put >> "$TEST_BACKUP_REMOTE/puts"
if [ -f "$TEST_BACKUP_REMOTE/lose-reply" ]; then rm "$TEST_BACKUP_REMOTE/lose-reply"; exit 1; fi;;
*) exit 2;;
esac
`), 0700))
	s, e := deliverystore.Open(c.Source)
	must(t, e)
	defer s.Close()
	_, e = s.Accept("op", deliverystore.Message{ID: "m1", Sender: "a", Recipients: []string{"b"}, Body: []byte("private body"), Priority: 1}, time.Now())
	must(t, e)
	return c
}
func run(t *testing.T, c Config, r Receipt) Receipt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, e := Run(ctx, c, r)
	must(t, e)
	return out
}
func interrupted(t *testing.T, c Config, r Receipt, step string) {
	t.Helper()
	must(t, privateDir(c.StateDir))
	s, e := ephemeral.Open(filepath.Join(c.StateDir, "workflows.db"))
	must(t, e)
	defer s.Close()
	w := worker{Config: c}
	w.dir = filepath.Join(c.StateDir, w.id())
	must(t, w.register(s, r))
	for {
		task, e := s.Poll(w.id(), 30*time.Second)
		must(t, e)
		if task == nil {
			t.Fatal("missing task")
		}
		must(t, json.Unmarshal(task.Input, &r))
		must(t, w.action(context.Background(), task.Activity, &r))
		if task.Activity == step {
			must(t, s.Heartbeat(task.Run, task.Token, time.Millisecond))
			return
		} // store closed before activity completion was recorded
		b, e := json.Marshal(r)
		must(t, e)
		must(t, s.Finish(task.Run, task.Token, b, ""))
	}
}
func restoreConfig(c Config) Config {
	return Config{Mode: "restore-drill", StateDir: c.StateDir + "-custodian", Adapter: c.Adapter, Age: c.Age, Instance: c.Instance, Window: c.Window, Identity: c.Source + ".key", Drill: "week-1"}
}
func TestReplayBoundaries(t *testing.T) {
	for _, step := range []string{"snapshot", "encrypt", "offsite", "restore"} {
		t.Run(step, func(t *testing.T) {
			c := fixture(t)
			if step != "restore" {
				interrupted(t, c, Receipt{Instance: c.Instance, Window: c.Window}, step)
			}
			r := run(t, c, Receipt{})
			if r.Status != "remote-readback-verified" || r.Messages != 1 || r.Deliveries != 1 {
				t.Fatalf("unexpected receipt %+v", r)
			}
			if run(t, c, Receipt{}) != r {
				t.Fatal("backup receipt changed on replay")
			}
			puts, e := os.ReadFile(filepath.Join(os.Getenv("TEST_BACKUP_REMOTE"), "puts"))
			must(t, e)
			if string(puts) != "put\n" {
				t.Fatal("repeated upload")
			}
			d := restoreConfig(c)
			if step == "restore" {
				interrupted(t, d, r, step)
			}
			verified := run(t, d, r)
			if verified.Status != "restore-verified" || verified.Drill != d.Drill {
				t.Fatal(verified)
			}
			for _, cfg := range []Config{c, d} {
				if _, e := os.Stat(filepath.Join(cfg.StateDir, (worker{Config: cfg}).id())); !os.IsNotExist(e) {
					t.Fatal("staging still present", e)
				}
			}
		})
	}
}
func TestAmbiguousUploadAndCorruptRemote(t *testing.T) {
	c := fixture(t)
	remote := os.Getenv("TEST_BACKUP_REMOTE")
	must(t, os.WriteFile(filepath.Join(remote, "lose-reply"), nil, 0600))
	if _, e := Run(context.Background(), c, Receipt{}); e == nil {
		t.Fatal("lost upload response reported success")
	}
	r := run(t, c, Receipt{})
	d := restoreConfig(c)
	run(t, d, r)
	must(t, os.WriteFile(filepath.Join(remote, r.key()), []byte("damaged"), 0600))
	d.Drill = "week-2"
	if _, e := Run(context.Background(), d, r); e == nil {
		t.Fatal("subsequent drill missed damaged remote object")
	}
	c.Recipient = "age1" + strings.Repeat("p", 58)
	if _, e := Run(context.Background(), c, Receipt{}); e == nil {
		t.Fatal("different recipient accepted for existing run")
	}
}
func TestRealAgeRoundTrip(t *testing.T) {
	age, e := exec.LookPath("age")
	if e != nil {
		t.Skip("age executable unavailable")
	}
	keygen, e := exec.LookPath("age-keygen")
	if e != nil {
		t.Skip("age-keygen unavailable")
	}
	c := fixture(t)
	d := restoreConfig(c)
	must(t, exec.Command(keygen, "-o", d.Identity).Run())
	pub, e := exec.Command(keygen, "-y", d.Identity).Output()
	must(t, e)
	c.Age = age
	c.Recipient = strings.TrimSpace(string(pub))
	d.Age = age
	run(t, d, run(t, c, Receipt{}))
}
