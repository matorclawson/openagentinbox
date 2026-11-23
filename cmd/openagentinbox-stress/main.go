// Command openagentinbox-stress measures the public messaging API on disposable local data.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Daemon, Backup, Scenario                 string
	Rate, Seats, Workers, History, BodyBytes int
	Duration, Cooldown, MaxSendP95           time.Duration
	MinWindowPercent                         float64
	MaxRSSMiB                                int
}
type bench struct {
	config
	dir, origin, path string
	keys              []string
	client            *http.Client
	proc              *exec.Cmd
	log               *os.File
}

func name(i int) string { return fmt.Sprintf("agent-%04d", i) }
func (b *bench) start() error {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()
	b.origin = fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
	instance := uuid.NewString()
	c := exec.Command(b.Daemon, "serve")
	c.Env = []string{"HOME=" + b.dir, "PATH=" + os.Getenv("PATH"), "INBOX_DATA=" + b.dir, "INBOX_DEPLOY_INSTANCE=" + instance, "INBOX_ORIGIN=" + b.origin, "INBOX_UI=" + b.dir, "INBOX_PORT=" + strconv.Itoa(addr.Port), "GOMAXPROCS=" + strconv.Itoa(runtime.GOMAXPROCS(0))}
	c.Stdout = b.log
	c.Stderr = b.log
	if e = c.Start(); e != nil {
		return e
	}
	b.proc = c
	for start := time.Now(); time.Since(start) < 25*time.Second; {
		var h struct{ Product, Instance string }
		if b.api("", "/api/health", nil, &h) == nil && h.Product == "openagentinbox" && h.Instance == instance {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("disposable daemon did not become ready")
}
func (b *bench) stop() error {
	if b.proc == nil {
		return nil
	}
	c := b.proc
	b.proc = nil
	_ = c.Process.Signal(syscall.SIGTERM)
	ch := make(chan error, 1)
	go func() { ch <- c.Wait() }()
	select {
	case e := <-ch:
		return e
	case <-time.After(20 * time.Second):
		c.Process.Kill()
		<-ch
		return errors.New("daemon shutdown timed out")
	}
}
func (b *bench) api(token, path string, body, out any) error {
	method := "GET"
	var data io.Reader
	if body != nil {
		method = "POST"
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		data = strings.NewReader(string(raw))
	}
	req, e := http.NewRequest(method, b.origin+path, data)
	if e != nil {
		return e
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := b.client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
func (b *bench) seed() error {
	s, e := deliverystore.Open(b.path)
	if e != nil {
		return e
	}
	defer s.Close()
	for i := 0; i < b.History; {
		batch := make([]deliverystore.Message, 0, 256)
		for len(batch) < 256 && i < b.History {
			batch = append(batch, deliverystore.Message{ID: fmt.Sprintf("history-%d", i), Sender: "archive", Subject: "Archived", Priority: 1, Body: []byte(strings.Repeat("h", 256)), Recipients: []string{name(i % b.Seats)}})
			i++
		}
		if e = s.AcceptBatch(batch, time.Now()); e != nil {
			return e
		}
	}
	return nil
}
func execute(c config) (r result, err error) {
	dir, e := os.MkdirTemp("", "openagentinbox-stress-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(dir)
	log, e := os.Create(filepath.Join(dir, "daemon.log"))
	if e != nil {
		return r, e
	}
	defer log.Close()
	transport := &http.Transport{MaxIdleConns: c.Workers * 2, MaxIdleConnsPerHost: c.Workers * 2}
	defer transport.CloseIdleConnections()
	b := bench{config: c, dir: dir, path: filepath.Join(dir, "console.sqlite"), log: log, client: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
	if e = os.WriteFile(filepath.Join(dir, "index.html"), []byte("stress fixture"), 0600); e != nil {
		return r, e
	}
	if e = b.seed(); e != nil {
		return r, e
	}
	defer b.stop()
	if e = b.start(); e != nil {
		return r, e
	}
	if e = b.accounts(); e != nil {
		return r, e
	}
	return b.load()
}
func main() {
	c := config{}
	var rates, scenarios, out string
	flag.StringVar(&c.Daemon, "daemon", "", "path to openagentinboxd")
	flag.StringVar(&c.Backup, "backup", "", "path to openagentinbox-backup (maintenance scenario)")
	flag.StringVar(&rates, "rates", "100,1000,10000", "comma-separated offered messages per minute")
	flag.StringVar(&scenarios, "scenarios", "mixed,hot,maintenance", "mixed, hot, maintenance, burst, requests or crash")
	flag.DurationVar(&c.Duration, "duration", time.Minute, "per matrix cell")
	flag.DurationVar(&c.Cooldown, "cooldown", 0, "record idle memory after each load window")
	flag.DurationVar(&c.MaxSendP95, "max-send-p95", 0, "optional maximum HTTP send p95")
	flag.Float64Var(&c.MinWindowPercent, "min-window-percent", 0, "minimum percentage of scheduled sends confirmed inside the window")
	flag.IntVar(&c.MaxRSSMiB, "max-rss-mib", 0, "optional daemon peak RSS budget")
	flag.IntVar(&c.Workers, "workers", 32, "maximum concurrent workflows")
	flag.IntVar(&c.Seats, "seats", 300, "disposable agents")
	flag.IntVar(&c.History, "history", 10000, "historical messages before each run")
	flag.IntVar(&c.BodyBytes, "body-bytes", 512, "message body bytes")
	flag.StringVar(&out, "out", "", "new JSONL report path; default stdout")
	flag.Parse()
	die := func(e any) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
	if runtime.GOOS != "linux" || c.Seats < 2 || c.Seats > 100000 || c.Workers < 1 || c.Workers > 4096 || c.Duration < time.Second || c.Duration > 7*24*time.Hour || c.History < 0 || c.BodyBytes < 1 || c.BodyBytes > 48000 {
		die("requires Linux, 2–100000 seats, 1–4096 workers, 1s–7d duration and 1–48000 body bytes")
	}
	if c.Cooldown < 0 || c.Cooldown > time.Hour || c.MaxSendP95 < 0 || math.IsNaN(c.MinWindowPercent) || c.MinWindowPercent < 0 || c.MinWindowPercent > 100 || c.MaxRSSMiB < 0 {
		die("invalid cooldown or budget")
	}
	var cases []config
	for _, s := range strings.Split(scenarios, ",") {
		if s != "mixed" && s != "hot" && s != "maintenance" && s != "burst" && s != "requests" && s != "crash" {
			die("unknown scenario " + s)
		}
		c.Scenario = s
		for _, raw := range strings.Split(rates, ",") {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 1000000 {
				die("invalid rate")
			}
			c.Rate = n
			if int64(n)*int64(c.Duration/time.Millisecond) < 60000 {
				die("duration too short for rate")
			}
			cases = append(cases, c)
		}
	}
	for i := range cases {
		for _, path := range []*string{&cases[i].Daemon, &cases[i].Backup} {
			if path == &cases[i].Backup && cases[i].Scenario != "maintenance" {
				continue
			}
			p, e := filepath.Abs(*path)
			if e != nil {
				die(e)
			}
			st, e := os.Stat(p)
			if e != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
				die("missing executable: " + p)
			}
			*path = p
		}
	}
	if _, e := os.Stat("/proc/self/status"); e != nil {
		die(e)
	}
	f := os.Stdout
	if out != "" {
		var e error
		f, e = os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			die(e)
		}
		defer f.Close()
	}
	bad := false
	for _, tc := range cases {
		fmt.Fprintf(os.Stderr, "%s @ %d/min for %s\n", tc.Scenario, tc.Rate, tc.Duration)
		r, e := execute(tc)
		r.Config = tc
		if e != nil {
			r.Failure = e.Error()
		}
		fmt.Fprintf(os.Stderr, "confirmed in window %d/%d; persisted %d; peak %.1f MiB; send p95 %.2f ms; %s\n", r.ConfirmedInWindow, r.Scheduled, r.Persisted, float64(r.PeakRSSKiB)/1024, r.Latency["send"].P95ms, r.Failure)
		if r.Failure != "" {
			bad = true
		}
		if e = json.NewEncoder(f).Encode(r); e != nil {
			die(e)
		}
		if out != "" {
			if e = f.Sync(); e != nil {
				die(e)
			}
		}
	}
	if bad {
		os.Exit(2)
	}
}
