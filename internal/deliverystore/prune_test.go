package deliverystore

import (
	"path/filepath"
	"testing"
)

func TestPruneSeatRemovesOnlyThatSeatsRowsThatAreNotKept(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows := []ImportRow{
		{ID: "m1", Sender: "alpha", Priority: 1, CreatedMilli: 1, Body: []byte("b"), Deliveries: []ImportDelivery{{Seat: "beta"}, {Seat: "gamma"}}},
		{ID: "m2", Sender: "alpha", Priority: 1, CreatedMilli: 2, Body: []byte("b"), Deliveries: []ImportDelivery{{Seat: "beta"}}},
		{ID: "m3", Sender: "alpha", Priority: 1, CreatedMilli: 3, Body: []byte("b"), Deliveries: []ImportDelivery{{Seat: "beta"}}},
	}
	if _, _, err := st.ImportBatch(rows); err != nil {
		t.Fatal(err)
	}
	n, err := st.PruneSeat("beta", map[string]bool{"m1": true, "m3": true})
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if u, _ := st.UnreadCount("beta"); u != 2 {
		t.Fatalf("beta unread %d", u)
	}
	if u, _ := st.UnreadCount("gamma"); u != 1 {
		t.Fatalf("another seat's rows are not touched: %d", u)
	}
	if n, _ := st.PruneSeat("beta", map[string]bool{"m1": true, "m3": true}); n != 0 {
		t.Fatalf("nothing left to prune: %d", n)
	}
}
