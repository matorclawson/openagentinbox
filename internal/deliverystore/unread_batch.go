package deliverystore

import (
	"context"
	"encoding/json"
	"time"

	dost "github.com/rodriveracom/dostoevsky"
)

// UnreadReader binds one authenticated recipient to its sparse SQLite sequence.
type UnreadReader struct {
	Store *Store
	Seat  string
}

var _ dost.BatchReader = UnreadReader{}

const MaxUnreadBatchBytes = 2 << 20

// Available returns a body-free page and the Messenger continuation flag.
// The byte budget includes the record key, ID and JSON value per Dostoevsky.
func (r UnreadReader) Available(ctx context.Context, after int64, l dost.Limits) (dost.Page, bool, error) {
	if r.Seat == "" || after < 0 || l.Records < 1 || l.Records > 50 || l.Bytes < 1 || l.Bytes > MaxUnreadBatchBytes {
		return dost.Page{}, false, dost.ErrInvalid
	}
	q, err := r.Store.unreadRows(ctx, r.Seat, after, l.Records)
	if err != nil {
		return dost.Page{}, false, err
	}
	defer q.Close()
	page := dost.Page{Next: after}
	for q.Next() {
		if len(page.Records) == l.Records {
			return page, true, nil
		}
		row, err := scanUnread(q)
		if err != nil {
			return dost.Page{}, false, err
		}
		value, err := json.Marshal(row)
		if err != nil {
			return dost.Page{}, false, err
		}
		size := len(r.Seat) + len(row.ID) + len(value)
		if size > l.Bytes {
			if len(page.Records) == 0 {
				return page, true, dost.ErrLimit
			}
			return page, true, nil
		}
		l.Bytes -= size
		page.Records = append(page.Records, dost.Record{Partition: dost.Partition{Topic: "unread"}, Offset: row.Seq, Time: time.UnixMilli(row.CreatedMilli), Message: dost.Message{Key: r.Seat, ID: row.ID, Value: value}})
		page.Next = row.Seq
	}
	return page, false, q.Err()
}

// ReadBatch supports durable consumers with exclusive-after cursors. A hint
// prompts another SQLite read because another consumer may already have noticed mail.
func (r UnreadReader) ReadBatch(ctx context.Context, p dost.Partition, after int64, l dost.Limits, wait bool) (dost.Page, error) {
	if p != (dost.Partition{Topic: "unread"}) {
		return dost.Page{}, dost.ErrInvalid
	}
	var watch *Subscription
	if wait {
		var err error
		watch, err = r.Store.WatchSeat(r.Seat, false)
		if err != nil {
			return dost.Page{}, err
		}
		defer watch.Close()
	}
	for {
		page, _, err := r.Available(ctx, after, l)
		if err != nil || len(page.Records) > 0 {
			return page, err
		}
		if !wait {
			return page, dost.ErrEmpty
		}
		select {
		case <-ctx.Done():
			return page, ctx.Err()
		case <-watch.Changes():
			if err := watch.Rearm(); err != nil {
				return page, err
			}
		}
	}
}
