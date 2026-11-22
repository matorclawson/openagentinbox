package dostoevsky

import "context"

type Limits struct{ Records, Bytes int }
type Page struct {
	Records []Record
	Next    int64
}
type BatchReader interface {
	ReadBatch(context.Context, Partition, int64, Limits, bool) (Page, error)
}
type BatchCheckpointer interface {
	Position(context.Context, Partition, string) (Position, error)
	CommitBatch(context.Context, Partition, string, int64, int64, []byte) error
}

// ReadBatch bounds record count and combined key, ID and value bytes. ErrLimit
// on an oversized first record leaves the cursor available for a larger retry.
func (n *Node) ReadBatch(ctx context.Context, p Partition, start int64, l Limits, wait bool) (Page, error) {
	if l.Records < 1 || l.Bytes < 1 {
		return Page{}, ErrInvalid
	}
	page := Page{Next: start}
	for len(page.Records) < l.Records {
		r, e := n.Read(ctx, p, page.Next, wait && len(page.Records) == 0)
		if e == ErrEmpty && len(page.Records) > 0 {
			break
		}
		if e != nil {
			return Page{}, e
		}
		size := len(r.Key) + len(r.ID) + len(r.Value)
		if size > l.Bytes {
			if len(page.Records) == 0 {
				return Page{}, ErrLimit
			}
			break
		}
		page.Records = append(page.Records, r)
		page.Next++
		l.Bytes -= size
	}
	return page, nil
}

// ConsumePages waits for pages and invokes apply once per page. The callback
// owns the transaction that writes its output and advances the expected cursor.
// On failure restart from the durable cursor. Source cursor values may be sparse.
func ConsumePages(ctx context.Context, r BatchReader, p Partition, start int64, l Limits, apply func(context.Context, int64, Page) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, e := r.ReadBatch(ctx, p, start, l, true)
		if e != nil {
			return e
		}
		if page.Next <= start || len(page.Records) == 0 {
			return ErrInvalid
		}
		if e = apply(ctx, start, page); e != nil {
			return e
		}
		start = page.Next
	}
}
