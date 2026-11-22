package backupflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/sqlitebackup"
)

func digest(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func remove(path string) error {
	e := os.Remove(path)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}

// Limited writer rejects excess bytes without growing the staging file.
type capped struct {
	w    io.Writer
	left int64
}

func (c *capped) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errors.New("object larger than receipt")
	}
	n, e := c.w.Write(p)
	c.left -= int64(n)
	return n, e
}
func (w worker) invoke(ctx context.Context, exe string, args []string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.WaitDelay = 5 * time.Second
	// A surviving helper retains the lock if the worker is killed.
	if w.lock != nil {
		cmd.ExtraFiles = []*os.File{w.lock}
	}
	cmd.Stdin = in
	cmd.Stdout = out
	return cmd.Run()
}
func (w worker) action(ctx context.Context, step string, r *Receipt) error {
	if r.Instance != w.Instance || r.Window != w.Window {
		return errors.New("workflow receipt mismatch")
	}
	if step == "cleanup" {
		if e := os.RemoveAll(w.dir); e != nil {
			return e
		}
		return syncDir(w.StateDir)
	}
	if e := privateDir(w.dir); e != nil {
		return e
	}
	if e := syncDir(w.StateDir); e != nil {
		return e
	}
	plain := filepath.Join(w.dir, "snapshot.db")
	cipher := filepath.Join(w.dir, "backup.age")
	switch step {
	case "snapshot":
		// sqlitebackup.Create can leave its private, unpublished temporary file on SIGKILL.
		leftovers, e := os.ReadDir(w.dir)
		if e != nil {
			return e
		}
		for _, item := range leftovers {
			if !strings.HasPrefix(item.Name(), ".snapshot-") {
				continue
			}
			if e = remove(filepath.Join(w.dir, item.Name())); e != nil {
				return e
			}
		}
		var m sqlitebackup.Manifest
		if _, err := os.Lstat(plain); os.IsNotExist(err) {
			m, e = sqlitebackup.Create(ctx, w.Source, plain)
		} else if err != nil {
			return err
		} else {
			m, e = sqlitebackup.Verify(ctx, plain)
		}
		if e != nil {
			return e
		}
		if e = syncDir(w.dir); e != nil {
			return e
		}
		r.SnapshotHash, r.SnapshotBytes, r.Messages, r.Deliveries = m.SHA256, m.Bytes, m.Messages, m.Deliveries
	case "encrypt":
		// Publish the seal receipt before discarding plaintext. If we crashed
		// between publishing ciphertext and its seal, encrypt the original again.
		marker := filepath.Join(w.dir, "seal.json")
		raw, e := os.ReadFile(marker)
		if os.IsNotExist(e) {
			h, n, e := digest(plain)
			if e != nil {
				return e
			}
			if h != r.SnapshotHash || n != r.SnapshotBytes {
				return errors.New("staged snapshot mismatch")
			}
			if e = w.encrypt(ctx, plain, cipher); e != nil {
				return e
			}
			r.CipherHash, r.CipherBytes, e = digest(cipher)
			if e != nil {
				return e
			}
			if !r.valid() {
				return errors.New("invalid ciphertext receipt")
			}
			raw, e = json.Marshal(r)
			if e != nil {
				return e
			}
			f, e := os.OpenFile(marker+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if e != nil {
				return e
			}
			_, e = f.Write(raw)
			if e == nil {
				e = f.Sync()
			}
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			if e = os.Rename(marker+".tmp", marker); e != nil {
				return e
			}
			if e = syncDir(w.dir); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			var saved Receipt
			if json.Unmarshal(raw, &saved) != nil || !saved.valid() {
				return errors.New("invalid seal receipt")
			}
			r.CipherHash, r.CipherBytes = saved.CipherHash, saved.CipherBytes
			if saved != *r {
				return errors.New("seal receipt belongs to a different snapshot")
			}
			h, n, e := digest(cipher)
			if e != nil {
				return e
			}
			if h != r.CipherHash || n != r.CipherBytes {
				return errors.New("sealed ciphertext changed")
			}
		}
		if e = remove(plain); e != nil {
			return e
		}
		return syncDir(w.dir)
	case "offsite":
		if !r.valid() {
			return errors.New("invalid upload receipt")
		}
		// get uses exit 44 exclusively for a missing object. Other failures are retryable.
		e := w.fetch(ctx, *r, io.Discard)
		var exit *exec.ExitError
		if errors.As(e, &exit) && exit.ExitCode() == 44 {
			h, n, e := digest(cipher)
			if e != nil {
				return e
			}
			if h != r.CipherHash || n != r.CipherBytes {
				return errors.New("staged ciphertext mismatch")
			}
			f, e := os.Open(cipher)
			if e != nil {
				return e
			}
			defer f.Close()
			if e = w.invoke(ctx, w.Adapter, []string{"put", r.key(), fmt.Sprint(n)}, f, io.Discard); e != nil {
				return e
			}
			if e = w.fetch(ctx, *r, io.Discard); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		r.Status = "remote-readback-verified"
	case "restore":
		if !r.valid() {
			return errors.New("invalid restore receipt")
		}
		// Rebuild disposable scratch after an interrupted drill. Only verified remote bytes reach age.
		defer remove(cipher)
		defer remove(plain)
		f, e := os.OpenFile(cipher, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
		if e != nil {
			return e
		}
		e = w.fetch(ctx, *r, f)
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		dst, e := os.OpenFile(plain, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
		if e != nil {
			return e
		}
		e = w.invoke(ctx, w.Age, []string{"--decrypt", "--identity", w.Identity, cipher}, nil, &capped{dst, r.SnapshotBytes})
		e = errors.Join(e, dst.Close())
		if e != nil {
			return e
		}
		m, e := sqlitebackup.Verify(ctx, plain)
		if e != nil {
			return e
		}
		if m.SHA256 != r.SnapshotHash || m.Bytes != r.SnapshotBytes || m.Messages != r.Messages || m.Deliveries != r.Deliveries {
			return errors.New("restored database differs from nightly receipt")
		}
		r.Status = "restore-verified"
		r.Drill = w.Drill
	default:
		return errors.New("unknown backup activity")
	}
	return nil
}
func (w worker) fetch(ctx context.Context, r Receipt, dest io.Writer) error {
	h := sha256.New()
	out := &capped{io.MultiWriter(dest, h), r.CipherBytes}
	if err := w.invoke(ctx, w.Adapter, []string{"get", r.key()}, nil, out); err != nil {
		return err
	}
	if out.left != 0 || hex.EncodeToString(h.Sum(nil)) != r.CipherHash {
		return errors.New("remote ciphertext mismatch")
	}
	return nil
}
func (w worker) encrypt(ctx context.Context, plain, dest string) error {
	temp := dest + ".tmp"
	f, e := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	defer remove(temp)
	e = w.invoke(ctx, w.Age, []string{"--encrypt", "--recipient", w.Recipient, plain}, nil, f)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(temp, dest); e != nil {
		return e
	}
	return syncDir(w.dir)
}
