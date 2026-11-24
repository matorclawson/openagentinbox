package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureDNS struct{ proof string }

func (f fixtureDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if strings.HasPrefix(name, "_openagentinbox.") {
		return []string{f.proof}, nil
	}
	return nil, &net.DNSError{IsNotFound: true}
}
func (f fixtureDNS) LookupHost(context.Context, string) ([]string, error) {
	return []string{"192.0.2.10"}, nil
}
func (f fixtureDNS) LookupMX(context.Context, string) ([]*net.MX, error) {
	return []*net.MX{{Host: "mx.agents.example.", Pref: 10}}, nil
}
func setupWorkspace(t *testing.T) *server {
	t.Helper()
	st, e := deliverystore.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{store: st, db: st.Writer(), operator: "operator", email: &emailConfig{relay: "127.0.0.1:25", senders: map[string]bool{}}}
	_, e = s.db.Exec(schema + liveSchema + pairSchema + addressSchema + onboardingSchema + trackingSchema + emailSchema)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"sales", "support", "third"} {
		_, e = s.db.Exec("INSERT INTO agents VALUES(?,?)", name, hash(strings.Repeat("x", 43-len(name))+name))
		if e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func TestDomainAndAliasControl(t *testing.T) {
	s := setupWorkspace(t)
	ctx := context.Background()
	_, e := s.saveDomain(nil, httptest.NewRequest("POST", "/", strings.NewReader(`{"domain":" Agents.Example ","ip":"192.0.2.10"}`)))
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("domain", "agents.example")
	p, e := s.plan(req)
	if e != nil {
		t.Fatal(e)
	}
	s.resolver = fixtureDNS{"wrong"}
	if _, e = s.activateDomain(nil, req); e == nil {
		t.Fatal("unverified activation")
	}
	s.resolver = fixtureDNS{"openagentinbox=" + p.Proof}
	if _, e = s.activateDomain(nil, req); e != nil {
		t.Fatal(e)
	}
	add := func(box, alias string) error {
		_, e := s.addAlias(nil, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"box":%q,"alias":%q}`, box, alias))))
		return e
	}
	var ch = make(chan error, 2)
	for _, box := range []string{"support", "sales"} {
		go func() { ch <- add(box, "HELP") }()
	}
	a, b := <-ch, <-ch
	if (a == nil) == (b == nil) {
		t.Fatalf("expected a single owner %v %v", a, b)
	}
	owner, e := s.emailBox(ctx, "Help@Agents.Example")
	if e != nil || owner == "" {
		t.Fatal(owner, e)
	}
	if e = add(owner, "help"); e != nil {
		t.Fatal(e)
	}
	if e = add("third", "sales"); e == nil {
		t.Fatal("claimed existing agent")
	}
	if _, e = s.db.Exec("INSERT INTO roster VALUES('pending','t','h',?)", now()+100000); e != nil {
		t.Fatal(e)
	}
	if e = add("third", "pending"); e == nil {
		t.Fatal("claimed an outstanding ticket")
	}
	if _, e = s.db.Exec("UPDATE agents SET key=NULL WHERE name=?", owner); e != nil {
		t.Fatal(e)
	}
	if _, e = s.emailBox(ctx, "help@agents.example"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("revoked alias", e)
	}
}
func TestConversationMigrationAndFolderIsolation(t *testing.T) {
	s := setupWorkspace(t)
	ctx := context.Background()
	put := func(id, src, dst, parent string) {
		t.Helper()
		_, e := s.store.Accept(id, deliverystore.Message{ID: id, Sender: src, Recipients: []string{dst}, ParentID: parent, Subject: "Update", Body: []byte("body")}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
	}
	put("root", "sales", "support", "")
	put("reply", "support", "sales", "root")
	put("different", "sales", "support", "")
	put("first", "external:a@other.example", "sales", "")
	put("second", "external:a@other.example", "sales", "")
	for _, row := range []struct{ id, wire, headers string }{{"first", "<first@other.example>", ""}, {"second", "<second@other.example>", "In-Reply-To: <first@other.example>\r\n"}} {
		if _, e := s.db.Exec("INSERT INTO email_original VALUES(?,?,?,?)", row.id, "", row.wire, []byte(row.headers+"From: a@other.example\r\n\r\nbody")); e != nil {
			t.Fatal(e)
		}
	}
	if e := initThreads(s.db); e != nil {
		t.Fatal(e)
	}
	if e := initThreads(s.db); e != nil {
		t.Fatal(e)
	}
	put("again", "sales", "support", "reply")
	for _, p := range []struct{ id, root string }{{"second", "first"}, {"again", "root"}, {"different", "different"}} {
		var root string
		if e := s.db.QueryRow("SELECT root FROM messages JOIN thread_members USING(seq) WHERE id=?", p.id).Scan(&root); e != nil || root != p.root {
			t.Fatal(p, root, e)
		}
	}
	call := func(actor, folder, root string) []object {
		req := httptest.NewRequest("GET", "/api/agent/threads?box=sales&folder="+folder, nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 43-len(actor))+actor)
		req.SetPathValue("root", root)
		v, e := s.threads(nil, req)
		if e != nil {
			t.Fatal(e)
		}
		return v.(object)["rows"].([]object)
	}
	if len(call("third", "inbox", "root")) != 0 || len(call("sales", "sent", "")) != 2 || len(call("support", "sent", "root")) != 3 {
		t.Fatal("incorrect visibility")
	}
	s.email.domain = "agents.example"
	_, e := s.acceptLetter(ctx, "", "sales@agents.example", []byte("From: a@other.example\r\nReferences: <second@other.example>\r\nSubject: changed\r\n\r\nbody"))
	if e != nil || len(call("sales", "inbox", "first")) != 3 {
		t.Fatal("internet reply", e)
	}
}

type forbiddenDNS struct{ t *testing.T }

func (f forbiddenDNS) LookupTXT(context.Context, string) ([]string, error) {
	f.t.Helper()
	f.t.Fatal("unexpected DNS lookup")
	return nil, nil
}
func (f forbiddenDNS) LookupHost(context.Context, string) ([]string, error) {
	f.t.Fatal("unexpected DNS lookup")
	return nil, nil
}
func (f forbiddenDNS) LookupMX(context.Context, string) ([]*net.MX, error) {
	f.t.Fatal("unexpected DNS lookup")
	return nil, nil
}

func TestActivationWithoutAMailEdgeMakesNoDNSLookup(t *testing.T) {
	s := setupWorkspace(t)
	if _, e := s.saveDomain(nil, httptest.NewRequest("POST", "/", strings.NewReader(`{"domain":"agents.example","ip":"192.0.2.10"}`))); e != nil {
		t.Fatal(e)
	}
	s.email = nil
	s.resolver = forbiddenDNS{t}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("domain", "agents.example")
	if _, e := s.activateDomain(nil, req); e == nil {
		t.Fatal("activation succeeded without a mail edge")
	}
}
