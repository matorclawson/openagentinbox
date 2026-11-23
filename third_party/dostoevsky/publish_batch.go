package dostoevsky

import (
	"bytes"
	"context"
	"time"
)

// Publication addresses a single message in a local owner batch.
type Publication struct {
	Topic   string
	Message Message
}

// PublishBatch amortizes sync over an explicit burst, preserving input order.
// A write failure can leave a recoverable prefix. Retry with stable IDs.
func (n *Node) PublishBatch(ctx context.Context, items []Publication) ([]Record, error) {
	if len(items) == 0 {
		return nil, ErrInvalid
	}
	if len(items) > 1024 {
		return nil, ErrLimit
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, ErrClosed
	}
	if n.j.failed != nil {
		return nil, n.j.failed
	}
	records := make([]Record, len(items))
	type staged struct {
		index int
		loc   location
	}
	type identity struct {
		Partition
		id string
	}
	seen := map[identity]int{}
	counts := map[Partition]int64{}
	var writes []staged
	var frames []byte
	var inputBytes int
	for i, item := range items {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		m := item.Message
		if len(m.Value) > MaxValue || len(m.Key) > 4096 || len(m.ID) > 256 {
			return nil, ErrLimit
		}
		inputBytes += len(m.Value) + len(m.Key) + len(m.ID) + len(item.Topic)
		if inputBytes > maxFrame {
			return nil, ErrLimit
		}
		if !validText(m.Key, m.ID) {
			return nil, ErrInvalid
		}
		p, e := n.partition(item.Topic, m.Key)
		if e != nil {
			return nil, e
		}
		owner, e := n.owner(p)
		if e != nil {
			return nil, e
		}
		if owner != n.cfg.Node {
			return nil, ErrOwner
		}
		s := n.streams[p]
		var base int64
		if s != nil {
			base = s.base + int64(len(s.records))
		}
		if m.ID != "" {
			k := identity{p, m.ID}
			var prior *Record
			if j, ok := seen[k]; ok {
				prior = &records[j]
			} else if s != nil {
				if off, ok := s.lookupID(m.ID); ok {
					ev, e := n.j.read(s.records[off-s.base])
					if e != nil {
						return nil, e
					}
					prior = ev.Record
				}
			}
			if prior != nil {
				if prior.Key != m.Key || !bytes.Equal(prior.Value, m.Value) {
					return nil, ErrConflict
				}
				records[i] = *prior
				continue
			}
			seen[k] = i
		}
		records[i] = Record{Partition: p, Message: m, Offset: base + counts[p], Time: time.Now().UTC()}
		counts[p]++
		frame, e := encodeFrame(entry{Record: &records[i]})
		if e != nil {
			return nil, e
		}
		if len(frames)+len(frame) > maxFrame {
			return nil, ErrLimit
		}
		writes = append(writes, staged{i, location{n.j.end + int64(len(frames)) + 20, len(frame) - 20}})
		frames = append(frames, frame...)
	}
	if len(writes) > 0 {
		if e := n.j.writeFrames(frames); e != nil {
			return nil, e
		}
		for _, w := range writes {
			if e := n.index(entry{Record: &records[w.index]}, w.loc); e != nil {
				return nil, e
			}
		}
	}
	return records, nil
}
