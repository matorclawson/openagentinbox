package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"golang.org/x/crypto/scrypt"
)

type object = map[string]any
type failure struct {
	code int
	msg  string
}

func (f failure) Error() string { return f.msg }

type handler func(http.ResponseWriter, *http.Request) (any, error)
type server struct {
	resolver                                                     dnsResolver
	email                                                        *emailConfig
	performance                                                  mailPerformance
	trustedProxy                                                 string
	live                                                         changes
	peerLimit                                                    int
	db                                                           *sql.DB
	store                                                        *deliverystore.Store
	dir, origin, broker, app, audience, delegationType, operator string
	passwords                                                    chan struct{}
	mu                                                           sync.Mutex
	failures                                                     int
	window                                                       time.Time
}

var nameRE = regexp.MustCompile("^[a-z0-9][a-z0-9._-]{0,63}$")
var bearerRE = regexp.MustCompile("^[A-Za-z0-9_-]{43}$")

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func token() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func now() int64           { return time.Now().UnixMilli() }
func validOrigin(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && u.User == nil && s == u.Scheme+"://"+u.Host && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}
func input(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return failure{400, "Invalid JSON fields"}
	}
	return nil
}
func cookie(r *http.Request, k string) string {
	c, e := r.Cookie(k)
	if e != nil {
		return ""
	}
	return c.Value
}
func (s *server) set(w http.ResponseWriter, k, v string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: k, Value: v, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.origin, "https://"), MaxAge: seconds})
}

const schema = `
CREATE TABLE IF NOT EXISTS owner(id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL,salt TEXT,password TEXT,principal TEXT);
CREATE TABLE IF NOT EXISTS sessions(key TEXT PRIMARY KEY,expires INTEGER NOT NULL,delegation TEXT);
CREATE TABLE IF NOT EXISTS attempts(state TEXT PRIMARY KEY,browser TEXT,verifier TEXT,expires INTEGER,intent TEXT);
CREATE TABLE IF NOT EXISTS agents(name TEXT PRIMARY KEY,key TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS enrollments(code TEXT PRIMARY KEY,key TEXT UNIQUE NOT NULL,name TEXT NOT NULL,expires INTEGER NOT NULL,peer TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS enrollment_expiry ON enrollments(expires);
CREATE TABLE IF NOT EXISTS message_readers(id TEXT NOT NULL,seat TEXT NOT NULL,PRIMARY KEY(id,seat)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS messages_sender_seq ON messages(sender,seq);`

