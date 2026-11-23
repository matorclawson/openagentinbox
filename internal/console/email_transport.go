package console

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Production listeners are filesystem protected. Protocol tests can supply an
// isolated loopback listener on hosts that disable AF_UNIX.
var listenMailSocket = func(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err == nil {
		err = os.Chmod(path, 0660)
		if err != nil {
			ln.Close()
		}
	}
	return ln, err
}

func (s *server) startEmail(ctx context.Context) (func(), error) {
	domain := strings.ToLower(os.Getenv("INBOX_EMAIL_DOMAIN"))
	if domain == "" && os.Getenv("INBOX_EMAIL_SOCKET") == "" {
		return func() {}, nil
	}
	socket, relay := os.Getenv("INBOX_EMAIL_SOCKET"), os.Getenv("INBOX_EMAIL_RELAY")
	if (domain != "" && !validDomain(domain)) || !filepath.IsAbs(socket) {
		return nil, errors.New("email requires a DNS domain and absolute LMTP socket path")
	}
	senders := map[string]bool{}
	for _, v := range strings.Fields(os.Getenv("INBOX_EMAIL_SENDERS")) {
		if !nameRE.MatchString(v) {
			return nil, errors.New("invalid email sender name")
		}
		senders[v] = true
	}
	if relay != "" {
		host, p, err := net.SplitHostPort(relay)
		port, e := strconv.Atoi(p)
		if err != nil || e != nil || port < 1 || port > 65535 || !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("email relay must be a literal loopback address and port")
		}
	} else if len(senders) > 0 {
		return nil, errors.New("email senders require a relay")
	}
	// Only the service account may write this directory. Keep the lock inode
	// across restarts so a second daemon cannot unlink a live listener.
	lock, err := os.OpenFile(socket+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, err
	}
	owns := false
	defer func() {
		if !owns {
			lock.Close()
		}
	}()
	for _, path := range []string{socket, socket + ".map"} {
		info, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("email socket path occupied by another file")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	}
	ln, err := listenMailSocket(socket)
	if err != nil {
		return nil, err
	}
	maps, err := listenMailSocket(socket + ".map")
	if err != nil {
		ln.Close()
		return nil, err
	}
	s.email = &emailConfig{domain: domain, socket: socket, relay: relay, senders: senders, wake: make(chan struct{}, 1)}
	emailCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
				wg.Add(1)
				go func() { defer wg.Done(); defer func() { <-slots }(); s.lmtp(emailCtx, conn) }()
			default:
				conn.Close()
			}
		}
	}()
	mapSlots := make(chan struct{}, 8)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := maps.Accept()
			if err != nil {
				return
			}
			select {
			case mapSlots <- struct{}{}:
				wg.Add(1)
				go func() { defer wg.Done(); defer func() { <-mapSlots }(); s.recipientMap(emailCtx, c) }()
			default:
				c.Close()
			}
		}
	}()
	if relay != "" {
		wg.Add(1)
		go func() { defer wg.Done(); s.relayWorker(emailCtx) }()
	}
	owns = true
	return func() { cancel(); ln.Close(); maps.Close(); wg.Wait(); lock.Close() }, nil
}
func pathArg(arg, prefix string) (string, string, bool) {
	if !strings.HasPrefix(strings.ToUpper(arg), prefix+"<") {
		return "", "", false
	}
	address, params, ok := strings.Cut(arg[len(prefix)+1:], ">")
	return address, strings.TrimSpace(params), ok
}
func (s *server) lmtp(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	r := bufio.NewReaderSize(c, 8192)
	write := func(v string) error { _, e := io.WriteString(c, v+"\r\n"); return e }
	greeted, started := false, false
	var from, to string
	c.SetDeadline(time.Now().Add(45 * time.Second))
	hello := s.email.domain
	if hello == "" {
		hello = "localhost"
	}
	if write("220 "+hello+" LMTP") != nil {
		return
	}
	for {
		line, e := r.ReadSlice('\n')
		if e != nil {
			return
		}
		verb, arg, _ := strings.Cut(strings.TrimRight(string(line), "\r\n"), " ")
		reply := "500 unsupported command"
		switch strings.ToUpper(verb) {
		case "LHLO":
			greeted = true
			started = false
			to = ""
			reply = "250-" + hello + "\r\n250-8BITMIME\r\n250-ENHANCEDSTATUSCODES\r\n250 SIZE 1048576"
		case "NOOP":
			reply = "250 2.0.0 OK"
		case "RSET":
			started = false
			to = ""
			reply = "250 2.0.0 reset"
		case "QUIT":
			write("221 2.0.0 bye")
			return
		case "MAIL":
			address, params, ok := pathArg(arg, "FROM:")
			if !greeted || started {
				reply = "503 5.5.1 invalid sequence"
				break
			}
			if !ok || (address != "" && !validEmail(address)) {
				reply = "553 5.1.7 unsupported envelope"
				break
			}
			reply = "250 2.1.0 OK"
			for _, p := range strings.Fields(strings.ToUpper(params)) {
				if strings.HasPrefix(p, "SIZE=") {
					n, e := strconv.Atoi(p[5:])
					if e != nil || n < 0 || n > maxLetter {
						reply = "552 5.3.4 invalid size"
						break
					}
				} else if p != "BODY=8BITMIME" && p != "BODY=7BIT" {
					reply = "555 5.5.4 unsupported parameter"
					break
				}
			}
			if strings.HasPrefix(reply, "250") {
				started = true
				from = address
			}
		case "RCPT":
			address, params, ok := pathArg(arg, "TO:")
			if !started {
				reply = "503 5.5.1 MAIL required"
				break
			}
			if to != "" {
				reply = "452 4.5.3 one recipient per transaction"
				break
			}
			if !ok || params != "" {
				reply = "501 5.5.4 invalid recipient"
				break
			}
			_, err := s.emailBox(ctx, address)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				reply = "550 5.1.1 unknown recipient"
			case err != nil:
				reply = "451 4.3.0 mailbox unavailable"
			default:
				to = address
				reply = "250 2.1.5 OK"
			}
		case "DATA":
			if to == "" {
				reply = "503 5.5.1 recipient required"
				break
			}
			if write("354 send message") != nil {
				return
			}
			raw, err := readLetter(r)
			if err != nil && !errors.Is(err, errLetterSize) {
				return
			}
			var id string
			if err == nil {
				id, err = s.acceptLetter(ctx, from, to, raw)
			}
			var f failure
			switch {
			case errors.Is(err, errLetterSize):
				reply = "552 5.3.4 message too large"
			case errors.Is(err, sql.ErrNoRows):
				reply = "550 5.1.1 unknown recipient"
			case errors.As(err, &f):
				reply = fmt.Sprintf("%d 5.6.0 %s", f.code, f.msg)
			case err != nil:
				reply = "451 4.3.0 temporary store failure"
			default:
				reply = "250 2.1.5 stored " + id
			}
			started = false
			to = ""
		}
		if write(reply) != nil {
			return
		}
	}
}

