// Package offsite verifies ciphertext delivered over a Connect-provided transport.
package offsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Store intentionally grants only upload and readback operations.
type Store interface {
	Put(context.Context, string, io.Reader, int64) error
	Get(context.Context, string) (io.ReadCloser, error)
}

type Receipt struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// PutVerified streams an already encrypted object. A receipt requires the
// remote object to match every byte the uploader consumed.
func PutVerified(ctx context.Context, s Store, key string, encrypted io.Reader, size int64) (Receipt, error) {
	var zero Receipt
	if size <= 0 || key == "" {
		return zero, errors.New("missing object key or ciphertext length")
	}
	src := &io.LimitedReader{R: encrypted, N: size}
	sent := sha256.New()
	if e := s.Put(ctx, key, io.TeeReader(src, sent), size); e != nil {
		return zero, e
	}
	if src.N != 0 {
		return zero, errors.New("upload ended before consuming the ciphertext")
	}
	var extra [1]byte
	if n, e := io.ReadFull(encrypted, extra[:]); n != 0 || e != io.EOF {
		return zero, errors.New("ciphertext exceeds declared length or cannot be read")
	}
	remote, e := s.Get(ctx, key)
	if e != nil {
		return zero, e
	}
	got := sha256.New()
	n, e := io.CopyN(got, remote, size)
	var excess int
	if e == nil {
		excess, e = io.ReadFull(remote, extra[:])
		if e == io.EOF {
			e = nil
		}
	}
	closeErr := remote.Close()
	if e != nil || closeErr != nil {
		return zero, errors.Join(e, closeErr)
	}
	sha := hex.EncodeToString(sent.Sum(nil))
	if excess != 0 || n != size || hex.EncodeToString(got.Sum(nil)) != sha {
		return zero, errors.New("ciphertext readback mismatch")
	}
	return Receipt{key, n, sha}, nil
}
