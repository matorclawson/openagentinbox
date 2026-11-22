package deliverystore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestAttentionChangeAndCustody(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	now := time.Now()
	get := func() Attention {
		t.Helper()
		a, e := s.Attention(ctx, "reader")
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	put := func(id string, p int) {
		t.Helper()
		_, e := s.Accept(id, Message{ID: id, Sender: "sender", Recipients: []string{"reader"}, Priority: p, Subject: "request", Body: []byte("PRIVATE BODY")}, now)
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 6; i++ {
		put(fmt.Sprint(i), 0)
	}
	a := get()
	if a.Urgent != 6 || a.Unread != 6 || !a.More || len(a.Entries) != 5 {
		t.Fatal(a)
	}
	// A seventh request is outside the preview but must invalidate the detector.
	put("hidden", 0)
	b := get()
	if b.Revision == a.Revision || b.Urgent != 7 {
		t.Fatal(a, b)
	}
	x, _ := json.Marshal(a.Entries)
	y, _ := json.Marshal(b.Entries)
	if string(x) != string(y) {
		t.Fatal("fixture did not hide new request")
	}
	put("courtesy", 2)
	if e = s.PutActivity(ctx, "reader", Activity{Key: "work", State: "working", TaskRef: "task:1", ExpiresAt: now.Add(time.Hour).UnixMilli()}, now); e != nil {
		t.Fatal(e)
	}
	if get().Revision != b.Revision {
		t.Fatal("p2 or activity invalidated attention")
	}
	if e = s.Notice("reader", "0", now); e != nil {
		t.Fatal(e)
	}
	c := get()
	if c.Revision == b.Revision || c.Urgent != 7 || c.Unread != 6 {
		t.Fatal("read lost answer obligation", c)
	}
	// Same-ID replay and receipt delivery do not turn into new attention.
	put("hidden", 0)
	if get().Revision != c.Revision {
		t.Fatal("retry invalidated attention")
	}
	if e = s.LinkTask(ctx, "reader", "hidden", "agenda:1"); e != nil {
		t.Fatal(e)
	}
	d := get()
	if d.Revision == c.Revision {
		t.Fatal("hidden task link did not invalidate")
	}
	if e = s.LinkTask(ctx, "reader", "hidden", "agenda:1"); e != nil {
		t.Fatal(e)
	}
	if get().Revision != d.Revision {
		t.Fatal("same task ref invalidated")
	}
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`UPDATE deliveries SET noticed_at=99 WHERE seat='reader'`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	if get().Revision != d.Revision {
		t.Fatal("rollback leaked attention")
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if get().Revision != d.Revision {
		t.Fatal("restart invalidated attention")
	}
	foreign, e := s.Attention(ctx, "foreign")
	if e != nil || foreign.Urgent != 0 || len(foreign.Entries) != 0 {
		t.Fatal(foreign, e)
	}
	raw, _ := json.Marshal(get())
	if len(raw) > 8192 {
		t.Fatal("unbounded attention")
	}
	if _, e = s.PruneSeat("reader", map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	z := get()
	if z.Urgent != 0 || z.Unread != 0 || z.Revision == d.Revision {
		t.Fatal("prune not projected", z)
	}
}

func TestAttentionBackfillAndProjection(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now()
	for i := 0; i < 9; i++ {
		_, e = s.Accept(fmt.Sprint(i), Message{ID: fmt.Sprint(i), Sender: "sender", Recipients: []string{"reader"}, Priority: i % 3, Body: []byte("body")}, now)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = s.Notice("reader", "0", now); e != nil {
		t.Fatal(e)
	}
	// Exercise backfill over an existing store, with no double counting on reopen.
	_, e = s.db.Exec(`DROP TABLE attention_state`)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(s.db, true); e != nil {
		t.Fatal(e)
	}
	a, e := s.Attention(ctx, "reader")
	if e != nil || a.Urgent != 3 || a.Requests != 3 || a.Unread != 5 {
		t.Fatal(a, e)
	}
	// Answer state and a changed recipient use the same atomic projection.
	_, e = s.db.Exec(`UPDATE deliveries SET answered_at=42 WHERE seat='reader' AND seq=(SELECT seq FROM messages WHERE id='0')`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`UPDATE deliveries SET seat='other' WHERE seat='reader' AND seq=(SELECT seq FROM messages WHERE id='1')`)
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.Attention(ctx, "reader")
	if e != nil || a.Urgent != 2 || a.Requests != 2 || a.Unread != 4 {
		t.Fatal(a, e)
	}
	b, e := s.Attention(ctx, "other")
	if e != nil || b.Requests != 1 || b.Unread != 1 {
		t.Fatal(b, e)
	}
}
