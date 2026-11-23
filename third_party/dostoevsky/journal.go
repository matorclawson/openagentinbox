package dostoevsky

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const maxFrame = 4 << 20

var crcTable = crc32.MakeTable(crc32.Castagnoli)
var signature = []byte("DOST0001")

type entry struct {
	Config *Config        `json:",omitempty"`
	Record *Record        `json:",omitempty"`
	Seed   *retentionSeed `json:",omitempty"`
	Check  *checkpoint    `json:",omitempty"`
}
type location struct {
	at   int64
	size int
}
type journal struct {
	file   *os.File
	lock   *os.File
	end    int64
	failed error
}

// One file descriptor owns the advisory lock for the lifetime of the node.
func openJournal(dir string) (*journal, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "journal.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "journal.v1"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		lock.Close()
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		d.Close()
	}
	if err != nil {
		f.Close()
		lock.Close()
		return nil, err
	}
	return &journal{file: f, lock: lock}, nil
}

func (j *journal) append(e entry) (location, error) {
	var loc location
	if j.failed != nil {
		return loc, j.failed
	}
	frame, err := encodeFrame(e)
	if err != nil {
		return loc, err
	}
	loc = location{j.end + 20, len(frame) - 20}
	return loc, j.writeFrames(frame)
}
func encodeFrame(e entry) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(b) > maxFrame {
		return nil, ErrLimit
	}
	h := make([]byte, 20, 20+len(b))
	copy(h, signature)
	binary.BigEndian.PutUint32(h[8:], uint32(len(b)))
	binary.BigEndian.PutUint32(h[12:], crc32.Checksum(b, crcTable))
	binary.BigEndian.PutUint32(h[16:], crc32.Checksum(h[:16], crcTable))
	return append(h, b...), nil
}
func (j *journal) writeFrames(frames []byte) error {
	if j.failed != nil {
		return j.failed
	}
	_, err := j.file.WriteAt(frames, j.end)
	if err == nil {
		err = j.file.Sync()
	}
	if err != nil {
		j.failed = fmt.Errorf("journal write requires reopen: %w", err)
		return j.failed
	}
	j.end += int64(len(frames))
	return nil
}
func (j *journal) read(loc location) (entry, error) {
	var e entry
	b := make([]byte, loc.size+20)
	if _, err := j.file.ReadAt(b, loc.at-20); err != nil {
		return e, err
	}
	if !validHeader(b[:20]) || int(binary.BigEndian.Uint32(b[8:])) != loc.size || binary.BigEndian.Uint32(b[12:]) != crc32.Checksum(b[20:], crcTable) {
		return e, errors.New("journal checksum mismatch")
	}
	err := json.Unmarshal(b[20:], &e)
	return e, err
}
func validHeader(h []byte) bool {
	return bytes.Equal(h[:8], signature) && binary.BigEndian.Uint32(h[16:]) == crc32.Checksum(h[:16], crcTable)
}
func (j *journal) recover(apply func(entry, location) error) error {
	info, err := j.file.Stat()
	if err != nil {
		return err
	}
	for j.end < info.Size() {
		var h [20]byte
		_, err = j.file.ReadAt(h[:], j.end)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !validHeader(h[:]) {
			return fmt.Errorf("invalid journal header at %d", j.end)
		}
		size := int(binary.BigEndian.Uint32(h[8:]))
		if size > maxFrame || size <= 0 {
			return fmt.Errorf("invalid frame length at %d", j.end)
		}
		loc := location{j.end + 20, size}
		next := loc.at + int64(size)
		if next > info.Size() {
			break
		}
		e, err := j.read(loc)
		if err != nil {
			return fmt.Errorf("frame at %d: %w", j.end, err)
		}
		if err = apply(e, loc); err != nil {
			return fmt.Errorf("replay at %d: %w", j.end, err)
		}
		j.end = next
	}
	if j.end != info.Size() {
		if err = j.file.Truncate(j.end); err != nil {
			return err
		}
		return j.file.Sync()
	}
	return nil
}

func (j *journal) close() error { return errors.Join(j.file.Close(), j.lock.Close()) }
