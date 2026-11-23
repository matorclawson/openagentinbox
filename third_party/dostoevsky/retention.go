package dostoevsky

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type retentionSeed struct {
	Partition
	Base   int64
	Groups map[string]Position
}

// Retain removes records before each supplied offset. All existing groups must
// have committed at or beyond that offset. IDs belonging to removed records
// expire. Readers below the new floor receive ErrExpired.
func (n *Node) Retain(ctx context.Context, floors map[Partition]int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return ErrClosed
	}
	if n.j.failed != nil {
		return n.j.failed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for p, floor := range floors {
		owner, err := n.owner(p)
		if err != nil {
			return err
		}
		if owner != n.cfg.Node {
			return ErrOwner
		}
		s := n.streams[p]
		if s == nil {
			if floor != 0 {
				return ErrInvalid
			}
			continue
		}
		if floor < s.base || floor > s.base+s.count() {
			return ErrInvalid
		}
		for _, pos := range s.groups {
			if pos.Next < floor {
				return fmt.Errorf("partition %v has a lagging consumer: %w", p, ErrConflict)
			}
		}
	}
	dir := filepath.Dir(n.j.file.Name())
	f, err := os.CreateTemp(dir, ".retention-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	installed := false
	defer func() {
		if !installed {
			f.Close()
			os.Remove(temp)
		}
	}()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	replacement := &journal{file: f}
	appendFrame := func(ev entry) (location, error) {
		frame, e := encodeFrame(ev)
		if e != nil {
			return location{}, e
		}
		loc := location{at: replacement.end + 20, size: len(frame) - 20}
		written, e := f.WriteAt(frame, replacement.end)
		if e != nil {
			return location{}, e
		}
		if written != len(frame) {
			return location{}, io.ErrShortWrite
		}
		replacement.end += int64(written)
		return loc, nil
	}
	if _, err = appendFrame(entry{Config: &n.cfg}); err != nil {
		return err
	}
	rebuilt := make(map[Partition]*stream, len(n.streams))
	for p, s := range n.streams {
		if err = ctx.Err(); err != nil {
			return err
		}
		floor := s.base
		if requested, ok := floors[p]; ok {
			floor = requested
		}
		if floor > 0 || len(s.groups) > 0 {
			if _, err = appendFrame(entry{Seed: &retentionSeed{Partition: p, Base: floor, Groups: s.groups}}); err != nil {
				return err
			}
		}
		updated := &stream{base: floor, groups: s.groups, wait: s.wait}
		rebuilt[p] = updated
		for i := floor - s.base; i < s.count(); i++ {
			if err = ctx.Err(); err != nil {
				return err
			}
			ev, e := n.j.read(s.location(i))
			if e != nil {
				return e
			}
			loc, e := appendFrame(ev)
			if e != nil {
				return e
			}
			updated.addRecord(ev.Record, loc)
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temp, n.j.file.Name()); err != nil {
		return err
	}
	installed = true
	old := n.j
	replacement.lock = old.lock
	n.j = replacement
	for p, s := range n.streams {
		*s = *rebuilt[p]
		s.signal()
	}
	closeErr := old.file.Close()
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	syncErr := d.Sync()
	d.Close()
	return errors.Join(closeErr, syncErr, n.j.failed)
}
