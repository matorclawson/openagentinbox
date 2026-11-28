package console

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

func TestTransportRecordAndMailboxCommitTogether(t *testing.T) {
	s := setupWorkspace(t)
	ctx := context.Background()
	m := deliverystore.Message{ID: "sample", Sender: "external:someone@example.net", Recipients: []string{"support"}, Subject: "Question", Body: []byte("A question"), Priority: 1}
	failed := errors.New("transport receipt failed")
	if _, err := s.acceptInbound(ctx, "source:event", m, func(*sql.Tx) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM messages WHERE id=?", m.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled back count %d: %v", n, err)
	}
	calls := 0
	record := func(tx *sql.Tx) error {
		calls++
		_, err := tx.Exec("INSERT INTO message_readers VALUES(?,?)", m.ID, "support")
		return err
	}
	for i := 0; i < 2; i++ {
		id, err := s.acceptInbound(ctx, "source:event", m, record)
		if err != nil || id != m.ID {
			t.Fatalf("attempt %d: %q %v", i, id, err)
		}
	}
	if calls != 1 {
		t.Fatalf("duplicate durable side effects: %d", calls)
	}
}
func TestDeliveryRoutesKeepHostedTrafficLocal(t *testing.T) {
	s := setupWorkspace(t)
	s.email.domain = "agents.example"
	s.email.senders["operator"] = true
	for _, tc := range []struct {
		to, want string
		external bool
	}{
		{"support", "support", false}, {"support@agents.example", "support", false}, {"who@example.net", "who@example.net", true},
	} {
		q := sendFields{To: tc.to, Subject: "Hello"}
		route, err := s.resolveDelivery(context.Background(), "operator", &q)
		if err != nil || q.To != tc.want || (route != nil) != tc.external {
			t.Errorf("%q: %q external=%v err=%v", tc.to, q.To, route != nil, err)
		}
	}
	q := sendFields{To: "support@agents.example", FileName: "note", FileData: []byte("file")}
	if _, err := s.resolveDelivery(context.Background(), "operator", &q); err == nil {
		t.Fatal("hosted shortcut accepted external-only attachment")
	}
}