var errLetterSize = errors.New("email exceeds size limit")

// Drain a rejected DATA transaction through its terminator so the edge can
// record a permanent refusal. ReadSlice fragments long lines without allocating.
func readLetter(r *bufio.Reader) ([]byte, error) {
	var b bytes.Buffer
	start, oversize := true, false
	for {
		line, err := r.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			return nil, err
		}
		if start && bytes.Equal(line, []byte(".\r\n")) {
			if oversize {
				return nil, errLetterSize
			}
			return b.Bytes(), nil
		}
		if start && len(line) > 0 && line[0] == '.' {
			line = line[1:]
		}
		if !oversize {
			if b.Len()+len(line) > maxLetter {
				oversize = true
			} else {
				b.Write(line)
			}
		}
		start = err == nil
	}
}

type relayItem struct {
	bounce                      string
	fileName, replyWire         string
	fileData                    []byte
	id, from, to, subject, body string
	attempt                     int
	created                     int64
}

func sendToRelay(ctx context.Context, relay string, m relayItem) (state string, code int) {
	classify := func(err error, ambiguous bool) (string, int) {
		var e *textproto.Error
		if errors.As(err, &e) {
			if e.Code >= 500 {
				return "failed", e.Code
			}
			return "queued", e.Code
		}
		if ambiguous {
			return "uncertain", 0
		}
		return "queued", 0
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", relay)
	if err != nil {
		return "queued", 0
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	host, _, _ := net.SplitHostPort(relay)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return classify(err, false)
	}
	defer c.Close()
	_, senderDomain, _ := strings.Cut(m.from, "@")
	if err = c.Mail("bounce+" + m.bounce + "@" + senderDomain); err != nil {
		return classify(err, false)
	}
	if err = c.Rcpt(m.to); err != nil {
		return classify(err, false)
	}
	w, err := c.Data()
	if err != nil {
		return classify(err, false)
	}
	_, domain, _ := strings.Cut(m.from, "@")
	// Encode all subjects, including long ASCII words, without header injection.
	subject := strings.ReplaceAll(mime.QEncoding.Encode("UTF-8", m.subject), "?= =?", "?=\r\n =?")
	if err = writeLetter(w, m, domain, subject); err != nil {
		return classify(err, false)
	}
	if err = w.Close(); err != nil {
		return classify(err, true)
	}
	return "relayed", 250
}
func writeLetter(w io.Writer, m relayItem, domain, subject string) error {
	if _, err := fmt.Fprintf(w, "From: <%s>\r\nTo: <%s>\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nSubject: %s\r\nMIME-Version: 1.0\r\n", m.from, m.to, time.Unix(m.created, 0).UTC().Format(time.RFC1123Z), m.id, domain, subject); err != nil {
		return err
	}
	if m.replyWire != "" {
		if _, err := fmt.Fprintf(w, "In-Reply-To: %s\r\nReferences: %s\r\n", m.replyWire, m.replyWire); err != nil {
			return err
		}
	}
	boundary := "oai-" + m.id
	if m.fileName != "" {
		if _, err := fmt.Fprintf(w, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n--%s\r\n", boundary, boundary); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"); err != nil {
		return err
	}
	if err := writeBase64(w, []byte(m.body)); err != nil {
		return err
	}
	if m.fileName != "" {
		if _, err := fmt.Fprintf(w, "--%s\r\nContent-Type: application/octet-stream\r\nContent-Disposition: %s\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, mime.FormatMediaType("attachment", map[string]string{"filename": m.fileName})); err != nil {
			return err
		}
		if err := writeBase64(w, m.fileData); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "--%s--\r\n", boundary)
		return err
	}
	return nil
}
func writeBase64(w io.Writer, data []byte) error {
	var line [78]byte
	for len(data) > 0 {
		n := min(len(data), 57)
		size := base64.StdEncoding.EncodedLen(n)
		base64.StdEncoding.Encode(line[:size], data[:n])
		line[size] = '\r'
		line[size+1] = '\n'
		if _, err := w.Write(line[:size+2]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
func (s *server) relayOne(ctx context.Context) (bool, error) {
	var m relayItem
	row := s.db.QueryRowContext(ctx, `UPDATE email_outbox SET state='sending',due=?,attempts=attempts+1 WHERE id=(SELECT id FROM email_outbox WHERE state='queued' AND due<=? ORDER BY due,id LIMIT 1) RETURNING id,origin,destination,attempts,created,bounce`, time.Now().Add(time.Minute).Unix(), time.Now().Unix())
	err := row.Scan(&m.id, &m.from, &m.to, &m.attempt, &m.created, &m.bounce)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = s.store.Reader().QueryRowContext(ctx, `SELECT m.subject,m.body,coalesce(f.name,''),f.data,coalesce(p.wire_id,'') FROM messages m LEFT JOIN email_files f ON f.id=m.id LEFT JOIN email_original p ON p.id=m.parent_id WHERE m.id=?`, m.id).Scan(&m.subject, &m.body, &m.fileName, &m.fileData, &m.replyWire); err != nil {
		return false, err
	}
	state, code := sendToRelay(ctx, s.email.relay, m)
	if state == "queued" && m.attempt >= 12 {
		state = "failed"
	}
	_, err = s.db.ExecContext(ctx, "UPDATE email_outbox SET state=?,code=?,due=? WHERE id=? AND state='sending'", state, code, time.Now().Add(time.Second*time.Duration(5<<min(m.attempt, 9))).Unix(), m.id)
	if err == nil {
		s.live.signal()
	}
	return true, err
}
func (s *server) relayWorker(ctx context.Context) {
	for ctx.Err() == nil {
		var pending, expired bool
		err := s.store.Reader().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM email_outbox WHERE state='queued' AND due<=?1),EXISTS(SELECT 1 FROM email_outbox WHERE state='sending' AND due<=?1)", time.Now().Unix()).Scan(&pending, &expired)
		if err == nil && expired {
			_, err = s.db.ExecContext(ctx, "UPDATE email_outbox SET state='uncertain' WHERE state='sending' AND due<=?", time.Now().Unix())
			s.live.signal()
		}
		if err == nil && pending {
			var work bool
			work, err = s.relayOne(ctx)
			if err == nil && work {
				continue
			}
		}
		if err != nil && ctx.Err() == nil {
			log.Print("email outbox unavailable; retrying")
		}
		select {
		case <-ctx.Done():
			return
		case <-s.email.wake:
		case <-time.After(30 * time.Second):
		}
	}
}

// Postfix socketmap requests are netstrings. One bounded connection is served
// at a time; short idle timeouts allow other smtpd processes to make progress.
func (s *server) recipientMap(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	r := bufio.NewReaderSize(c, 512)
	for {
		c.SetDeadline(time.Now().Add(2 * time.Second))
		size, e := r.ReadSlice(':')
		if e != nil || len(size) > 4 {
			return
		}
		n, e := strconv.Atoi(string(size[:len(size)-1]))
		if e != nil || n < 1 || n > 320 {
			return
		}
		data := make([]byte, n+1)
		if _, e = io.ReadFull(r, data); e != nil || data[n] != ',' {
			return
		}
		table, address, _ := strings.Cut(string(data[:n]), " ")
		result := "NOTFOUND "
		if table == "domains" {
			ok, e := s.hosted(ctx, strings.ToLower(address))
			if e != nil {
				result = "TEMP domain lookup unavailable"
			} else if ok {
				result = "OK 1"
			}
		}
		if table == "inboxes" {
			_, e := s.emailBox(ctx, address)
			switch {
			case e == nil:
				result = "OK 1"
			case !errors.Is(e, sql.ErrNoRows):
				result = "TEMP mailbox unavailable"
			}
		}
		if _, e = fmt.Fprintf(c, "%d:%s,", len(result), result); e != nil {
			return
		}
	}
}
