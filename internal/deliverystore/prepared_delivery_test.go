package deliverystore

import (
	"context"
	"testing"
	"time"
)

// A failing trigger on a later recipient must roll back the whole send.
// The same connection must remain usable after the trigger is removed.
func TestPreparedDeliveryRollbackAndReuse(t *testing.T) {
	s := nativeStore(t)
	m := nativeMessage("retry-prepared")
	m.Recipients = []string{"b", "c"}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_c BEFORE INSERT ON deliveries WHEN NEW.seat='c' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.AcceptContext(context.Background(), "key", m, now); err == nil {
		t.Fatal("accepted partial delivery")
	}
	rows, _, err := s.Unread("b", 0, 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial delivery %v %v", rows, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_c"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptContext(context.Background(), "key", m, now); err != nil {
		t.Fatal(err)
	}
	for _, seat := range m.Recipients {
		rows, _, err := s.Unread(seat, 0, 50)
		if err != nil || len(rows) != 1 || rows[0].Delivered || rows[0].CreatedMilli != now.UnixMilli() {
			t.Fatalf("%s: %v %v", seat, rows, err)
		}
	}
}
