package console

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// Keep a consistent, private recovery copy of the former database, including
// committed WAL pages, before any native schema change. The old service must
// be stopped before launching an upgrade.
func backupLegacy(path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw&_pragma=busy_timeout(5000)"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	var n int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='letters' AND type='table'").Scan(&n); e != nil || n == 0 {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), "pre-native-*.sqlite")
	if e != nil {
		return e
	}
	name := f.Name()
	done := false
	defer func() {
		if !done {
			os.Remove(name)
		}
	}()
	if e = f.Close(); e != nil {
		return e
	}
	if _, e = db.Exec("VACUUM INTO ?", name); e != nil {
		return fmt.Errorf("pre-upgrade snapshot: %w", e)
	}
	checkURL := url.URL{Scheme: "file", Path: name, RawQuery: "mode=ro"}
	check, e := sql.Open("sqlite", checkURL.String())
	if e != nil {
		return e
	}
	defer check.Close()
	var result string
	if e = check.QueryRow("PRAGMA integrity_check").Scan(&result); e != nil {
		return e
	}
	if result != "ok" {
		return errors.New("invalid pre-upgrade snapshot")
	}
	f, e = os.OpenFile(name, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	e = f.Sync()
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		return e
	}
	done = true
	return nil
}
