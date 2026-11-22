package dostoevsky

import (
	"context"
	"encoding/json"
	"fmt"
)

// Store is implemented by both Node and Mesh.
type Publisher interface {
	Publish(context.Context, string, Message) (Record, error)
}
type Reader interface {
	Read(context.Context, Partition, int64, bool) (Record, error)
}
type Checkpointer interface {
	Position(context.Context, Partition, string) (Position, error)
	Commit(context.Context, Partition, string, int64, []byte) error
}

type Store interface {
	Publisher
	Reader
	Checkpointer
}

// Fold processes a partition until cancellation or an error. Each successful
// reduction commits its state with the input offset. Use a pure reducer. It may
// run again after a crash or a competing consumer's commit.
func Fold[S any](ctx context.Context, s interface {
	Reader
	Checkpointer
}, p Partition, group string, initial S, reduce func(context.Context, S, Record) (S, error)) error {
	pos, e := s.Position(ctx, p, group)
	if e != nil {
		return e
	}
	state := initial
	if pos.Next > 0 {
		var saved S
		if e = json.Unmarshal(pos.State, &saved); e != nil {
			return fmt.Errorf("decode %s state: %w", group, e)
		}
		state = saved
	}
	for {
		r, e := s.Read(ctx, p, pos.Next, true)
		if e != nil {
			return e
		}
		next, e := reduce(ctx, state, r)
		if e != nil {
			return e
		}
		data, e := json.Marshal(next)
		if e != nil {
			return e
		}
		if e = s.Commit(ctx, p, group, pos.Next, data); e != nil {
			return e
		}
		state = next
		pos.Next++
	}
}

// Pipe transforms one input record into one output record, then checkpoints
// the input. Its deterministic output ID makes replay after a lost response
// safe even when input and output belong to different owners. The transform
// must produce identical keys and values on every replay.
func Pipe(ctx context.Context, s Store, from Partition, group, to string, transform func(context.Context, Record) (string, []byte, error)) error {
	if from.Topic == to {
		return fmt.Errorf("%w: pipe requires distinct topics", ErrInvalid)
	}
	pos, e := s.Position(ctx, from, group)
	if e != nil {
		return e
	}
	for {
		r, e := s.Read(ctx, from, pos.Next, true)
		if e != nil {
			return e
		}
		key, value, e := transform(ctx, r)
		if e != nil {
			return e
		}
		id := fmt.Sprintf("pipe/%s/%s/%d/%d", group, from.Topic, from.Number, r.Offset)
		if _, e = s.Publish(ctx, to, Message{Key: key, Value: value, ID: id}); e != nil {
			return e
		}
		if e = s.Commit(ctx, from, group, r.Offset, nil); e != nil {
			return e
		}
		pos.Next++
	}
}
