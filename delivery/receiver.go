package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var ErrFence = errors.New("current Runtime assignment unavailable")
var ErrConflict = errors.New("delivery key holds another envelope")

type Binding struct {
	Assignment string
	Generation int64
}

// Authority holds Runtime's assignment fence while commit runs. recheck must
// validate expiry after local database lock waits. No network/provider effect
// belongs in this callback. Messenger cannot manufacture an Authority.
type Authority interface {
	WithCurrent(context.Context, string, func(Binding, func() error) error) error
}

// Inbox is a durable Runtime receipt journal, not another copy of message bodies.
// It proves receipt only; Runtime's lifecycle remains the sole provider executor.
type Inbox struct {
	db        *sql.DB
	authority Authority
}

func OpenInbox(path string, a Authority) (*Inbox, error) {
	if a == nil || !filepath.IsAbs(path) {
		return nil, ErrFence
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(filepath.Dir(path))
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0077 != 0 {
		return nil, errors.New("receipt directory must be private")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	fi, err = os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 {
		return nil, errors.New("receipt file must be private and regular")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE IF NOT EXISTS received(id TEXT PRIMARY KEY,envelope TEXT NOT NULL,receipt TEXT NOT NULL); CREATE INDEX IF NOT EXISTS received_seat ON received(json_extract(envelope,'$.seat'))`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Inbox{db: db, authority: a}, nil
}
func (i *Inbox) Close() error { return i.db.Close() }
func (i *Inbox) Receive(ctx context.Context, e Envelope) (r Receipt, err error) {
	if e.ID == "" || len(e.ID) > 256 || e.MessageID == "" || len(e.MessageID) > 256 || e.Seat == "" || len(e.Seat) > 128 {
		return r, errors.New("invalid envelope")
	}
	raw, _ := json.Marshal(e)
	err = i.authority.WithCurrent(ctx, e.Seat, func(b Binding, recheck func() error) error {
		if b.Assignment == "" || b.Generation < 1 || recheck == nil {
			return ErrFence
		}
		tx, x := i.db.BeginTx(ctx, nil)
		if x != nil {
			return x
		}
		defer tx.Rollback()
		// Take the write lock before the final authority check.
		if _, x = tx.ExecContext(ctx, `UPDATE received SET id=id WHERE id=?`, e.ID); x != nil {
			return x
		}
		if x = recheck(); x != nil {
			return x
		}
		var old, receipt string
		x = tx.QueryRowContext(ctx, `SELECT envelope,receipt FROM received WHERE id=?`, e.ID).Scan(&old, &receipt)
		if x == nil {
			if old != string(raw) {
				return ErrConflict
			}
			return json.Unmarshal([]byte(receipt), &r)
		}
		if x != sql.ErrNoRows {
			return x
		}
		r = Receipt{ID: e.ID, MessageID: e.MessageID, Seat: e.Seat, Assignment: b.Assignment, Generation: b.Generation, State: "received"}
		out, _ := json.Marshal(r)
		if _, x = tx.ExecContext(ctx, `INSERT INTO received VALUES(?,?,?)`, e.ID, string(raw), string(out)); x != nil {
			return x
		}
		if x = recheck(); x != nil {
			return x
		}
		return tx.Commit()
	})
	return
}

// Received is inspectable custody, not permission to inject a provider.
type Received struct {
	Sequence          int64    `json:"sequence"`
	Envelope          Envelope `json:"envelope"`
	Receipt           Receipt  `json:"receipt"`
	CurrentAssignment bool     `json:"current_assignment"`
}

// List is fenced and bounded. Rows received by a predecessor remain visible but
// explicitly non-current; only Runtime's existing reconciliation may carry work.
func (i *Inbox) List(ctx context.Context, seat string, after int64, limit int) (out []Received, err error) {
	out = []Received{}
	if after < 0 || limit < 1 || limit > 50 {
		return out, errors.New("invalid page")
	}
	err = i.authority.WithCurrent(ctx, seat, func(b Binding, recheck func() error) error {
		rows, e := i.db.QueryContext(ctx, `SELECT rowid,envelope,receipt FROM received WHERE json_extract(envelope,'$.seat')=? AND rowid>? ORDER BY rowid LIMIT ?`, seat, after, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r Received
			var envelope, receipt string
			if e = rows.Scan(&r.Sequence, &envelope, &receipt); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(envelope), &r.Envelope); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(receipt), &r.Receipt); e != nil {
				return e
			}
			r.CurrentAssignment = r.Receipt.Assignment == b.Assignment && r.Receipt.Generation == b.Generation
			out = append(out, r)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		return recheck()
	})
	return
}
