// Package sqlitebackup creates and verifies consistent SQLite snapshots without loading the database into Go memory.
package sqlitebackup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	_ "modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Manifest struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Messages   int64  `json:"messages"`
	Deliveries int64  `json:"deliveries"`
}

func open(path string) (*sql.DB, error) {
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "cache_size(-4096)", "mmap_size(0)", "temp_store(FILE)", "synchronous(FULL)"}}
	u := url.URL{Scheme: "file", Opaque: (&url.URL{Path: path}).EscapedPath(), RawQuery: q.Encode()}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Create publishes only after integrity checks, hash and fsync. Existing destinations are never overwritten.
// Source must be a local SQLite database, not a network filesystem. Caller supplies a private destination directory.
func Create(ctx context.Context, source, destination string) (Manifest, error) {
	return create(ctx, source, destination, Verify)
}

// Snapshot is Create for any SQLite store, including one that predates the current schema.
// It checks integrity and records size and hash, but not message counts, so an upgrade can
// keep a restorable copy of whatever version is running. Counts in the Manifest are zero.
func Snapshot(ctx context.Context, source, destination string) (Manifest, error) {
	return create(ctx, source, destination, integrity)
}
func integrity(ctx context.Context, path string) (m Manifest, err error) {
	db, err := open(path)
	if err != nil {
		return m, err
	}
	defer db.Close()
	var result string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return m, err
	}
	if result != "ok" {
		return m, fmt.Errorf("integrity check: %s", result)
	}
	return fileManifest(path)
}
func fileManifest(path string) (m Manifest, err error) {
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	h := sha256.New()
	m.Bytes, err = io.CopyBuffer(h, f, make([]byte, 64<<10))
	if err != nil {
		return m, err
	}
	m.Path = path
	m.SHA256 = hex.EncodeToString(h.Sum(nil))
	return m, nil
}
func create(ctx context.Context, source, destination string, check func(context.Context, string) (Manifest, error)) (m Manifest, err error) {
	source, err = filepath.Abs(source)
	if err != nil {
		return m, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return m, err
	}
	if source == destination {
		return m, errors.New("source equals destination")
	}
	fi, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		return m, err
	}
	if !fi.IsDir() || fi.Mode().Perm()&0077 != 0 {
		return m, errors.New("backup directory must be private (0700)")
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return m, errors.New("destination exists or cannot be inspected")
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".snapshot-*")
	if err != nil {
		return m, err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	db, err := open(source)
	if err != nil {
		return m, err
	}
	_, err = db.ExecContext(ctx, `VACUUM INTO ?`, tmp)
	closeErr := db.Close()
	if err != nil {
		return m, err
	}
	if closeErr != nil {
		return m, closeErr
	}
	m, err = check(ctx, tmp)
	if err != nil {
		return m, err
	}
	f, err = os.OpenFile(tmp, os.O_RDWR, 0600)
	if err != nil {
		return m, err
	}
	err = f.Sync()
	closeErr = f.Close()
	if err != nil {
		return m, err
	}
	if closeErr != nil {
		return m, closeErr
	}
	// Link is an atomic no-replace publication on the same filesystem.
	if err = os.Link(tmp, destination); err != nil {
		return m, err
	}
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return m, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return m, err
	}
	m.Path = destination
	return m, nil
}
func Verify(ctx context.Context, path string) (m Manifest, err error) {
	db, err := open(path)
	if err != nil {
		return m, err
	}
	defer db.Close()
	var result string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return m, err
	}
	if result != "ok" {
		return m, fmt.Errorf("integrity check: %s", result)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return m, err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if bad {
		return m, errors.New("foreign key check failed")
	}
	for _, table := range []string{"messages", "deliveries", "seats", "operations", "meta", "mailbox_counts", "delivery_outbox", "mailbox_events"} {
		var n int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			return m, err
		}
		if n != 1 {
			return m, fmt.Errorf("missing table %s", table)
		}
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&m.Messages); err != nil {
		return m, err
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM deliveries`).Scan(&m.Deliveries); err != nil {
		return m, err
	}
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	h := sha256.New()
	m.Bytes, err = io.CopyBuffer(h, f, make([]byte, 64<<10))
	if err != nil {
		return m, err
	}
	m.Path = path
	m.SHA256 = hex.EncodeToString(h.Sum(nil))
	return m, nil
}

// Upload streams a verified snapshot via a caller-supplied HTTPS PUT URL. No redirect or unbounded response body is accepted.
// Success means the remote HTTP endpoint accepted the upload; periodic remote download/restore remains necessary.
func Upload(ctx context.Context, m Manifest, target string) error {
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("upload needs an HTTPS URL")
	}
	f, e := os.Open(m.Path)
	if e != nil {
		return e
	}
	defer f.Close()
	fi, e := f.Stat()
	if e != nil {
		return e
	}
	if fi.Size() != m.Bytes {
		return errors.New("snapshot size changed")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPut, target, f)
	if e != nil {
		return e
	}
	req.ContentLength = m.Bytes
	req.Header.Set("Content-Type", "application/vnd.sqlite3")
	req.Header.Set("X-Content-SHA256", m.SHA256)
	client := http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return errors.New("remote upload failed")
	}
	defer res.Body.Close()
	io.CopyN(io.Discard, res.Body, 4096)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("remote upload status %d", res.StatusCode)
	}
	return nil
}
