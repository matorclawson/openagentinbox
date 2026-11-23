package console

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"net/http"

	"strconv"
	"strings"
	"time"
)

func (s *server) mailRoutes(add func(string, handler)) {
	s.setupRoutes(add)
	add("GET /api/desk/email/uncertain", s.uncertainMail)
	add("POST /api/desk/email/reconcile", s.reconcileEmail)
	add("POST /api/desk/revoke-batch", s.revokeBatch)
	add("POST /api/desk/addresses", s.reconcile)
	add("GET /api/desk/accounts", s.accounts)
	for _, v := range []string{"agents", "rotate", "revoke"} {
		add("POST /api/desk/"+v, s.credential)
	}
	for _, a := range []string{"desk", "agent"} {
		add("GET /api/"+a+"/threads", s.threads)
		add("GET /api/"+a+"/threads/{root}", s.threads)
		add("POST /api/"+a+"/send", s.send)
		add("GET /api/"+a+"/email-original/{id}", s.originalEmail)
		add("GET /api/"+a+"/receipt/{id}", s.receipt)
		add("POST /api/"+a+"/request", s.requestAction)
		add("GET /api/"+a+"/alerts", s.alerts)
		add("GET /api/"+a+"/message/{id}", s.detail)
	}
	add("POST /api/desk/message/{id}/share", s.shareMessage)
	add("GET /api/agent/whoami", func(_ http.ResponseWriter, r *http.Request) (any, error) {
		name, err := s.actor(r)
		if err != nil {
			return nil, err
		}
		return s.mailboxIdentity(name)
	})
	add("GET /api/desk/list", s.list)
	add("GET /api/agent/inbox", s.list)
	add("GET /api/agent/notice", s.notice)
}
func (s *server) accounts(_ http.ResponseWriter, r *http.Request) (any, error) {
	names := []string{}
	rs, e := s.store.Reader().QueryContext(r.Context(), "SELECT address FROM addresses WHERE current=1 ORDER BY address")
	if e != nil {
		return nil, e
	}
	defer rs.Close()
	for rs.Next() {
		var n string
		if e = rs.Scan(&n); e != nil {
			return nil, e
		}
		names = append(names, n)
	}
	if e = rs.Err(); e != nil {
		return nil, e
	}
	result := object{"operator": s.operator, "agents": names}
	if s.email != nil {
		result["email_domain"] = s.primaryDomain()
		result["operator_email_send"] = s.maySend(s.operator)
	}
	return result, nil
}
func (s *server) credential(_ http.ResponseWriter, r *http.Request) (any, error) {

	var q struct{ Name string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	if !nameRE.MatchString(q.Name) || q.Name == s.operator {
		return nil, failure{400, "Invalid agent name"}
	}
	t := token()
	var res sql.Result
	var e error
	switch r.URL.Path {
	case "/api/desk/agents":
		res, e = s.db.Exec("INSERT OR IGNORE INTO agents SELECT ?1,?2 WHERE NOT EXISTS(SELECT 1 FROM addresses WHERE address=?1)", q.Name, hash(t))
	case "/api/desk/revoke":
		res, e = s.db.Exec("UPDATE agents SET key=NULL WHERE name=(SELECT box FROM addresses WHERE address=?)", q.Name)
	default:
		res, e = s.db.Exec("UPDATE agents SET key=? WHERE name=(SELECT box FROM addresses WHERE address=?)", hash(t), q.Name)
	}
	if e != nil {
		return nil, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, failure{409, "Agent missing or already assigned"}
	}
	s.live.signal()
	if r.URL.Path == "/api/desk/revoke" {
		return object{"ok": true}, nil
	}
	return object{"name": q.Name, "credential": t}, nil
}
func bearer(r *http.Request) string {
	v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !bearerRE.MatchString(v) {
		return ""
	}
	return v
}
func (s *server) actor(r *http.Request) (string, error) {
	if strings.HasPrefix(r.URL.Path, "/api/desk/") {
		return s.operator, nil
	}
	var n string
	b := bearer(r)
	if b != "" {
		e := s.store.Reader().QueryRowContext(r.Context(), "SELECT name FROM agents WHERE key=?", hash(b)).Scan(&n)
		if e == nil {
			return n, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return "", e
		}
	}
	return "", failure{401, "Invalid agent credential"}
}
func (s *server) send(_ http.ResponseWriter, r *http.Request) (any, error) {
	started := time.Now()
	src, e := s.actor(r)
	if e != nil {
		return nil, e
	}
	var q struct {
		To, Subject, Body, Key, Intent, ReplyTo, FileName string
		FileData                                          []byte
	}
	if e = input(r, &q); e != nil {
		return nil, e
	}
	q.Subject = strings.TrimSpace(q.Subject)
	q.Body = strings.TrimSpace(q.Body)
	external := strings.Contains(q.To, "@")
	localDomain := false
	if external {
		_, d, _ := strings.Cut(q.To, "@")
		localDomain, e = s.hosted(r.Context(), strings.ToLower(d))
		if e != nil {
			return nil, e
		}
	}
	if external && localDomain && q.ReplyTo == "" {
		box, err := s.emailBox(r.Context(), q.To)
		if err != nil {
			return nil, err
		}
		q.To = box
		external = false
	}
	if (q.FileName != "" || q.FileData != nil) && (!external || !fileNameRE.MatchString(q.FileName) || len(q.FileData) > 96<<10 || len(q.FileData) == 0) {
		return nil, failure{400, "External attachment needs a safe filename and 1 to 98304 bytes"}
	}
	if external {
		if q.ReplyTo != "" {
			q.To = strings.TrimPrefix(q.To, "external:")
		}
		if !s.maySend(src) {
			return nil, failure{403, "External email sending is disabled for this mailbox"}
		}
		if !validEmail(q.To) || localDomain || strings.ContainsAny(q.Subject, "\r\n\x00") || (q.Intent != "" && q.Intent != "fyi") {
			return nil, failure{400, "Use an external address, single-line subject and FYI intent"}
		}
	}
	if (!external && !nameRE.MatchString(q.To)) || len(q.Subject) == 0 || len([]rune(q.Subject)) > 240 || len(q.Body) == 0 || len([]rune(q.Body)) > 48000 || uuid.Validate(q.Key) != nil {
		return nil, failure{400, "Invalid message fields"}
	}

	if !external && q.To != s.operator {
		if e = s.store.Reader().QueryRowContext(r.Context(), "SELECT box FROM addresses WHERE address=?", q.To).Scan(&q.To); e != nil {
			return nil, e
		}
	}
	priority := 1
	switch q.Intent {
	case "", "fyi":
		q.Intent = ""
	case "request":
	case "urgent":
		priority = 0
	default:
		return nil, failure{400, "Unknown intent"}
	}
	timestamp := time.Now()
	msg := deliverystore.Message{ID: uuid.NewString(), Sender: src, Subject: q.Subject, Recipients: []string{q.To}, Priority: priority, Intent: q.Intent, ParentID: q.ReplyTo, Body: []byte(q.Body)}
	if q.FileName != "" {
		msg.ExternalPayloadDigest = attachmentDigest(q.FileName, q.FileData)
	}
	id, e := s.store.AcceptMail(r.Context(), "console/"+src+"/"+q.Key, msg, timestamp, func(tx *sql.Tx) error {
		if q.ReplyTo != "" {
			var parent string
			if err := tx.QueryRow("SELECT m.sender FROM messages m JOIN deliveries d USING(seq) WHERE m.id=? AND d.seat=?", q.ReplyTo, src).Scan(&parent); err != nil {
				return err
			}
			if parent != q.To && !(external && parent == "external:"+q.To) {
				return failure{400, "Reply must go to the original sender"}
			}
			_, err := tx.Exec(`INSERT INTO mail_receipts(id,seat,replied) VALUES(?,?,?) ON CONFLICT(id,seat) DO UPDATE SET replied=coalesce(replied,excluded.replied),state=CASE WHEN state='fyi' THEN state ELSE 'resolved' END,due=NULL`, q.ReplyTo, src, timestamp.UnixMilli())
			if err != nil {
				return err
			}
		}
		if external {
			return s.enqueueLetter(tx, msg, q.FileName, q.FileData)
		}
		if q.Intent == "" {
			return nil
		}
		delay := 15 * time.Minute
		if priority == 0 {
			delay = 2 * time.Minute
		}
		_, err := tx.Exec("INSERT INTO mail_receipts(id,seat,state,due) VALUES(?,?,'pending',?)", msg.ID, q.To, timestamp.Add(delay).UnixMilli())
		return err
	})
	if errors.Is(e, deliverystore.ErrConflict) {
		return nil, failure{409, "Request key already used for different content"}
	}
	if e == nil {
		if external {
			select {
			case s.email.wake <- struct{}{}:
			default:
			}
		}
		s.performance.observe(true, q.To, time.Since(started))
		s.live.signal()
	}
	return object{"id": id}, e
}
func (s *server) list(_ http.ResponseWriter, r *http.Request) (any, error) {
	box, e := s.actor(r)
	if e != nil {
		return nil, e
	}
	q := r.URL.Query()
	sent := q.Get("folder") == "sent"
	if v := q.Get("folder"); v != "" && v != "sent" && v != "inbox" {
		return nil, failure{400, "Unknown folder"}
	}
	if strings.HasPrefix(r.URL.Path, "/api/desk/") {
		if v := q.Get("box"); v != "" {
			box = v
		}
		switch q.Get("folder") {
		case "sent":
			sent = true
		case "", "inbox":
		default:
			return nil, failure{400, "Unknown folder"}
		}
	}
	if !nameRE.MatchString(box) {
		return nil, failure{400, "Invalid mailbox"}
	}

	if box != s.operator && strings.HasPrefix(r.URL.Path, "/api/desk/") {
		if e = s.store.Reader().QueryRowContext(r.Context(), "SELECT box FROM addresses WHERE address=?", box).Scan(&box); e != nil {
			return nil, e
		}
	}
	before := int64(1<<63 - 1)
	if v := q.Get("before"); v != "" {
		before, e = strconv.ParseInt(v, 10, 64)
		if e != nil || before < 1 {
			return nil, failure{400, "Invalid cursor"}
		}
	}
	query := "SELECT m.seq,m.id,m.sender,d.seat,m.subject,m.created_text FROM deliveries d JOIN messages m ON m.seq=d.seq WHERE d.seat=? AND d.seq<? ORDER BY d.seq DESC LIMIT 41"
	if sent {
		query = "SELECT m.seq,m.id,m.sender,(SELECT group_concat(seat, ', ') FROM deliveries WHERE seq=m.seq),m.subject,m.created_text FROM messages m WHERE sender=? AND seq<? ORDER BY seq DESC LIMIT 41"
	}
	rs, e := s.store.Reader().QueryContext(r.Context(), query, box, before)
	if e != nil {
		return nil, e
	}
	defer rs.Close()
	result := []object{}
	var next any
	var last int64
	for rs.Next() {
		if len(result) == 40 {
			next = strconv.FormatInt(last, 10)
			break
		}
		var id, src, dst, subject, at string
		if e = rs.Scan(&last, &id, &src, &dst, &subject, &at); e != nil {
			return nil, e
		}
		result = append(result, object{"seq": last, "id": id, "src": src, "dst": dst, "subject": subject, "at": at})
	}
	return object{"rows": result, "next": next}, rs.Err()
}
func (s *server) detail(_ http.ResponseWriter, r *http.Request) (any, error) {
	started := time.Now()
	actor, e := s.actor(r)
	if e != nil {
		return nil, e
	}
	id := r.PathValue("id")
	if len(id) > 256 {
		return nil, failure{400, "Invalid ID"}
	}

	var src, dst, sub, body, at, root string
	var opened, replied, due int64
	var state string
	query := `SELECT coalesce((SELECT root FROM thread_members WHERE seq=m.seq),m.id),m.sender,d.seat,m.subject,m.body,m.created_text,coalesce(r.opened,0),coalesce(r.replied,0),coalesce(r.state,'fyi'),coalesce(r.due,0) FROM messages m JOIN deliveries d USING(seq) LEFT JOIN mail_receipts r ON r.id=m.id AND r.seat=d.seat WHERE m.id=?`
	args := []any{id}
	if strings.HasPrefix(r.URL.Path, "/api/agent/") {
		query += " AND (m.sender=? OR d.seat=? OR EXISTS(SELECT 1 FROM message_readers WHERE id=m.id AND seat=?))"
		args = append(args, actor, actor, actor)
	}
	e = s.store.Reader().QueryRowContext(r.Context(), query, args...).Scan(&root, &src, &dst, &sub, &body, &at, &opened, &replied, &state, &due)
	if e != nil {
		return nil, e
	}
	if actor == dst && opened == 0 {
		err := s.db.QueryRowContext(r.Context(), `INSERT INTO mail_receipts(id,seat,opened) VALUES(?,?,?) ON CONFLICT(id,seat) DO UPDATE SET opened=excluded.opened WHERE opened IS NULL RETURNING opened,coalesce(replied,0),state,coalesce(due,0)`, id, dst, now()).Scan(&opened, &replied, &state, &due)
		if errors.Is(err, sql.ErrNoRows) {
			// Another recipient fetch won the race to record the first open.
			err = s.store.Reader().QueryRowContext(r.Context(), "SELECT opened,coalesce(replied,0),state,coalesce(due,0) FROM mail_receipts WHERE id=? AND seat=?", id, dst).Scan(&opened, &replied, &state, &due)
		} else if err == nil {
			s.live.signal()
		}
		if err != nil {
			return nil, err
		}
	}
	s.performance.observe(false, "", time.Since(started))
	result := object{"id": id, "thread": root, "src": src, "dst": dst, "subject": sub, "body": body, "at": at, "opened": opened, "replied": replied, "state": state, "due": due}
	if strings.HasPrefix(src, "external:") {
		result["channel"] = "email"
		result["trust"] = "external"
		result["original_available"] = actor == dst || strings.HasPrefix(r.URL.Path, "/api/desk/")
	}
	if strings.Contains(dst, "@") {
		var status string
		if err := s.store.Reader().QueryRowContext(r.Context(), "SELECT state FROM email_outbox WHERE id=?", id).Scan(&status); err != nil {
			return nil, err
		}
		result["email_status"] = status
	}
	return result, nil
}

func (s *server) shareMessage(_ http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Seat string }
	if err := input(r, &q); err != nil {
		return nil, err
	}
	if !nameRE.MatchString(q.Seat) {
		return nil, failure{400, "Invalid inbox name"}
	}
	// Selecting both records also makes repeated grants idempotent.
	result, err := s.db.ExecContext(r.Context(), `INSERT INTO message_readers(id,seat)
        SELECT m.id,a.name FROM messages m JOIN agents a ON a.name=(SELECT box FROM addresses WHERE address=?) AND a.key IS NOT NULL
        WHERE m.id=? ON CONFLICT(id,seat) DO UPDATE SET seat=excluded.seat`, q.Seat, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return nil, failure{404, "Message or active agent not found"}
	}
	return object{"seat": q.Seat, "id": r.PathValue("id")}, err
}

// Notice reads at most 101 index entries plus one index seek, regardless of backlog.
func (s *server) notice(_ http.ResponseWriter, r *http.Request) (any, error) {
	seat, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		return nil, failure{400, "Invalid cursor"}
	}
	var latest int64
	var count int
	err = s.store.Reader().QueryRowContext(r.Context(), `SELECT coalesce((SELECT max(seq) FROM deliveries WHERE seat=?1),0),
 (SELECT count(*) FROM (SELECT seq FROM deliveries WHERE seat=?1 AND seq>?2 LIMIT 101))`, seat, after).Scan(&latest, &count)
	return object{"cursor": latest, "count": count}, err
}
