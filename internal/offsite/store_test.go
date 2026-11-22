package offsite

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type memory struct {
	data       []byte
	truncate   bool
	corruption string
}

func (m *memory) Put(_ context.Context, _ string, r io.Reader, _ int64) error {
	if m.truncate {
		return nil
	}
	b, e := io.ReadAll(r)
	m.data = b
	return e
}
func (m *memory) Get(context.Context, string) (io.ReadCloser, error) {
	b := m.data
	if m.corruption != "" {
		b = []byte(m.corruption)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func TestVerified(t *testing.T) {
	for _, c := range []struct {
		m    memory
		size int64
		ok   bool
	}{{memory{}, 3, true}, {memory{truncate: true}, 3, false}, {memory{corruption: "abd"}, 3, false}, {memory{corruption: "abcd"}, 3, false}, {memory{corruption: "ab"}, 3, false}, {memory{}, 2, false}, {memory{}, 4, false}} {
		rec, e := PutVerified(context.Background(), &c.m, "nightly", bytes.NewBufferString("abc"), c.size)
		if (e == nil) != c.ok || (c.ok && rec.Bytes != 3) {
			t.Fatal(c, rec, e)
		}
	}
}
