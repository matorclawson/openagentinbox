package deliverystore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMailboxStatsAndSenderFilteredProjection(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "mail.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	for _, seat := range []string{"a", "b", "c"} {
		if e = s.PutSeat(SeatRec{Seat: seat}); e != nil {
			t.Fatal(e)
		}
	}
	for _, m := range []Message{{ID: "a0", Sender: "a", Recipients: []string{"b"}, Priority: 0, Body: []byte("zero")}, {ID: "c1", Sender: "c", Recipients: []string{"b"}, Priority: 1, Body: []byte("one")}, {ID: "a2", Sender: "a", Recipients: []string{"b"}, Priority: 2, Body: []byte("two")}} {
		if _, e = s.AcceptRecord(ctx, m, now); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.AckSeat("b", "a0", now); e != nil {
		t.Fatal(e)
	}
	rows, _, e := s.MailboxStats(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r := rows[1]
	if r.Seat != "b" || r.UnreadP0 != 0 || r.UnreadP1 != 1 || r.PendingP0 != 1 || r.PendingP1 != 1 {
		t.Fatalf("%+v", r)
	}
	v, e := s.PendingFrom(ctx, "b", "a", 1)
	if e != nil || v.Total != 1 || len(v.Rows) != 1 || v.Rows[0].ID != "a0" {
		t.Fatal(v, e)
	}
	d, e := s.ForSeat("b", "c1")
	if e != nil || d.NoticedMilli != 0 {
		t.Fatal("summary acknowledged mail", d, e)
	}
}
