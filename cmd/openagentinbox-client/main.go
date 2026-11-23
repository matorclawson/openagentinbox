// openagentinbox-client connects a local harness to one remote workspace.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type profile struct {
	Origin string `json:"origin"`
	Name   string `json:"name"`
	Token  string `json:"token"`
}

var safeName = regexp.MustCompile("^[a-z0-9][a-z0-9._-]{0,63}$")

func validOrigin(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || raw != u.Scheme+"://"+u.Host {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}
func secret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func readProfile(path string) (profile, error) {
	var p profile
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return p, e
	}
	if info.Mode().Perm()&0077 != 0 {
		return p, errors.New("profile must be private (chmod 600)")
	}
	e = json.NewDecoder(f).Decode(&p)
	if e == nil && (!validOrigin(p.Origin) || !safeName.MatchString(p.Name) || len(p.Token) != 43) {
		e = errors.New("invalid profile")
	}
	return p, e
}
func ensure(path, origin, name string) (profile, error) {
	if !validOrigin(origin) || !safeName.MatchString(name) {
		return profile{}, errors.New("supply an HTTPS origin (or loopback HTTP) and valid inbox name")
	}
	p, e := readProfile(path)
	if os.IsNotExist(e) {
		dir := filepath.Dir(path)
		if e = os.MkdirAll(dir, 0700); e != nil {
			return p, e
		}
		f, e := os.CreateTemp(dir, ".profile-*")
		if e != nil {
			return p, e
		}
		defer os.Remove(f.Name())
		e = json.NewEncoder(f).Encode(profile{origin, name, secret()})
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return p, e
		}
		if closeErr != nil {
			return p, closeErr
		}
		if e = os.Link(f.Name(), path); e != nil && !os.IsExist(e) {
			return p, e
		}
		p, e = readProfile(path)
		if e != nil {
			return p, e
		}
	} else if e != nil {
		return p, e
	}
	if p.Origin != origin || p.Name != name {
		return p, errors.New("profile belongs to a different inbox; set OPENAGENTINBOX_PROFILE")
	}
	return p, nil
}

var client = http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type remoteError struct {
	status int
	body   string
}

