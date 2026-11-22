// Package backupflow drives resumable backup jobs outside the mailbox server.
package backupflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"ephemeral"
)

type Config struct{ Mode, Drill, StateDir, Instance, Window, Source, Recipient, Identity, Adapter, Age string }
type Receipt struct {
	Drill         string `json:"drill_id,omitempty"`
	Instance      string `json:"instance"`
	Window        string `json:"window"`
	SnapshotHash  string `json:"snapshot_sha256"`
	SnapshotBytes int64  `json:"snapshot_bytes"`
	Messages      int64  `json:"messages"`
	Deliveries    int64  `json:"deliveries"`
	CipherHash    string `json:"ciphertext_sha256"`
	CipherBytes   int64  `json:"ciphertext_bytes"`
	Status        string `json:"status"`
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (r Receipt) key() string { return r.Instance + "-" + r.Window + "-" + r.CipherHash + ".age" }
func (r Receipt) valid() bool {
	return nameRE.MatchString(r.Instance) && nameRE.MatchString(r.Window) && hashRE.MatchString(r.SnapshotHash) && hashRE.MatchString(r.CipherHash) && r.CipherBytes > 0 && r.SnapshotBytes > 0 && r.Messages >= 0 && r.Deliveries >= 0
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("expected private directory %s", path)
	}
	return nil
}

type worker struct {
	Config
	lock  *os.File
	dir   string
	lease time.Duration
}

func (w worker) id() string {
	id := w.Mode + "." + w.Instance + "." + w.Window
	if w.Mode == "restore-drill" {
		return id + "." + w.Drill
	}
	return id
}
func (w worker) binding() string {
	// Bind queued work to the same source, destination adapter and encryption recipient.
	b, _ := json.Marshal([]string{w.Mode, w.Instance, w.Source, w.Recipient, w.Adapter})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (w worker) register(s *ephemeral.Store, r Receipt) error {
	var steps []ephemeral.Step
	names := []string{"snapshot", "encrypt", "offsite", "cleanup"}
	if w.Mode == "restore-drill" {
		names = []string{"restore", "cleanup"}
	}
	for _, step := range names {
		steps = append(steps, ephemeral.Step{Kind: "activity", Name: step, Attempts: 6, BackoffMillis: 1000})
	}
	if err := s.Register(w.Mode, 1, steps); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	h := sha256.Sum256(b)
	attrs := map[string]string{"binding": w.binding(), "input": hex.EncodeToString(h[:]), "window": w.Window}
	if w.Mode == "restore-drill" {
		attrs["drill"] = w.Drill
	}
	d, err := s.Describe(w.id())
	if errors.Is(err, ephemeral.ErrMissing) {
		return s.Start(w.id(), w.Mode, 1, w.id(), b, attrs)
	}
	if err != nil {
		return err
	}
	if d.Attributes["input"] != attrs["input"] || d.Attributes["binding"] != attrs["binding"] {
		return errors.New("run already exists with different input or configuration")
	}
	return nil
}
func Run(ctx context.Context, c Config, r Receipt) (Receipt, error) {
	if !nameRE.MatchString(c.Instance) || !nameRE.MatchString(c.Window) || !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.Adapter) {
		return Receipt{}, errors.New("invalid instance, window, state directory or Connect adapter")
	}
	switch c.Mode {
	case "backup-run":
		if !filepath.IsAbs(c.Source) || !regexp.MustCompile(`^age1[0-9a-z]{58}$`).MatchString(c.Recipient) || c.Identity != "" {
			return Receipt{}, errors.New("backup requires absolute database path and pinned age X25519 public recipient")
		}
		r = Receipt{Instance: c.Instance, Window: c.Window}
	case "restore-drill":
		if !nameRE.MatchString(c.Drill) || r.Drill != "" || !r.valid() || r.Status != "remote-readback-verified" || r.Instance != c.Instance || r.Window != c.Window || !filepath.IsAbs(c.Identity) {
			return Receipt{}, errors.New("restore requires a drill ID, verified nightly receipt and custodian identity file")
		}
	default:
		return Receipt{}, errors.New("unknown workflow")
	}
	if err := privateDir(c.StateDir); err != nil {
		return Receipt{}, err
	}
	// Exclude concurrent local side effects, including cleanup, across invocations.
	lock, err := os.Open(c.StateDir)
	if err != nil {
		return Receipt{}, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return Receipt{}, fmt.Errorf("backup worker lock: %w", err)
	}
	s, err := ephemeral.Open(filepath.Join(c.StateDir, "workflows.db"))
	if err != nil {
		return Receipt{}, err
	}
	defer s.Close()
	w := worker{Config: c, lock: lock, lease: 30 * time.Second}
	if err = w.register(s, r); err != nil {
		return Receipt{}, err
	}
	// Revisit interrupted earlier windows with this configuration on the next invocation.
	var older error
	for _, state := range []string{"ready", "pending", "leased", "timer"} {
		after := ""
		for {
			ids, e := s.List(state, after, 100)
			if e != nil {
				return Receipt{}, e
			}
			for _, id := range ids {
				after = id
				d, e := s.Describe(id)
				if e != nil {
					return Receipt{}, e
				}
				if id == w.id() || d.Attributes["binding"] != w.binding() {
					continue
				}
				prev := w
				prev.Window = d.Attributes["window"]
				prev.Drill = d.Attributes["drill"]
				if !nameRE.MatchString(prev.Window) || prev.id() != id {
					return Receipt{}, errors.New("invalid stored window")
				}
				_, e = prev.drive(ctx, s)
				older = errors.Join(older, e)
				if ctx.Err() != nil {
					return Receipt{}, ctx.Err()
				}
			}
			if len(ids) < 100 {
				break
			}
		}
	}
	out, err := w.drive(ctx, s)
	return out, errors.Join(older, err)
}
func (w worker) drive(ctx context.Context, s *ephemeral.Store) (Receipt, error) {
	w.dir = filepath.Join(w.StateDir, w.id())
	for ctx.Err() == nil {
		d, err := s.Describe(w.id())
		if err != nil {
			return Receipt{}, err
		}
		switch d.State {
		case "completed":
			var r Receipt
			err = json.Unmarshal(d.Value, &r)
			return r, err
		case "failed", "cancelled":
			return Receipt{}, fmt.Errorf("%s is %s", w.id(), d.State)
		}
		t, err := s.Poll(w.id(), w.lease)
		if err != nil {
			return Receipt{}, err
		}
		if t == nil {
			select {
			case <-ctx.Done():
				return Receipt{}, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		if err = w.execute(ctx, s, t); err != nil {
			return Receipt{}, err
		}
	}
	return Receipt{}, ctx.Err()
}
func (w worker) execute(ctx context.Context, s *ephemeral.Store, t *ephemeral.Task) error {
	activity, cancel := context.WithTimeout(ctx, 30*time.Minute)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(w.lease / 3)
		defer tick.Stop()
		for {
			select {
			case <-activity.Done():
				return
			case <-tick.C:
				if s.Heartbeat(t.Run, t.Token, w.lease) != nil {
					cancel()
					return
				}
			}
		}
	}()
	var r Receipt
	err := json.Unmarshal(t.Input, &r)
	if err == nil {
		err = w.action(activity, t.Activity, &r)
	}
	cancel()
	<-stopped
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// Only the activity name enters durable history. Worker errors remain local.
		if e := s.Finish(t.Run, t.Token, nil, t.Activity+" failed"); e != nil {
			return errors.Join(err, e)
		}
		return fmt.Errorf("%s: %w", t.Activity, err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.Finish(t.Run, t.Token, b, "")
}
