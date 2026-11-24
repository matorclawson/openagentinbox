package dostoevsky

import (
	"bytes"
	"context"
	"sync"
)

// One isolated caller writes inline. Only callers that arrive during a write
// enqueue. Overflow falls back to the ordinary node lock for backpressure.
const (
	maxQueuedPublishes = 64
	maxQueuedBytes     = 1 << 20
)

type queuedPublish struct {
	ctx       context.Context
	partition Partition
	msg       Message
	result    Record
	err       error
	done      chan struct{}
}
type publishQueue struct {
	mu      sync.Mutex
	active  bool
	pending []*queuedPublish
	bytes   int
}

func (n *Node) schedulePublish(ctx context.Context, p Partition, m Message) (Record, error) {
	// Bypass queue bookkeeping when the node is idle.
	if n.mu.TryLock() {
		defer n.mu.Unlock()
		return n.publishLocked(ctx, p, m)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	q := &n.publishes
	q.mu.Lock()
	if !q.active {
		q.active = true
		q.mu.Unlock()
		n.mu.Lock()
		r, err := n.publishLocked(ctx, p, m)
		n.mu.Unlock()
		if batch := q.take(); len(batch) > 0 {
			go n.drainPublishes(batch)
		}
		return r, err
	}
	size := len(p.Topic) + len(m.ID) + len(m.Key) + len(m.Value)
	if len(q.pending) >= maxQueuedPublishes || size > maxQueuedBytes-q.bytes {
		q.mu.Unlock()
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.publishLocked(ctx, p, m)
	}
	w := &queuedPublish{ctx: ctx, partition: p, msg: m, done: make(chan struct{})}
	q.pending = append(q.pending, w)
	q.bytes += size
	q.mu.Unlock()
	<-w.done
	return w.result, w.err
}
func (q *publishQueue) take() []*queuedPublish {
	q.mu.Lock()
	defer q.mu.Unlock()
	batch := q.pending
	q.pending = nil
	q.bytes = 0
	q.active = len(batch) > 0
	return batch
}
func (n *Node) drainPublishes(batch []*queuedPublish) {
	for len(batch) > 0 {
		n.mu.Lock()
		n.writeQueued(batch)
		n.mu.Unlock()
		for _, w := range batch {
			close(w.done)
		}
		batch = n.publishes.take()
	}
}
func (n *Node) writeQueued(batch []*queuedPublish) {
	if len(batch) > 1 {
		items := make([]Publication, 0, len(batch))
		for _, w := range batch {
			w.err = w.ctx.Err()
			if w.err == nil {
				items = append(items, Publication{Topic: w.partition.Topic, Message: w.msg})
			}
		}
		if len(items) == 0 {
			return
		}
		records, err := n.publishBatchLocked(context.Background(), items)
		switch err {
		case ErrOwner, ErrConflict, ErrInvalid, ErrLimit:
			// Batch validation precedes I/O. Resolve these calls independently so one
			// conflicting retry cannot reject another caller's valid publication.
		default:
			i := 0
			for _, w := range batch {
				if w.err != nil {
					continue
				}
				w.err = err
				if err == nil {
					w.result = records[i]
					// A deduplicated record may refer to another caller's input buffer.
					if w.msg.ID != "" {
						w.result.Value = bytes.Clone(w.result.Value)
					}
				}
				i++
			}
			return
		}
	}
	for _, w := range batch {
		w.result, w.err = n.publishLocked(w.ctx, w.partition, w.msg)
	}
}
