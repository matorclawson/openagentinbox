package dostoevsky

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Backup writes a consistent journal snapshot into a new directory. The node
// remains locked during the copy. A failed copy leaves a partial destination.
func (n *Node) Backup(ctx context.Context, dir string) error {
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
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "journal.v1"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	buf := make([]byte, 128*1024)
	var offset int64
	for offset < n.j.end {
		if err = ctx.Err(); err != nil {
			break
		}
		size := int64(len(buf))
		if remain := n.j.end - offset; remain < size {
			size = remain
		}
		var count int
		count, err = n.j.file.ReadAt(buf[:size], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			break
		}
		if int64(count) != size {
			err = io.ErrUnexpectedEOF
			break
		}
		var written int
		written, err = f.Write(buf[:count])
		if err != nil {
			break
		}
		if written != count {
			err = io.ErrShortWrite
			break
		}
		offset += int64(count)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
