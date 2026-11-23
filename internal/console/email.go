package console

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

const maxLetter = 1 << 20
const emailSchema = setupMailSchema + `
CREATE TABLE IF NOT EXISTS email_decisions(id TEXT,attempt INTEGER,state TEXT,evidence TEXT,decided INTEGER,PRIMARY KEY(id,attempt));
CREATE TABLE IF NOT EXISTS email_original(id TEXT PRIMARY KEY, envelope TEXT NOT NULL, wire_id TEXT NOT NULL, raw BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS email_files(id TEXT PRIMARY KEY,name TEXT NOT NULL,data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS email_outbox(id TEXT PRIMARY KEY,box TEXT NOT NULL,origin TEXT NOT NULL,bounce TEXT NOT NULL UNIQUE,remote_action TEXT NOT NULL DEFAULT '',remote_status TEXT NOT NULL DEFAULT '',destination TEXT NOT NULL,state TEXT NOT NULL,due INTEGER NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,code INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS email_state_id ON email_outbox(state,id);
CREATE INDEX IF NOT EXISTS email_due ON email_outbox(state,due);
CREATE INDEX IF NOT EXISTS email_budget ON email_outbox(box,created);
`

var fileNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._ -]{0,119}$`)

func attachmentDigest(name string, b []byte) []byte {
	h := sha256.New()
	io.WriteString(h, name+"\x00")
	h.Write(b)
	return h.Sum(nil)
}

var dnsName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var localPart = regexp.MustCompile("^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]{1,64}$")

func validDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if !dnsName.MatchString(label) {
			return false
		}
	}
	return true
}
func validEmail(addr string) bool {
	local, domain, ok := strings.Cut(addr, "@")
	return ok && len(addr) <= 254 && localPart.MatchString(local) && !strings.Contains(local, "..") && !strings.HasPrefix(local, ".") && !strings.HasSuffix(local, ".") && validDomain(strings.ToLower(domain))
}

type emailConfig struct {
	domain, socket, relay string
	senders               map[string]bool
	wake                  chan struct{}
}

func (s *server) emailBox(ctx context.Context, address string) (string, error) {
	local, domain, _ := strings.Cut(strings.ToLower(address), "@")
	if !validEmail(address) {
		return "", sql.ErrNoRows
	}
	hosted, e := s.hosted(ctx, domain)
	if e != nil {
		return "", e
	}
	if !hosted {
		return "", sql.ErrNoRows
	}
	if token, ok := strings.CutPrefix(local, "bounce+"); ok {
		var box string
		err := s.store.Reader().QueryRowContext(ctx, "SELECT box FROM email_outbox WHERE bounce=? AND (box=? OR EXISTS(SELECT 1 FROM agents WHERE name=box AND key IS NOT NULL))", token, s.operator).Scan(&box)
		return box, err
	}
	if local == s.operator {
		return local, nil
	}
	var box string
	if id, ok := strings.CutPrefix(local, "agent+"); ok {
		err := s.store.Reader().QueryRowContext(ctx, "SELECT box FROM mailbox_ids JOIN agents ON name=box WHERE id=? AND key IS NOT NULL", id).Scan(&box)
		return box, err
	}
	err := s.store.Reader().QueryRowContext(ctx, "SELECT a.box FROM addresses a LEFT JOIN agents g ON g.name=a.box WHERE a.address=? AND (g.key IS NOT NULL OR a.box=?)", local, s.operator).Scan(&box)
	return box, err
}

// Extract just one text part for the normal inbox. Original MIME is retained byte-for-byte.
func textPart(h mail.Header, r io.Reader, depth int, budget *int) string {
	*budget--
	if *budget < 0 || depth > 8 {
		return ""
	}
	kind, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if h.Get("Content-Type") == "" {
		kind = "text/plain"
	} else if err != nil {
		return ""
	}
	disposition, _, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	if disposition == "attachment" {
		return ""
	}
	if strings.HasPrefix(kind, "multipart/") {
		mr := multipart.NewReader(r, params["boundary"])
		for *budget > 0 {
			part, err := mr.NextRawPart()
			if err != nil {
				break
			}
			v := textPart(mail.Header(part.Header), part, depth+1, budget)
			part.Close()
			if v != "" {
				return v
			}
		}
		return ""
	}
	if kind != "text/plain" {
		return ""
	}
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	b, err := io.ReadAll(io.LimitReader(r, 48000))
	if err != nil {
		return ""
	}
	return strings.ToValidUTF8(string(b), "�")
}
func (s *server) acceptLetter(ctx context.Context, envelope, rcpt string, raw []byte) (string, error) {
	if len(raw) > maxLetter {
		return "", failure{552, "message too large"}
	}
	box, err := s.emailBox(ctx, rcpt)
	if err != nil {
		return "", err
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", failure{550, "invalid message headers"}
	}
	sender := "external:unknown"
	if from, e := mail.ParseAddress(msg.Header.Get("From")); e == nil && validEmail(from.Address) {
		sender = "external:" + from.Address
	}
	subject := msg.Header.Get("Subject")
	if sub, e := new(mime.WordDecoder).DecodeHeader(subject); e == nil {
		subject = sub
	}
	subject = strings.ToValidUTF8(subject, "�")
	if utf8.RuneCountInString(subject) > 240 {
		subject = string([]rune(subject)[:240])
	}
	budget := 64
	body := textPart(msg.Header, msg.Body, 0, &budget)
	if body == "" {
		body = "[See original email for the message content.]"
	}
	var reports []deliveryReport
	local, _, _ := strings.Cut(strings.ToLower(rcpt), "@")
	bounce, isBounce := strings.CutPrefix(local, "bounce+")
	if isBounce && envelope == "" {
		reports = readDeliveryReport(raw)
	}
	digest := sha256.Sum256(raw)
	key := fmt.Sprintf("edge/%s/%s/%x", box, envelope, digest)
	if isBounce {
		key += "/" + bounce
	}
	m := deliverystore.Message{ID: uuid.NewString(), Sender: sender, Recipients: []string{box}, Subject: subject, Body: []byte(body), Priority: 1}
	parent, e := mailParent(ctx, s.store.Reader(), msg.Header, box, sender, 1<<63-1)
	if e != nil {
		return "", e
	}
	if parent != 0 {
		if e = s.store.Reader().QueryRowContext(ctx, "SELECT root FROM thread_members WHERE seq=?", parent).Scan(&m.ThreadHint); e != nil {
			return "", e
		}
	}
	id, err := s.store.AcceptMail(ctx, key, m, time.Now(), func(tx *sql.Tx) error {
		if box != s.operator {
			var active int
			if err := tx.QueryRow("SELECT 1 FROM agents WHERE name=? AND key IS NOT NULL", box).Scan(&active); err != nil {
				return err
			}
		}
		if len(reports) > 0 {
			if err := applyDeliveryReport(tx, bounce, reports); err != nil {
				return err
			}
		}
		wire := msg.Header.Get("Message-ID")
		if len(wire) < 3 || len(wire) > 256 || wire[0] != '<' || wire[len(wire)-1] != '>' || !validEmail(wire[1:len(wire)-1]) {
			wire = ""
		}
		_, err := tx.Exec("INSERT INTO email_original VALUES(?,?,?,?)", m.ID, envelope, wire, raw)
		return err
	})
	if err == nil {
		s.live.signal()
	}
	return id, err
}
func (s *server) originalEmail(_ http.ResponseWriter, r *http.Request) (any, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = s.store.Reader().QueryRowContext(r.Context(), `SELECT o.raw FROM email_original o JOIN messages m ON m.id=o.id JOIN deliveries d USING(seq) WHERE o.id=? AND (d.seat=? OR ?)`, r.PathValue("id"), actor, strings.HasPrefix(r.URL.Path, "/api/desk/")).Scan(&raw)
	return object{"content_type": "message/rfc822", "base64": raw}, err
}
func (s *server) enqueueLetter(tx *sql.Tx, m deliverystore.Message, fileName string, fileData []byte) error {
	var addr string
	if m.Sender == s.operator {
		addr = s.operator
	} else if err := tx.QueryRow("SELECT address FROM addresses JOIN agents ON name=box WHERE box=? AND current=1 AND key IS NOT NULL", m.Sender).Scan(&addr); err != nil {
		return err
	}
	domain := s.primaryDomain()
	if domain == "" {
		return failure{409, "Activate a mail domain first"}
	}
	if !validEmail(addr + "@" + domain) {
		var id string
		if err := tx.QueryRow("SELECT id FROM mailbox_ids WHERE box=?", m.Sender).Scan(&id); err != nil {
			return err
		}
		addr = "agent+" + id
	}
	var recent int
	if err := tx.QueryRow("SELECT count(*) FROM email_outbox WHERE box=? AND created>=?", m.Sender, time.Now().Add(-time.Hour).Unix()).Scan(&recent); err != nil {
		return err
	}
	if recent >= 120 {
		return failure{429, "External mail hourly limit reached"}
	}
	_, err := tx.Exec("INSERT INTO email_outbox(id,box,origin,bounce,destination,state,due,created) VALUES(?,?,?,?,?,'queued',0,?)", m.ID, m.Sender, addr+"@"+domain, strings.ReplaceAll(uuid.NewString(), "-", ""), m.Recipients[0], time.Now().Unix())
	if err != nil {
		return err
	}
	if fileName != "" {
		if _, err = tx.Exec("INSERT INTO email_files VALUES(?,?,?)", m.ID, fileName, fileData); err != nil {
			return err
		}
	}
	_, err = tx.Exec("INSERT INTO message_readers VALUES(?,?)", m.ID, m.Sender)
	return err
}

// An operator can resolve an uncertain handoff after checking the edge queue
// and logs using the stable Message-ID. Racing or stale decisions fail closed.
func (s *server) reconcileEmail(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct {
		ID, Decision, Evidence string
		Attempt                int
	}
	if err := input(r, &q); err != nil {
		return nil, err
	}
	state := map[string]string{"confirmed": "relayed", "retry": "queued", "cancel": "failed"}[q.Decision]
	if state == "" || uuid.Validate(q.ID) != nil || q.Attempt < 1 || len(strings.TrimSpace(q.Evidence)) < 1 || len(q.Evidence) > 256 {
		return nil, failure{400, "Supply a decision, attempt number and edge evidence reference"}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE email_outbox SET state=?,due=0 WHERE id=? AND state='uncertain' AND attempts=?", state, q.ID, q.Attempt)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, failure{409, "Delivery state changed"}
	}
	_, err = tx.Exec("INSERT INTO email_decisions VALUES(?,?,?,?,?)", q.ID, q.Attempt, state, q.Evidence, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if s.email != nil {
		select {
		case s.email.wake <- struct{}{}:
		default:
		}
	}
	s.live.signal()
	return object{"state": state}, nil
}
func (s *server) uncertainMail(_ http.ResponseWriter, r *http.Request) (any, error) {
	rows, err := s.store.Reader().QueryContext(r.Context(), "SELECT id,origin,destination,attempts FROM email_outbox WHERE state='uncertain' AND id>? ORDER BY id LIMIT 100", r.URL.Query().Get("after"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []object{}
	for rows.Next() {
		var id, from, to string
		var attempt int
		if err = rows.Scan(&id, &from, &to, &attempt); err != nil {
			return nil, err
		}
		result = append(result, object{"id": id, "from": from, "to": to, "attempt": attempt})
	}
	return object{"rows": result}, rows.Err()
}
