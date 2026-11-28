package console

import (
	"context"
	"fmt"
	"testing"
)

// A wire Message-ID is supplied by the sender. The product can use it as a
// thread parent only when exactly one prior message fits this correspondence.
func TestProductEmailThreadAncestry(t *testing.T) {
	s := setupWorkspace(t)
	s.email.domain = "agents.example"
	if err := initThreads(s.db); err != nil {
		t.Fatal(err)
	}
	deliver := func(t *testing.T, from, box, wire, reference, body string) (string, string) {
		t.Helper()
		header := fmt.Sprintf("From: %s\r\nMessage-ID: <%s@outside.example>\r\nSubject: Same subject\r\n", from, wire)
		if reference != "" {
			header += fmt.Sprintf("In-Reply-To: <%s@outside.example>\r\n", reference)
		}
		id, err := s.acceptLetter(context.Background(), from, box+"@agents.example", []byte(header+"\r\n"+body))
		if err != nil {
			t.Fatal(err)
		}
		var root string
		err = s.db.QueryRow("SELECT root FROM messages JOIN thread_members USING(seq) WHERE id=?", id).Scan(&root)
		if err != nil {
			t.Fatal(err)
		}
		return id, root
	}
	const sender = "human@outside.example"
	parent, _ := deliver(t, sender, "sales", "parent", "", "original")
	for _, tt := range []struct {
		name, from, box, ref string
		linked               bool
	}{
		{"same-correspondent", sender, "sales", "parent", true},
		{"other-inbox", sender, "support", "parent", false},
		{"other-correspondent", "second@outside.example", "sales", "parent", false},
		{"subject-only", sender, "sales", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, root := deliver(t, tt.from, tt.box, tt.name, tt.ref, "reply")
			want := id
			if tt.linked {
				want = parent
			}
			if root != want {
				t.Fatalf("root %q, want %q", root, want)
			}
		})
	}
	deliver(t, sender, "sales", "parent", "", "independent message reusing wire id")
	id, root := deliver(t, sender, "sales", "ambiguous", "parent", "ambiguous parent")
	if id != root {
		t.Fatal("ambiguous reference joined an existing conversation")
	}
}
