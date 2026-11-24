package console

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"io"
	"mime"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixtureDaemon(t *testing.T) {
	if os.Getenv("OAI_TEST_LMTP") == "" {
		t.Skip("used by tests/email-edge.py --tcp-fixture")
	}
	listenMailSocket = func(path string) (net.Listener, error) {
		addr := os.Getenv("OAI_TEST_LMTP")
		if strings.HasSuffix(path, ".map") {
			addr = os.Getenv("OAI_TEST_MAP")
		}
		host, _, e := net.SplitHostPort(addr)
		if e != nil || host != "127.0.0.1" {
			t.Fatal("expected test loopback")
		}
		return net.Listen("tcp", addr)
	}
	if err := Run(); err != nil {
		t.Fatal(err)
	}
}

func TestLetterAdmissionAndRevocation(t *testing.T) {
	store, err := deliverystore.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.Writer()
	if _, err = db.Exec(schema + addressSchema + emailSchema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO agents VALUES('sales','active')"); err != nil {
		t.Fatal(err)
	}
	s := server{db: db, store: store, operator: "operator", email: &emailConfig{domain: "agents.example"}}
	ctx := context.Background()
	if _, err = s.acceptLetter(ctx, "", "sales@other.example", []byte("From: a@b.example\r\n\r\nhello")); err != sql.ErrNoRows {
		t.Fatalf("external domain: %v", err)
	}
	msg := deliverystore.Message{ID: "out", Sender: "sales", Recipients: []string{"a@b.example"}, Subject: "hello", Body: []byte("message")}
	if _, err = db.Exec(`WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<120) INSERT INTO email_outbox(id,box,origin,bounce,destination,state,due,created) SELECT x,'sales','sales@agents.example',x,'a@b.example','relayed',0,? FROM seq`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptMail(ctx, "limit", msg, time.Now(), func(tx *sql.Tx) error { return s.enqueueLetter(tx, msg, "", nil) })
	if f, ok := err.(failure); !ok || f.code != 429 {
		t.Fatalf("quota error: %v", err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM messages").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial commit %d %v", n, err)
	}
	if _, err = db.Exec("UPDATE agents SET key=NULL WHERE name='sales'"); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"sales@agents.example", "bounce+1@agents.example"} {
		if _, err = s.emailBox(ctx, addr); err != sql.ErrNoRows {
			t.Fatalf("revoked %s: %v", addr, err)
		}
	}
}
func TestBoundedEmailPreview(t *testing.T) {
	m, err := mail.ReadMessage(strings.NewReader("Content-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), 100000))))
	if err != nil {
		t.Fatal(err)
	}
	budget := 64
	if n := len(textPart(m.Header, m.Body, 0, &budget)); n != 48000 {
		t.Fatalf("preview bytes %d", n)
	}
}
func TestWireHeaders(t *testing.T) {
	var b bytes.Buffer
	subject := strings.Repeat("📬", 240)
	err := writeLetter(&b, relayItem{id: "abc", from: "a@mine.example", to: "b@theirs.example", created: 1, body: "hello"}, "mine.example", strings.ReplaceAll(mime.QEncoding.Encode("UTF-8", subject), "?= =?", "?=\r\n =?"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(b.String(), "\r\n") {
		if len(line) > 998 {
			t.Fatal("unfolded header")
		}
	}
	m, err := mail.ReadMessage(&b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, m.Body))
	if err != nil || string(data) != "hello" {
		t.Fatalf("wire body %q %v", data, err)
	}
}

func TestReadLetterFragmentsAndSize(t *testing.T) {
	data := strings.Repeat("x", 8192) + ".\r\n..dot\r\n"
	raw, err := readLetter(bufio.NewReaderSize(strings.NewReader(data+".\r\n"), 8192))
	if err != nil || string(raw) != strings.Replace(data, "..dot", ".dot", 1) {
		t.Fatal("lost DATA or misread terminator", err)
	}
	r := bufio.NewReader(strings.NewReader(strings.Repeat("x", maxLetter+1) + "\r\n.\r\nNOOP\r\n"))
	if _, err = readLetter(r); err != errLetterSize {
		t.Fatal(err)
	}
	line, _ := r.ReadString('\n')
	if line != "NOOP\r\n" {
		t.Fatal("transaction not drained")
	}
}
