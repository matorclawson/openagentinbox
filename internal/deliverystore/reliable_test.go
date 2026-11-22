package deliverystore

import (
	"context"
	"errors"
	"github.com/rodriveracom/openagentinbox/delivery"
	"path/filepath"
	"testing"
	"time"
)

type receiver struct {
	seen  map[string]delivery.Receipt
	lose  bool
	calls int
}

func (r *receiver) Receive(_ context.Context, e delivery.Envelope) (delivery.Receipt, error) {
	r.calls++
	v, ok := r.seen[e.ID]
	if !ok {
		v = delivery.Receipt{ID: e.ID, MessageID: e.MessageID, Seat: e.Seat, Assignment: "runtime", Generation: 1, State: "received"}
		r.seen[e.ID] = v
	}
	if r.lose {
		r.lose = false
		return delivery.Receipt{}, errors.New("lost response")
	}
	return v, nil
}
func TestOutboxRecoveryAndChangeFeed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	m := Message{ID: "one", Sender: "sender", Recipients: []string{"reader"}, Body: []byte("body"), Priority: 1}
	if _, e = s.Accept("op", m, now); e != nil {
		t.Fatal(e)
	}
	r := &receiver{seen: map[string]delivery.Receipt{}, lose: true}
	if n, e := s.DispatchPending(ctx, r, 5); n != 0 || e == nil {
		t.Fatal(n, e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if n, e := s.DispatchPending(ctx, r, 5); n != 1 || e != nil {
		t.Fatal(n, e)
	}
	if n, e := s.DispatchPending(ctx, r, 5); n != 0 || e != nil || len(r.seen) != 1 {
		t.Fatal(n, e)
	}
	changes, e := s.Changes(ctx, "reader", 0, 50)
	if e != nil || len(changes.Events) != 2 || changes.Events[1].Kind != "runtime_received" {
		t.Fatal(changes, e)
	}
	cursor := changes.Cursor
	if e = s.Notice("reader", "one", now); e != nil {
		t.Fatal(e)
	}
	changes, e = s.Changes(ctx, "reader", cursor, 50)
	if e != nil || len(changes.Events) != 1 || !changes.Events[0].Read {
		t.Fatal(changes, e)
	}
	if n, e := s.PruneChanges(ctx, "reader", changes.Cursor, 1); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	changes, e = s.Changes(ctx, "reader", 0, 50)
	if e != nil || !changes.Resync {
		t.Fatal(changes, e)
	}
	snap, e := s.Snapshot(ctx, "reader", 0, 50)
	if e != nil || len(snap.Entries) != 1 || !snap.Entries[0].Read {
		t.Fatal(snap, e)
	}
	if n, e := s.PendingCount("reader"); e != nil || n != 1 {
		t.Fatal("read must remain owed", n, e)
	}
}
func TestActivityDigestAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now()
	_, e = s.Accept("op", Message{ID: "m", Sender: "sender", Recipients: []string{"reader"}, Priority: 0, Subject: "request", Body: []byte("SECRET BODY")}, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.LinkTask(ctx, "foreign", "m", "agenda:task"); !errors.Is(e, ErrNoSuch) {
		t.Fatal(e)
	}
	if e = s.LinkTask(ctx, "reader", "m", "agenda:task"); e != nil {
		t.Fatal(e)
	}
	a := Activity{Key: "task", TaskRef: "agenda:task", State: "working", ExpiresAt: now.Add(time.Hour).UnixMilli()}
	for n := 0; n < 3; n++ {
		if e = s.PutActivity(ctx, "reader", a, now); e != nil {
			t.Fatal(e)
		}
	}
	acts, e := s.Activities(ctx, "reader", now)
	if e != nil || len(acts) != 1 {
		t.Fatal(acts, e)
	}
	changes, e := s.Changes(ctx, "reader", 0, 50)
	if e != nil || len(changes.Events) != 1 {
		t.Fatal("heartbeats became messages", changes, e)
	}
	d, e := s.Digest(ctx, "reader")
	if e != nil || d.Pending != 1 || len(d.Entries) != 1 || d.Entries[0].TaskRef != "agenda:task" || d.Entries[0].Subject != "request" {
		t.Fatal(d, e)
	}
	acts, e = s.Activities(ctx, "reader", now.Add(2*time.Hour))
	if e != nil || len(acts) != 0 {
		t.Fatal(acts, e)
	}
}
func TestOutboxTransactionRollback(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, e = s.Accept("op", Message{ID: "m", Sender: "s", Recipients: []string{"r", "r"}, Body: []byte("body"), Priority: 1}, time.Now())
	if e == nil {
		t.Fatal("duplicate recipient should roll back")
	}
	for _, table := range []string{"messages", "delivery_outbox", "mailbox_events"} {
		var n int
		if e = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}

func TestLateImportAndRetentionResync(t *testing.T) {
	ctx := context.Background()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	row := ImportRow{ID: "old", Sender: "sender", Priority: 1, CreatedMilli: 1, Body: []byte("old"), Deliveries: []ImportDelivery{{Seat: "other"}}}
	if _, _, e = s.ImportBatch([]ImportRow{row}); e != nil {
		t.Fatal(e)
	}
	_, e = s.Accept("new", Message{ID: "new", Sender: "sender", Recipients: []string{"reader"}, Body: []byte("body"), Priority: 1}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.Changes(ctx, "reader", 0, 50)
	if e != nil {
		t.Fatal(e)
	}
	row.Deliveries = []ImportDelivery{{Seat: "reader"}}
	if _, _, e = s.ImportBatch([]ImportRow{row}); e != nil {
		t.Fatal(e)
	}
	after, e := s.Changes(ctx, "reader", before.Cursor, 50)
	if e != nil || len(after.Events) != 1 || after.Events[0].MessageID != "old" {
		t.Fatal("late recipient disappeared behind message cursor", after, e)
	}
	snap, e := s.Snapshot(ctx, "reader", 0, 1)
	if e != nil || !snap.More || len(snap.Entries) != 1 {
		t.Fatal(snap, e)
	}
	if _, e = s.PruneSeat("reader", map[string]bool{"new": true}); e != nil {
		t.Fatal(e)
	}
	delta, e := s.Changes(ctx, "reader", snap.ChangesAfter, 50)
	if e != nil || len(delta.Events) != 1 || delta.Events[0].Kind != "removed" {
		t.Fatal(delta, e)
	}
	if _, e = s.PruneChanges(ctx, "reader", delta.Cursor, 100); e != nil {
		t.Fatal(e)
	}
	resumed, e := s.Changes(ctx, "reader", snap.ChangesAfter, 50)
	if e != nil || !resumed.Resync {
		t.Fatal(resumed, e)
	}
}

func TestNativeViewChoosesNewestCreationNotLatestImport(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, row := range []ImportRow{
		{ID: "new", Sender: "s", Priority: 1, CreatedMilli: 200, Body: []byte("Priority: p1"), Deliveries: []ImportDelivery{{Seat: "reader"}}},
		{ID: "late-old", Sender: "s", Priority: 1, CreatedMilli: 100, Body: []byte("Priority: p1"), Deliveries: []ImportDelivery{{Seat: "reader"}}},
	} {
		if _, _, e = s.ImportBatch([]ImportRow{row}); e != nil {
			t.Fatal(e)
		}
	}
	v, e := s.InboxView("reader", 1, 100)
	if e != nil || len(v.Rows) != 1 || v.Rows[0].ID != "new" {
		t.Fatal(v, e)
	}
	v, e = s.PendingView("reader", 1)
	if e != nil || len(v.Rows) != 1 || v.Rows[0].ID != "new" {
		t.Fatal(v, e)
	}
}

func TestHistoricalReadImportDoesNotScheduleWake(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, _, e = s.ImportBatch([]ImportRow{{ID: "read-history", Sender: "s", Priority: 1, Body: []byte("old"), Deliveries: []ImportDelivery{{Seat: "reader", NoticedMilli: 1}}}})
	if e != nil {
		t.Fatal(e)
	}
	r := &receiver{seen: map[string]delivery.Receipt{}}
	if n, e := s.DispatchPending(context.Background(), r, 50); e != nil || n != 0 || r.calls != 0 {
		t.Fatal(n, e, r.calls)
	}
	if n, e := s.PendingCount("reader"); e != nil || n != 1 {
		t.Fatal("read request lost pending status", n, e)
	}
}

type runtimeAuthority struct{}

func (runtimeAuthority) WithCurrent(ctx context.Context, seat string, commit func(delivery.Binding, func() error) error) error {
	if seat != "reader" {
		return delivery.ErrFence
	}
	return commit(delivery.Binding{Assignment: "assignment", Generation: 3}, ctx.Err)
}

type lostReceipt struct{ receiver delivery.Receiver }

func (l lostReceipt) Receive(ctx context.Context, e delivery.Envelope) (delivery.Receipt, error) {
	if _, err := l.receiver.Receive(ctx, e); err != nil {
		return delivery.Receipt{}, err
	}
	return delivery.Receipt{}, errors.New("response lost after durable commit")
}
func TestDurableEndToEndLostReceipt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "mail.db")
	receiverPath := filepath.Join(dir, "private", "runtime.db")
	s, e := Open(storePath)
	if e != nil {
		t.Fatal(e)
	}
	i, e := delivery.OpenInbox(receiverPath, runtimeAuthority{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Accept("op", Message{ID: "m", Sender: "sender", Recipients: []string{"reader"}, Body: []byte("body")}, time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DispatchPending(ctx, lostReceipt{i}, 50); e == nil {
		t.Fatal("lost response labeled success")
	}
	s.Close()
	i.Close()
	s, e = Open(storePath)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	i, e = delivery.OpenInbox(receiverPath, runtimeAuthority{})
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	if n, e := s.DispatchPending(ctx, i, 50); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	receipt, e := s.Receipt(ctx, "reader", "m")
	if e != nil || receipt == nil || receipt.Generation != 3 {
		t.Fatal(receipt, e)
	}
	received, e := i.List(ctx, "reader", 0, 50)
	if e != nil || len(received) != 1 {
		t.Fatal(received, e)
	}
	if n, e := s.UnreadCount("reader"); e != nil || n != 1 {
		t.Fatal("Runtime receipt acknowledged actor reading", n, e)
	}
}

// Council read of PR 145 (M2): the mirror's open must not add the reliable-delivery migration (outbox, events, their triggers, a snapshot row per delivery). Only the daemon's
// Open does, and it can still add it later to the same database.
func TestOpenMirrorDoesNotMigrateReliableDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirror.db")
	count := func(s *Store, where string) int {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE ` + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	m, err := OpenMirror(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(m, `name IN ('delivery_outbox','mailbox_events','sync_floor','reliable_version') OR name LIKE 'reliable_%'`); n != 0 {
		t.Fatalf("the mirror open created %d reliable-delivery objects", n)
	}
	if n := count(m, `name LIKE 'deliveries_%' AND type='trigger'`); n == 0 {
		t.Fatal("the mirror open must keep the read-index triggers")
	}
	m.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if n := count(d, `name IN ('delivery_outbox','mailbox_events','reliable_version')`); n != 3 {
		t.Fatalf("the daemon's open must add the reliable-delivery tables to the same database, got %d of 3", n)
	}
}
