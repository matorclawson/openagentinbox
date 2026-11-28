package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/rodriveracom/openagentinbox/internal/channelbinding"
	"github.com/rodriveracom/openagentinbox/internal/channelqueue"
	"github.com/rodriveracom/openagentinbox/internal/channelrequest"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

// Replay realistic opaque keys at the normalized boundary. Provider-specific
// authentication, parsing, and network behavior require separate worker tests.
func TestNormalizedChannelJourney(t *testing.T) {
	cases := []struct{ mode, room, subject, event, object string }{
		{"workspace-thread", "C01/1711111111.000001", "U123", "Ev01", "1711111112.000002"},
		{"tenant-activity", "19:chat@thread/parent", "tenant/opaque-user", "activity-1", "activity-1"},
		{"chat-topic", "-100123/9", "7183", "123456", "991"},
		{"business-chat", "number-id/contact-id", "contact-id", "wamid.abc", "wamid.abc"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "inbox.sqlite")
			open := func() *server {
				st, err := deliverystore.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				return &server{store: st, db: st.Writer(), operator: "owner"}
			}
			s := open()
			defer func() { s.store.Close() }()
			_, err := s.db.Exec(schema + channelbinding.Schema + channelqueue.Schema + channelrequest.Schema)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec("INSERT INTO channel_installation VALUES(?,1)", tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec("INSERT INTO channel_binding VALUES(?,?,?,'support',1)", tc.mode, tc.room, "conversation")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec("INSERT INTO channel_membership VALUES(?,?,?)", tc.mode, tc.room, tc.subject)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec("INSERT INTO channel_identity VALUES(?,?,'human')", tc.mode, tc.subject)
			if err != nil {
				t.Fatal(err)
			}
			source := channelbinding.Source{Installation: tc.mode, Remote: tc.room, Subject: tc.subject, Seat: "support"}
			msg := deliverystore.Message{ID: "local-1", Sender: "external-subject-1", Recipients: []string{"support"}, Subject: "Support", Body: []byte("Can you help?"), Priority: 1}
			key := fmt.Sprintf("installation/%s/%s", tc.mode, tc.event)
			record := func(tx *sql.Tx) error {
				a, err := channelbinding.Lookup(ctx, tx, source)
				if err != nil {
					return err
				}
				if a.Participant != "human" {
					return errors.New("no request grant")
				}
				if err = channelrequest.Open(ctx, tx, msg.ID, tc.room, "support", 900); err != nil {
					return err
				}
				first, err := channelbinding.FirstContact(ctx, tx, tc.mode, tc.room, "support", "audience-1")
				if err != nil {
					return err
				}
				if !first {
					return errors.New("unexpected duplicate disclosure")
				}
				return channelqueue.Enqueue(ctx, tx, channelqueue.Item{Installation: tc.mode, Operation: "disclosure/" + tc.object, Binding: tc.room, Message: "intro-revision-1"}, 0)
			}
			for attempt := 0; attempt < 2; attempt++ {
				id, err := s.acceptInbound(ctx, key, msg, record)
				if err != nil || id != msg.ID {
					t.Fatal(id, err)
				}
				if attempt == 0 {
					s.store.Close()
					s = open()
				}
			}
			// Fake remote accepts one disclosure and drops its response. A restart
			// quarantines the local attempt until the remote receipt is reconciled.
			wire := map[string]string{"intro-revision-1": "I’m the support AI agent operated by Example Studio."}
			var sent []string
			dispatch := func(now int64) (channelqueue.Item, error) {
				item, err := channelqueue.Claim(ctx, s.db, tc.mode, now)
				if err == nil {
					sent = append(sent, wire[item.Message])
				}
				return item, err
			}
			row, err := dispatch(0)
			if err != nil {
				t.Fatal(err)
			}
			s.store.Close()
			s = open()
			if err = channelqueue.Recover(ctx, s.db, tc.mode); err != nil {
				t.Fatal(err)
			}
			if _, err = dispatch(1); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("recovered attempt was dispatched again", err)
			}
			if len(sent) != 1 || sent[0] != wire["intro-revision-1"] {
				t.Fatalf("remote sends: %q", sent)
			}
			if err = channelqueue.Reconcile(ctx, s.db, row, channelqueue.Confirmed, 0, "remote-receipt-1"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
