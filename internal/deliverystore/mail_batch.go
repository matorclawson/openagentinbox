package deliverystore

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

type mailWrite struct {
	ctx      context.Context
	key      string
	msg      Message
	at       time.Time
	record   func(*sql.Tx) error
	id       string
	err      error
	inserted bool
	done     chan struct{}
}

type mailWriteQueue struct {
	mu      sync.Mutex
	running bool
	pending []*mailWrite
	bytes   int
}

// AcceptMail shares a commit only when sends already overlap. The first send
// starts immediately. Overflow uses the ordinary serialized SQLite writer.
func (s *Store) AcceptMail(ctx context.Context, key string, msg Message, at time.Time, record func(*sql.Tx) error) (string, error) {
	q := &s.mailWrites
	q.mu.Lock()
	if !q.running {
		q.running = true
		q.mu.Unlock()
		id, err := s.AcceptWithReceipt(ctx, key, msg, at, record)
		if batch := q.next(); len(batch) > 0 {
			go s.drainMail(batch)
		}
		return id, err
	}
	n := len(msg.Body)
	if len(q.pending) >= 32 || q.bytes+n > 1<<20 {
		q.mu.Unlock()
		return s.AcceptWithReceipt(ctx, key, msg, at, record)
	}
	w := &mailWrite{ctx: ctx, key: key, msg: msg, at: at, record: record, done: make(chan struct{})}
	q.pending = append(q.pending, w)
	q.bytes += n
	q.mu.Unlock()
	// Hold the caller's callback and its inputs until the writer has finished.
	<-w.done
	return w.id, w.err
}
func (q *mailWriteQueue) next() []*mailWrite {
	q.mu.Lock()
	defer q.mu.Unlock()
	batch := q.pending
	q.pending = nil
	q.bytes = 0
	q.running = len(batch) > 0
	return batch
}
func (s *Store) drainMail(batch []*mailWrite) {
	for len(batch) > 0 {
		if len(batch) == 1 {
			w := batch[0]
			w.id, w.err = s.AcceptWithReceipt(w.ctx, w.key, w.msg, w.at, w.record)
		} else {
			err := s.commitMail(batch)
			for _, w := range batch {
				if err != nil {
					w.id = ""
					w.err = err
				} else if w.err == nil && w.inserted {
					s.signal(w.msg.Recipients)
				}
			}
		}
		for _, w := range batch {
			close(w.done)
		}
		batch = s.mailWrites.next()
	}
}
func (s *Store) commitMail(batch []*mailWrite) error {
	// The transaction lifetime is independent of any one client disconnect.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range batch {
		if w.err = w.ctx.Err(); w.err != nil {
			continue
		}
		if _, err = tx.Exec("SAVEPOINT mail_item"); err != nil {
			return err
		}
		w.id, w.inserted, w.err = s.acceptTx(w.ctx, tx, w.key, w.msg, w.at, nil)
		if w.err == nil && w.inserted && w.record != nil {
			w.err = w.record(tx)
		}
		if w.err != nil {
			w.id = ""
			if _, err = tx.Exec("ROLLBACK TO mail_item"); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("RELEASE mail_item"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