func Run() error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	port := env("INBOX_PORT", "4310")
	s := &server{dir: env("INBOX_DATA", filepath.Join(home, ".local/share/openagentinbox")), origin: env("INBOX_ORIGIN", "http://127.0.0.1:"+port), passwords: make(chan struct{}, 2), broker: os.Getenv("OPENPASSWORD_ORIGIN"), app: os.Getenv("OPENPASSWORD_APP"), audience: env("OPENPASSWORD_AUDIENCE", "openagentinbox"), delegationType: env("OPENPASSWORD_DELEGATION_TYPE", "openpassword-human-delegation+jwt"), operator: "operator"}
	limit, err := strconv.Atoi(env("INBOX_PENDING_PER_HOST", "8"))
	if err != nil || limit < 1 || limit > 512 {
		return errors.New("INBOX_PENDING_PER_HOST must be between 1 and 512")
	}
	s.peerLimit = limit
	if raw := os.Getenv("INBOX_TRUSTED_PROXY_IP"); raw != "" {
		ip, ok := literalIP(raw)
		if !ok {
			return errors.New("INBOX_TRUSTED_PROXY_IP requires a single proxy IP")
		}
		s.trustedProxy = ip.String()
	}
	listen := env("INBOX_LISTEN", "127.0.0.1")
	if !validOrigin(s.origin) || (listen != "127.0.0.1" && !(listen == "0.0.0.0" && strings.HasPrefix(s.origin, "https://"))) {
		return errors.New("invalid origin or bind address")
	}
	if (s.app == "") != (s.broker == "") || s.broker != "" && (!validOrigin(s.broker) || !strings.HasPrefix(s.origin, "https://")) {
		return errors.New("invalid OpenPassword configuration")
	}

	if e = os.MkdirAll(s.dir, 0700); e != nil {
		return e
	}
	p, e := filepath.Abs(filepath.Join(s.dir, "console.sqlite"))
	if e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	f.Close()
	if e = backupLegacy(p); e != nil {
		return e
	}
	s.store, e = deliverystore.Open(p)
	if e != nil {
		return e
	}
	defer s.store.Close()
	s.db = s.store.Writer()
	if _, e = s.db.Exec(schema + liveSchema + pairSchema + addressSchema + onboardingSchema + trackingSchema + emailSchema); e != nil {
		return e
	}
	if e = s.store.UpgradeConsole(context.Background()); e != nil {
		return e
	}
	if e = initThreads(s.db); e != nil {
		return e
	}
	if e = initMailSearch(s.db); e != nil {
		return e
	}
	var owners int
	if e = s.store.Reader().QueryRow("SELECT count(*) FROM owner").Scan(&owners); e != nil {
		return e
	}
	if owners == 0 {
		f, e := os.OpenFile(filepath.Join(s.dir, "setup-code"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			_, e = f.WriteString(token())
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		} else if !os.IsExist(e) {
			return e
		}
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	ui := env("INBOX_UI", filepath.Join(filepath.Dir(exe), "ui"))
	if _, e = os.Stat(filepath.Join(ui, "index.html")); e != nil {
		return fmt.Errorf("web assets: %w", e)
	}
	instance := os.Getenv("INBOX_DEPLOY_INSTANCE")
	if instance == "" {
		var b [16]byte
		rand.Read(b[:])
		instance = hex.EncodeToString(b[:])
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		var n int
		if err := s.store.Reader().QueryRowContext(r.Context(), "SELECT count(*) FROM owner").Scan(&n); err != nil {
			http.Error(w, "store unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(object{"product": "openagentinbox", "version": "0.12.6", "instance": instance, "setup": n == 0})
	})
	s.live.draining = make(chan struct{})
	s.routes(mux)
	for _, route := range []string{"GET /inboxes/{seat}", "GET /inboxes/{seat}/sent", "GET /messages/{id}", "GET /agents", "GET /activity", "GET /compose", "GET /mail-setup", "GET /settings", "GET /inboxes/{seat}/threads/{root}"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(ui, "index.html"))
		})
	}
	mux.Handle("/", http.FileServer(http.Dir(ui)))
	srv := http.Server{Addr: listen + ":" + port, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	closeEmail, err := s.startEmail(ctx)
	if err != nil {
		return err
	}
	defer closeEmail()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); s.deliveryWorker(ctx) }()
	defer func() { stop(); <-workerDone }()
	done := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		select {
		case <-ctx.Done():
			close(s.live.draining)
			c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			srv.Shutdown(c)
		case <-done:
		}
	}()
	log.Printf("OpenAgentInbox %s, setup code at %s", s.origin, filepath.Join(s.dir, "setup-code"))
	e = srv.ListenAndServe()
	close(done)
	<-drained
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func (s *server) routes(m *http.ServeMux) {
	add := func(route string, h handler) {
		m.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			r.Body = http.MaxBytesReader(w, r.Body, 240000)
			var e error
			var v any
			if r.Method == "POST" && !strings.HasPrefix(r.URL.Path, "/api/agent/") && r.Header.Get("Origin") != s.origin {
				e = failure{403, "Invalid origin"}
			}
			if e == nil && strings.HasPrefix(r.URL.Path, "/api/desk/") {
				var ok bool
				ok, e = s.signed(r)
				if e == nil && !ok {
					e = failure{401, "Sign in to continue"}
				}
			}
			if e == nil {
				v, e = h(w, r)
			}
			if e != nil {
				f, ok := e.(failure)
				if !ok {
					f = failure{503, "Service unavailable"}
					if errors.Is(e, sql.ErrNoRows) {
						f = failure{404, "Unknown record"}
					}
				}
				w.WriteHeader(f.code)
				v = object{"error": f.msg}
			}
			if v != nil {
				json.NewEncoder(w).Encode(v)
			}
		})
	}
	add("GET /api/session", s.session)
	add("POST /api/login", s.password)
	add("POST /api/setup", s.password)
	add("POST /api/logout", s.logout)
	add("POST /api/connect", s.connect)
	add("GET /api/callback", s.callback)
	m.HandleFunc("/mcp", s.mcp)
	s.liveRoutes(add)
	s.pairRoutes(add)
	s.mailRoutes(add)
	add("GET /api/desk/performance", func(_ http.ResponseWriter, _ *http.Request) (any, error) { return s.performance.snapshot(), nil })
	s.enrollRoutes(add)
}
func (s *server) issue(w http.ResponseWriter, exp int64, delegation any) error {
	t := token()
	if _, e := s.db.Exec("DELETE FROM sessions WHERE expires<=?", now()); e != nil {
		return e
	}
	_, e := s.db.Exec("INSERT INTO sessions VALUES(?,?,?)", hash(t), exp, delegation)
	if e == nil {
		s.set(w, "oai_session", t, int((exp-now()+999)/1000))
	}
	return e
}
func (s *server) signed(r *http.Request) (bool, error) {
	var d sql.NullString
	e := s.store.Reader().QueryRowContext(r.Context(), "SELECT delegation FROM sessions WHERE key=? AND expires>?", hash(cookie(r, "oai_session")), now()).Scan(&d)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !d.Valid {
		return true, nil
	}
	return s.active(r, d.String)
}
func (s *server) session(_ http.ResponseWriter, r *http.Request) (any, error) {
	var name string
	var p sql.NullString
	e := s.store.Reader().QueryRow("SELECT username,principal FROM owner").Scan(&name, &p)
	setup := errors.Is(e, sql.ErrNoRows)
	if e != nil && !setup {
		return nil, e
	}
	ok, e := s.signed(r)
	var account any
	if ok {
		account = name
	}
	return object{"setup": setup, "name": account, "linked": p.Valid, "openpassword": s.broker != ""}, e
}
func (s *server) setupCode(code string) bool {
	b, e := os.ReadFile(filepath.Join(s.dir, "setup-code"))
	return e == nil && code != "" && subtle.ConstantTimeCompare([]byte(hash(code)), []byte(hash(strings.TrimSpace(string(b))))) == 1
}
func (s *server) password(w http.ResponseWriter, r *http.Request) (any, error) {
	var q struct{ Username, Password, Code string }
	if e := input(r, &q); e != nil {
		return nil, e
	}
	q.Username = strings.TrimSpace(q.Username)
	if len(q.Username) == 0 || len(q.Username) > 64 || len(q.Password) < 12 || len(q.Password) > 256 {
		return nil, failure{400, "Invalid account fields"}
	}
	create := r.URL.Path == "/api/setup"
	if create && !s.setupCode(q.Code) {
		return nil, failure{403, "Invalid setup code"}
	}
	s.mu.Lock()
	if time.Now().After(s.window) {
		s.window = time.Now().Add(time.Minute)
		s.failures = 0
	}
	limited := s.failures >= 12
	s.mu.Unlock()
	if limited {
		return nil, failure{429, "Try again in one minute"}
	}
	select {
	case s.passwords <- struct{}{}:
		defer func() { <-s.passwords }()
	default:
		return nil, failure{429, "Try again shortly"}
	}
	var user string
	var salt, pass sql.NullString
	e := s.store.Reader().QueryRow("SELECT username,salt,password FROM owner").Scan(&user, &salt, &pass)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if create && e == nil {
		return nil, failure{409, "Setup already complete"}
	}
	if create {
		salt.String = token()
	}
	key, e := scrypt.Key([]byte(q.Password), []byte(salt.String), 16384, 8, 1, 64)
	if e != nil {
		return nil, e
	}
	encoded := hex.EncodeToString(key)
	if create {
		_, e = s.db.Exec("INSERT INTO owner VALUES(1,?,?,?,NULL)", q.Username, salt.String, encoded)
		if e != nil {
			return nil, e
		}
	} else if subtle.ConstantTimeCompare([]byte(encoded), []byte(pass.String)) != 1 || user != q.Username {
		s.mu.Lock()
		s.failures++
		s.mu.Unlock()
		return nil, failure{401, "Invalid account or password"}
	}
	return object{"ok": true}, s.issue(w, now()+8*3600000, nil)
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) (any, error) {
	_, e := s.db.Exec("DELETE FROM sessions WHERE key=?", hash(cookie(r, "oai_session")))
	s.set(w, "oai_session", "", -1)
	return object{"ok": true}, e
}
