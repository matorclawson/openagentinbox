package dostoevsky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()
var p = Partition{"words", 0}

func config() Config { return Config{"a", []string{"a"}, map[string]int{"words": 1}} }
func openTest(t *testing.T, dir string, c Config) *Node {
	t.Helper()
	n, e := Open(dir, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { n.Close() })
	return n
}
func publish(t *testing.T, s Store, id string) Record {
	t.Helper()
	r, e := s.Publish(ctx, "words", Message{Key: "key", Value: []byte("value"), ID: id})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestRecovery(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	if other, e := Open(dir, config()); e == nil {
		other.Close()
		t.Fatal("double writer")
	}
	a := publish(t, n, "1")
	if a.Offset != 0 {
		t.Fatal(a)
	}
	publish(t, n, "2")
	if e := n.Commit(ctx, p, "g", 0, []byte("state")); e != nil {
		t.Fatal(e)
	}
	n.Close()
	path := filepath.Join(dir, "journal.v1")
	before, _ := os.Stat(path)
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(signature); e != nil {
		t.Fatal(e)
	}
	f.Close()
	n = openTest(t, dir, config())
	after, _ := os.Stat(path)
	if after.Size() != before.Size() {
		t.Fatal("torn tail retained")
	}
	pos, e := n.Position(ctx, p, "g")
	if e != nil || pos.Next != 1 || string(pos.State) != "state" {
		t.Fatal(pos, e)
	}
	if r := publish(t, n, "1"); !a.Time.Equal(r.Time) || r.Offset != 0 {
		t.Fatal(r)
	}
	if _, e = n.Publish(ctx, "words", Message{ID: "1", Key: "key", Value: []byte("changed")}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if r := publish(t, n, "3"); r.Offset != 2 {
		t.Fatal(r)
	}
	pos, e = n.Position(ctx, p, "independent")
	if e != nil || pos.Next != 0 {
		t.Fatal(pos, e)
	}
	n.Close()
	c := config()
	c.Topics["words"] = 2
	if _, e = Open(dir, c); e == nil {
		t.Fatal("configuration change accepted")
	}
}
func TestIncompleteBodyAndCorruption(t *testing.T) {
	dir := t.TempDir()
	n := openTest(t, dir, config())
	publish(t, n, "x")
	publish(t, n, "y")
	n.Close()
	path := filepath.Join(dir, "journal.v1")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Truncate(path, int64(len(b)-1)); e != nil {
		t.Fatal(e)
	}
	n = openTest(t, dir, config())
	if r := publish(t, n, "replacement"); r.Offset != 1 {
		t.Fatal(r)
	}
	n.Close()
	b, e = os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	b[len(b)-1] ^= 1
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(dir, config()); e == nil {
		t.Fatal("corruption accepted")
	}
}
func TestConcurrentCommit(t *testing.T) {
	n := openTest(t, t.TempDir(), config())
	publish(t, n, "")
	const count = 24
	var wg sync.WaitGroup
	out := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- n.Commit(ctx, p, "g", 0, nil) }()
	}
	wg.Wait()
	close(out)
	wins := 0
	for e := range out {
		if e == nil {
			wins++
		} else if e != ErrConflict {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal(wins)
	}
}
func TestConcurrentPublish(t *testing.T) {
	n := openTest(t, t.TempDir(), config())
	const count = 48
	var wg sync.WaitGroup
	out := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := n.Publish(ctx, "words", Message{Key: "k", ID: fmt.Sprint(i % 12)})
			out <- e
		}(i)
	}
	wg.Wait()
	close(out)
	for e := range out {
		if e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	for i := int64(0); i < 12; i++ {
		r, e := n.Read(ctx, p, i, false)
		if e != nil || r.Offset != i || seen[r.ID] {
			t.Fatal(i, r, e)
		}
		seen[r.ID] = true
	}
	if _, e := n.Read(ctx, p, 12, false); e != ErrEmpty {
		t.Fatal(e)
	}
}
func TestFoldAndCancel(t *testing.T) {
	n := openTest(t, t.TempDir(), config())
	publish(t, n, "1")
	publish(t, n, "2")
	stop := errors.New("stop")
	fold := func(limit int) error {
		return Fold(ctx, n, p, "g", 0, func(_ context.Context, s int, r Record) (int, error) {
			if int(r.Offset) == limit {
				return 0, stop
			}
			return s + 1, nil
		})
	}
	if e := fold(1); e != stop {
		t.Fatal(e)
	}
	publish(t, n, "3")
	if e := fold(2); e != stop {
		t.Fatal(e)
	}
	pos, e := n.Position(ctx, p, "g")
	if e != nil || pos.Next != 2 || string(pos.State) != "2" {
		t.Fatal(pos, e)
	}
	w, c := context.WithTimeout(ctx, 15*time.Millisecond)
	defer c()
	if _, e = n.Read(w, p, 3, true); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := n.Read(ctx, p, 3, true); done <- e }()
	n.Close()
	select {
	case e := <-done:
		if e != ErrClosed {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked")
	}
}
func TestPeer(t *testing.T) {
	c := config()
	c.Nodes = append(c.Nodes, "b")
	c.Topics["words"] = 2
	a := openTest(t, t.TempDir(), c)
	c.Node = "b"
	b := openTest(t, t.TempDir(), c)
	token := "0123456789abcdef"
	mb, e := NewMesh(b, map[string]string{"a": "http://localhost"}, token, nil)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(mb)
	defer srv.Close()
	ma, e := NewMesh(a, map[string]string{"b": srv.URL}, token, nil)
	if e != nil {
		t.Fatal(e)
	}
	key := ""
	for i := 0; ; i++ {
		key = fmt.Sprint(i)
		dest, _ := a.partition("words", key)
		if dest.Number == 1 {
			break
		}
	}
	msg := Message{Key: key, Value: []byte("wire"), ID: "stable"}
	part := Partition{"words", 1}
	if _, e = a.Publish(ctx, "words", msg); e != ErrOwner {
		t.Fatal(e)
	}
	timeout, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, e := ma.Read(timeout, part, 0, true)
		if e == nil && r.Key != key {
			e = ErrInvalid
		}
		done <- e
	}()
	if _, e = ma.Publish(ctx, "words", msg); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = ma.Commit(ctx, part, "group", 0, []byte("s")); e != nil {
		t.Fatal(e)
	}
	pos, e := ma.Position(ctx, part, "group")
	if e != nil || pos.Next != 1 || string(pos.State) != "s" {
		t.Fatal(pos, e)
	}
	if e = ma.Commit(ctx, part, "group", 0, nil); e != ErrConflict {
		t.Fatal(e)
	}
	resp, e := http.Post(srv.URL+PeerPath, "application/json", bytes.NewBufferString("{}"))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.Status)
	}
	raw, _ := json.Marshal(request{Owner: "b", Topology: "different"})
	req, _ := http.NewRequest("POST", srv.URL+PeerPath, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatal(resp.Status)
	}
	srv.Close()
	if _, e = ma.Publish(ctx, "words", msg); e == nil {
		t.Fatal("unavailable owner acknowledged write")
	}
}
func TestProcessExit(t *testing.T) {
	if dir := os.Getenv("DOST_TEST_EXIT"); dir != "" {
		n, e := Open(dir, config())
		if e != nil {
			os.Exit(2)
		}
		_, e = n.Publish(ctx, "words", Message{Key: "exit"})
		if e != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessExit$")
	cmd.Env = append(os.Environ(), "DOST_TEST_EXIT="+dir)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(string(b), e)
	}
	n := openTest(t, dir, config())
	r, e := n.Read(ctx, p, 0, false)
	if e != nil || r.Key != "exit" {
		t.Fatal(r, e)
	}
}

type interrupted struct{ Store }

func (interrupted) Commit(context.Context, Partition, string, int64, []byte) error {
	return errors.New("simulated interruption")
}
func TestPipeRetry(t *testing.T) {
	dir := t.TempDir()
	c := config()
	n := openTest(t, dir, c)
	publish(t, n, "in")
	n.Close()
	c.Topics["output"] = 1
	n = openTest(t, dir, c)
	fn := func(ctx context.Context, r Record) (string, []byte, error) { return r.Key, r.Value, nil }
	if err := Pipe(ctx, interrupted{n}, p, "copy", "output", fn); err == nil {
		t.Fatal("expected interruption")
	}
	n.Close()
	n = openTest(t, dir, c)
	limited, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Pipe(limited, n, p, "copy", "output", fn) }()
	for {
		pos, e := n.Position(limited, p, "copy")
		if e != nil {
			t.Fatal(e)
		}
		if pos.Next == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	output := Partition{"output", 0}
	r, e := n.Read(ctx, output, 0, false)
	if e != nil || r.Key != "key" {
		t.Fatal(r, e)
	}
	if _, e = n.Read(ctx, output, 1, false); e != ErrEmpty {
		t.Fatal("duplicate output", e)
	}
}