func (e remoteError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }
func request(ctx context.Context, p profile, path string, body any) (json.RawMessage, error) {
	method := "GET"
	var data []byte
	var e error
	if body != nil {
		method = "POST"
		data, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, p.Origin+"/api/agent/"+path, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if p.Token != "" {
		r.Header.Set("Authorization", "Bearer "+p.Token)
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(r)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode != 200 {
		return nil, remoteError{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	if !json.Valid(b) {
		return nil, errors.New("invalid JSON response")
	}
	return b, nil
}
func watch(ctx context.Context, path string, p profile) error {
	var cursor int64
	state := path + ".cursor"
	b, e := os.ReadFile(state)
	if e == nil {
		cursor, e = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	delay := time.Second
	for ctx.Err() == nil {
		b, e := request(ctx, p, "watch?wait=50&after="+strconv.FormatInt(cursor, 10), nil)
		if e != nil {
			var httpErr remoteError
			if errors.As(e, &httpErr) && (httpErr.status == 401 || httpErr.status == 403) {
				return e
			}
			fmt.Fprintln(os.Stderr, e)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
			delay = min(30*time.Second, 2*delay)
			continue
		}
		delay = time.Second
		var v struct{ Cursor int64 }
		if e = json.Unmarshal(b, &v); e != nil {
			return e
		}
		if v.Cursor == cursor {
			continue
		}
		// Emit a coalesced fetch notification before advancing the durable cursor.
		if _, e = os.Stdout.Write(b); e != nil {
			return e
		}
		f, e := os.CreateTemp(filepath.Dir(state), ".cursor-*")
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(f, v.Cursor)
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e == nil {
			e = os.Rename(f.Name(), state)
		}
		if e != nil {
			os.Remove(f.Name())
			return e
		}
		cursor = v.Cursor
	}
	return nil
}

const usage = "openagentinbox-client [--seat NAME] whoami | authorization | onboard ORIGIN [--watch|--install-worker] | verify ORIGIN CURRENT_NAME [MAILBOX_ID] | use-name ORIGIN CURRENT_NAME MAILBOX_ID | bind ORIGIN --token-stdin | login ORIGIN | status ORIGIN | tickets ORIGIN DIRECTORY | join ORIGIN NAME [--ticket-stdin] | host-info ORIGIN | handshake ORIGIN DIRECTORY | inbox [BEFORE] | read ID | send TO SUBJECT BODY [UUID] | request|urgent TO SUBJECT BODY [UUID] | reply ID TO SUBJECT BODY [UUID] | receipt ID | alerts | request-action accept|defer|resolve|decline ID [RFC3339] | watch | notify help"

func run(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println("0.10.4")
		return nil
	}
	if len(args) == 0 || args[0] == "help" {
		fmt.Println(usage)
		return nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	path := os.Getenv("OPENAGENTINBOX_PROFILE")
	if path == "" {
		path = filepath.Join(home, ".config/openagentinbox/agent.json")
	}
	seat := ""
	if args[0] == "--seat" {
		if len(args) < 3 || !safeName.MatchString(args[1]) {
			return errors.New("expected --seat NAME and a seat command")
		}
		seat, args = args[1], args[2:]
		path = filepath.Join(home, ".config/openagentinbox/seats", seat+".json")
		switch args[0] {
		case "authorization", "notify", "whoami", "use-name", "bind", "verify", "inbox", "read", "send", "watch", "join":
		default:
			return errors.New("expected a seat command")
		}
	}
	cmd, a := args[0], args[1:]
	var p profile
	var body any
	var route string
	if cmd == "onboard" && len(a) == 2 && a[1] == "--install-worker" {
		return installWorker(ctx, home, a[0])
	}
	if cmd == "onboard" && (len(a) == 1 || len(a) == 2 && a[1] == "--watch") {
		return onboard(ctx, home, a[0], len(a) == 2)
	}
	if cmd == "login" && len(a) == 1 {
		return login(ctx, home, a[0])
	}
	if cmd == "status" && len(a) == 1 {
		if !validOrigin(a[0]) {
			return errors.New("invalid origin")
		}
		p, e = readProfile(hostPath(home, a[0]))
		if e != nil {
			return e
		}
		b, e := request(ctx, p, "pair/status", nil)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(b)
		return e
	}
	if cmd == "host-info" && len(a) == 1 {
		if !validOrigin(a[0]) {
			return errors.New("invalid origin")
		}
		host, e := os.Hostname()
		if e != nil {
			return e
		}
		b, e := request(ctx, profile{Origin: a[0]}, "peer", nil)
		if e != nil {
			return e
		}
		var reply struct{ IP string }
		if e = json.Unmarshal(b, &reply); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"hostname": host, "ip": reply.IP})
	}
	if (cmd == "handshake" || cmd == "tickets") && len(a) == 2 {
		if !validOrigin(a[0]) {
			return errors.New("invalid origin")
		}
		var b json.RawMessage
		var host string
		if cmd == "tickets" {
			p, e = readProfile(hostPath(home, a[0]))
			if e != nil {
				return e
			}
			host = p.Token
			b, e = request(ctx, p, "pair/tickets", struct{}{})
		} else {
			raw, err := io.ReadAll(io.LimitReader(os.Stdin, 256))
			if err != nil {
				return err
			}
			host = strings.TrimSpace(string(raw))
			localHost, err := os.Hostname()
			if err != nil {
				return err
			}
			b, e = request(ctx, profile{Origin: a[0], Token: host}, "handshake", map[string]string{"host": localHost})
		}
		if e != nil {
			return e
		}
		var grant struct{ Names []string }
		if e = json.Unmarshal(b, &grant); e != nil {
			return e
		}
		if e = os.MkdirAll(a[1], 0700); e != nil {
			return e
		}
		info, e := os.Lstat(a[1])
		if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("ticket directory must be private")
		}
		for _, n := range grant.Names {
			if !safeName.MatchString(n) {
				return errors.New("invalid name in roster")
			}
			mac := hmac.New(sha256.New, []byte(host))
			mac.Write([]byte(n))
			ticket := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
			dest := filepath.Join(a[1], n+".ticket")
			f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if os.IsExist(e) {
				old, e := os.ReadFile(dest)
				if e == nil && strings.TrimSpace(string(old)) == ticket {
					continue
				}
				return errors.New("conflicting ticket file: " + dest)
			}
			if e != nil {
				return e
			}
			_, e = fmt.Fprintln(f, ticket)
			ce := f.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		}
		fmt.Printf("Prepared %d per-agent tickets in %s\n", len(grant.Names), a[1])
		return nil
	}
	if cmd == "bind" {
		if seat == "" || len(a) != 2 || !validOrigin(a[0]) || a[1] != "--token-stdin" {
			return errors.New("use --seat NAME bind ORIGIN --token-stdin")
		}
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 256))
		if err != nil {
			return err
		}
		p = profile{Origin: a[0], Name: seat, Token: strings.TrimSpace(string(raw))}
		if !regexp.MustCompile("^[A-Za-z0-9_-]{43}$").MatchString(p.Token) {
			return errors.New("invalid credential")
		}
		if _, err = verify(ctx, p, seat, ""); err != nil {
			return err
		}
		return installProfile(path, p)
	}
	if cmd == "join" && (len(a) == 2 || len(a) == 3 && a[2] == "--ticket-stdin") {
		if seat != "" && a[1] != seat {
			return errors.New("join name differs from selected seat")
		}
		p, e = ensure(path, a[0], a[1])
		if e != nil {
			return e
		}
		q := map[string]string{"name": p.Name}
		if len(a) == 3 {
			b, e := io.ReadAll(io.LimitReader(os.Stdin, 256))
			if e != nil {
				return e
			}
			localHost, e := os.Hostname()
			if e != nil {
				return e
			}
			q["host"] = localHost
			q["ticket"] = strings.TrimSpace(string(b))
			if len(q["ticket"]) != 43 {
				return errors.New("expected one roster ticket on stdin")
			}
		}
		body = q
		route = "enroll"
	} else {
		p, e = readProfile(path)
		if e != nil {
			return e
		}
		if seat != "" && p.Name != seat {
			return errors.New("profile name differs from selected seat")
		}
		switch {
		case cmd == "use-name" && len(a) == 3:
			if seat == "" || p.Origin != a[0] || !safeName.MatchString(a[1]) || len(a[2]) != 32 {
				return errors.New("use --seat OLD use-name ORIGIN CURRENT_NAME MAILBOX_ID")
			}
			if _, e = verify(ctx, p, a[1], a[2]); e != nil {
				return e
			}
			p.Name = a[1]
			return installProfile(filepath.Join(filepath.Dir(path), p.Name+".json"), p)
		case cmd == "notify":
			return notifications(ctx, path, p, a)
		case cmd == "verify" && (len(a) == 2 || len(a) == 3):
			if p.Origin != a[0] {
				return errors.New("profile belongs to a different workspace")
			}
			id := ""
			if len(a) == 3 {
				id = a[2]
			}
			b, err := verify(ctx, p, a[1], id)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(b)
			return err
		case cmd == "authorization" && len(a) == 0:
			route = "authorization"
		case cmd == "whoami" && len(a) == 0:
			route = "whoami"
		case cmd == "watch" && len(a) == 0:
			return watch(ctx, path, p)
		case cmd == "inbox" && len(a) <= 1:
			route = "inbox"
			if len(a) == 1 {
				route += "?before=" + url.QueryEscape(a[0])
			}
		case cmd == "read" && len(a) == 1:
			route = "message/" + url.PathEscape(a[0])
		case cmd == "receipt" && len(a) == 1:
			route = "receipt/" + url.PathEscape(a[0])
		case cmd == "alerts" && len(a) == 0:
			route = "alerts"
		case cmd == "request-action" && (len(a) == 2 || len(a) == 3):
			var until int64
			if len(a) == 3 {
				t, err := time.Parse(time.RFC3339, a[2])
				if err != nil {
					return err
				}
				until = t.UnixMilli()
			}
			body = map[string]any{"action": a[0], "id": a[1], "until": until}
			route = "request"
		case (cmd == "send" || cmd == "request" || cmd == "urgent" || cmd == "reply") && (len(a) == 3 || len(a) == 4 || cmd == "reply" && len(a) == 5):
			parent := ""
			if cmd == "reply" {
				if len(a) < 4 {
					return errors.New("reply ID TO SUBJECT BODY [UUID]")
				}
				parent = a[0]
				a = a[1:]
			}
			if len(a) < 3 || len(a) > 4 {
				return errors.New(usage)
			}
			b := make([]byte, 16)
			rand.Read(b)
			b[6] = b[6]&15 | 64
			b[8] = b[8]&63 | 128
			h := hex.EncodeToString(b)
			k := h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
			if len(a) == 4 {
				k = a[3]
			}
			fmt.Fprintln(os.Stderr, "Retry key:", k)
			intent := "fyi"
			if cmd == "request" || cmd == "urgent" {
				intent = cmd
			}
			body = map[string]string{"to": a[0], "subject": a[1], "body": a[2], "key": k, "intent": intent, "replyTo": parent}
			route = "send"
		default:
			return errors.New(usage)
		}
	}
	b, e := request(ctx, p, route, body)
	if e != nil {
		return e
	}
	_, e = os.Stdout.Write(b)
	return e
}
func installProfile(path string, p profile) error {
	if existing, err := readProfile(path); err == nil && existing == p {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".bind-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(p)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("cannot install profile at %s: %w", path, err)
	}
	return nil
}

func verify(ctx context.Context, p profile, name, id string) ([]byte, error) {
	b, err := request(ctx, p, "whoami", nil)
	if err != nil {
		return nil, err
	}
	var info struct {
		Name string
		ID   string `json:"mailbox_id"`
	}
	if json.Unmarshal(b, &info) != nil || info.Name != name || len(info.ID) != 32 || id != "" && info.ID != id {
		return nil, errors.New("authenticated mailbox identity differs from the expected mapping")
	}
	return b, nil
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := run(ctx, os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
