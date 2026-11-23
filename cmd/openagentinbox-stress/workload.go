package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rodriveracom/dostoevsky/sketch"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type latency struct {
	Count                      int64
	P50ms, P95ms, P99ms, MaxMs float64
}
type hist struct {
	samples sketch.LatencyHistogram
	max     time.Duration
}

func (h *hist) add(d time.Duration) {
	h.samples.Observe(d)
	h.max = max(h.max, d)
}
func (h *hist) summary() latency {
	ms := func(p int) float64 {
		bound, _ := h.samples.PercentileUpper(p)
		return float64(min(bound, h.max)) / float64(time.Millisecond)
	}
	return latency{int64(h.samples.Count()), ms(50), ms(95), ms(99), float64(h.max) / float64(time.Millisecond)}
}

type result struct {
	CorpusRows, CorpusDeliveries int `json:",omitempty"`
	StoredByInbox                map[string]int

	IdleSamples                                                                    []sample `json:",omitempty"`
	Config                                                                         config
	Failure                                                                        string
	Scheduled, Dropped, Confirmed, ConfirmedInWindow, WorkflowErrors, PeakInFlight int
	WindowRate, DrainedRate, DrainSeconds, CPUPercentOneCore                       float64
	BaselineRSSKiB, PeakRSSKiB, EndRSSKiB, PeakWALBytes                            int64
	Latency                                                                        map[string]latency
	Errors                                                                         map[string]int
	Samples                                                                        []sample
	Persisted                                                                      int
	Backup                                                                         *snapshot
	RestartMS                                                                      float64
}
type snapshot struct {
	PeakRSSKiB                  int64
	CPUSeconds                  float64
	Bytes, Messages, Deliveries int64
	Seconds                     float64
}

// A commutative fingerprint checks every acknowledged ID without retaining one
// object per message during a multi-hour soak. Keys are unique per scheduled send.
type fingerprint [sha256.Size]byte

func (f *fingerprint) add(key, id string) {
	d := sha256.Sum256([]byte(key + "/" + id))
	for i, v := range d {
		f[i] ^= v
	}
}

type metrics struct {
	acceptedIDs fingerprint
	firstID     string
	sync.Mutex
	hist                                      map[string]*hist
	errors                                    map[string]int
	active, peak, confirmed, inWindow, failed int
}

func (m *metrics) observe(k string, d time.Duration) {
	m.Lock()
	defer m.Unlock()
	h := m.hist[k]
	if h == nil {
		h = &hist{}
		m.hist[k] = h
	}
	h.add(d)
}
func (b *bench) accounts() error {
	code, e := os.ReadFile(filepath.Join(b.dir, "setup-code"))
	if e != nil {
		return e
	}
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	post := func(path string, payload, out any) error {
		raw, e := json.Marshal(payload)
		if e != nil {
			return e
		}
		req, e := http.NewRequest("POST", b.origin+path, bytes.NewReader(raw))
		if e != nil {
			return e
		}
		req.Header.Set("Origin", b.origin)
		req.Header.Set("Content-Type", "application/json")
		res, e := admin.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("fixture %s: HTTP %d", path, res.StatusCode)
		}
		return json.NewDecoder(res.Body).Decode(out)
	}
	if e = post("/api/setup", map[string]string{"code": strings.TrimSpace(string(code)), "username": "bench", "password": "disposable-bench-owner"}, &map[string]any{}); e != nil {
		return e
	}
	for i := 0; i < b.Seats; i++ {
		var res struct{ Credential string }
		if e = post("/api/desk/agents", map[string]string{"name": name(i)}, &res); e != nil {
			return e
		}
		if res.Credential == "" {
			return errors.New("empty fixture credential")
		}
		b.keys = append(b.keys, res.Credential)
	}
	return nil
}
func (b *bench) route(i int) (int, int) {
	src := i % b.Seats
	dst := (src + 1 + (i/b.Seats)%(b.Seats-1)) % b.Seats
	if len(b.shapes) > 0 {
		sh := b.liveShape(i)
		src = sh.src
		dst = sh.dst[i%len(sh.dst)]
	}
	if b.Scenario == "hot" {
		src = 1 + i%(b.Seats-1)
		dst = 0
	}
	return src, dst
}
func key(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012x", i) }
func (b *bench) work(i int, scheduled, end time.Time, body string, m *metrics) error {
	src, dst := b.route(i)
	body = b.liveBody(i, body)
	subject := fmt.Sprintf("load-%d", i)
	payload := map[string]string{"to": name(dst), "key": key(i), "subject": subject, "body": body}
	if b.Scenario == "requests" {
		payload["intent"] = "request"
	}
	var id struct{ ID string }
	start := time.Now()
	if e := b.api(b.keys[src], "/api/agent/send", payload, &id); e != nil {
		return fmt.Errorf("send: %w", e)
	}
	if id.ID == "" {
		return errors.New("send returned no ID")
	}
	accepted := time.Now()
	m.observe("send", accepted.Sub(start))
	m.observe("scheduled_to_send", accepted.Sub(scheduled))
	m.Lock()
	m.confirmed++
	m.acceptedIDs.add(key(i), id.ID)
	if i == 0 {
		m.firstID = id.ID
	}
	if !accepted.After(end) {
		m.inWindow++
	}
	m.Unlock()
	if i%10 == 0 {
		var retry struct{ ID string }
		start = time.Now()
		if e := b.api(b.keys[src], "/api/agent/send", payload, &retry); e != nil {
			return fmt.Errorf("retry: %w", e)
		}
		if retry.ID != id.ID {
			return errors.New("retry changed ID")
		}
		m.observe("retry", time.Since(start))
	}
	start = time.Now()
	var detail struct{ ID, Body, Src, Dst string }
	if e := b.api(b.keys[dst], "/api/agent/message/"+id.ID, nil, &detail); e != nil {
		return fmt.Errorf("read: %w", e)
	}
	if detail.ID != id.ID || detail.Body != body || detail.Src != name(src) || detail.Dst != name(dst) {
		return errors.New("read mismatch")
	}
	m.observe("read", time.Since(start))
	if b.Scenario == "requests" {
		start = time.Now()
		var state struct{ State string }
		if err := b.api(b.keys[dst], "/api/agent/request", map[string]string{"id": id.ID, "action": "resolve"}, &state); err != nil {
			return err
		}
		var receipt struct {
			Opened int64
			State  string
		}
		if err := b.api(b.keys[src], "/api/agent/receipt/"+id.ID, nil, &receipt); err != nil {
			return err
		}
		if receipt.Opened <= 0 || receipt.State != "resolved" {
			return errors.New("receipt mismatch")
		}
		m.observe("resolve_and_check", time.Since(start))
	}
	if i%4 == 0 {
		var page struct{ Rows []struct{ ID string } }
		start = time.Now()
		listPath := "/api/agent/inbox"
		if len(b.shapes) > 0 {
			listPath = "/api/agent/threads?folder=inbox&preview=1"
		}
		if e := b.api(b.keys[dst], listPath, nil, &page); e != nil {
			return fmt.Errorf("list: %w", e)
		}
		if len(page.Rows) == 0 || len(page.Rows) > 40 {
			return errors.New("invalid inbox page")
		}
		m.observe("list", time.Since(start))
	}
	m.observe("workflow_from_schedule", time.Since(scheduled))
	return nil
}
func (b *bench) backup() (s snapshot, e error) {
	path := filepath.Join(b.dir, "snapshot.db")
	start := time.Now()
	c := exec.Command(b.Backup, "-db", b.path, "-out", path)
	c.Env = []string{"HOME=" + b.dir, "PATH=" + os.Getenv("PATH")}
	c.Stderr = b.log
	raw, e := c.Output()
	if e != nil {
		return s, e
	}
	s.Seconds = time.Since(start).Seconds()
	if c.ProcessState != nil {
		s.PeakRSSKiB = c.ProcessState.SysUsage().(*syscall.Rusage).Maxrss
		s.CPUSeconds = (c.ProcessState.UserTime() + c.ProcessState.SystemTime()).Seconds()
	}
	if e = json.Unmarshal(raw, &s); e != nil {
		return s, e
	}
	if s.Messages < int64(b.History) || s.Deliveries-s.Messages != int64(b.historyDeliveries-b.History) || s.Bytes < 1 {
		return s, errors.New("inconsistent snapshot manifest")
	}
	return s, nil
}
func (b *bench) verify(n int, body string, accepted fingerprint, byInbox map[string]int) error {
	db, e := sql.Open("sqlite", b.path)
	if e != nil {
		return e
	}
	defer db.Close()
	var ok string
	if e = db.QueryRow("PRAGMA integrity_check").Scan(&ok); e != nil {
		return e
	}
	if ok != "ok" {
		return errors.New("database integrity check failed")
	}
	var total, del, ops int
	if e = db.QueryRow("SELECT (SELECT count(*) FROM messages),(SELECT count(*) FROM deliveries),(SELECT count(*) FROM operations)").Scan(&total, &del, &ops); e != nil {
		return e
	}
	if total != b.History+n || del != b.historyDeliveries+n || ops != n {
		return fmt.Errorf("durability mismatch: messages=%d deliveries=%d operations=%d expected new=%d", total, del, ops, n)
	}
	rows, e := db.Query(`SELECT m.id,m.subject,m.sender,d.seat,o.op_key,m.body FROM operations o JOIN messages m ON m.id=o.message_id JOIN deliveries d USING(seq)`)
	if e != nil {
		return e
	}
	defer rows.Close()
	var stored fingerprint
	checked := 0
	for rows.Next() {
		var id, sub, src, dst, op, persistedBody string
		var i int
		if e = rows.Scan(&id, &sub, &src, &dst, &op, &persistedBody); e != nil {
			return e
		}
		if _, e = fmt.Sscanf(sub, "load-%d", &i); e != nil {
			return e
		}
		if i < 0 || persistedBody != b.liveBody(i, body) {
			return errors.New("stored payload mismatch")
		}
		a, z := b.route(i)
		if i < 0 || src != name(a) || dst != name(z) || op != "console/"+src+"/"+key(i) {
			return errors.New("stored route or operation mismatch")
		}
		byInbox[dst]++
		stored.add(key(i), id)
		checked++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if stored != accepted {
		return errors.New("persisted message IDs differ from confirmed sends")
	}
	if checked != n {
		return fmt.Errorf("verified %d persisted payloads, expected %d", checked, n)
	}
	return nil
}

type job struct {
	i   int
	due time.Time
}

// Split whole minutes to keep the offset safe on multi-day soak tests.
func offset(i, rate int, burst bool) time.Duration {
	steady := time.Duration(i/rate)*time.Minute + time.Duration(int64(i%rate)*int64(time.Minute)/int64(rate))
	if !burst {
		return steady
	}
	const cycle = 5 * time.Second
	return steady/cycle*cycle + (steady%cycle)/5
}
func (b *bench) load() (r result, err error) {
	if len(b.shapes) > 0 {
		r.CorpusRows = len(b.shapes)
		r.CorpusDeliveries = b.historyDeliveries
	}
	ticks, e := clockTicks()
	if e != nil {
		return r, e
	}
	body := strings.Repeat("x", b.BodyBytes)
	m := metrics{hist: map[string]*hist{}, errors: map[string]int{}}
	n := int(int64(b.Rate) * int64(b.Duration/time.Millisecond) / 60000)
	r.Scheduled = n
	start := time.Now()
	end := start.Add(b.Duration)
	base, e := b.sample(start)
	if e != nil {
		return r, e
	}
	r.BaselineRSSKiB = base.RSSKiB
	done := make(chan struct{})
	sampleDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		r.Samples = append(r.Samples, base)
		for {
			select {
			case <-done:
				sampleDone <- nil
				return
			case <-ticker.C:
				s, e := b.sample(start)
				if e != nil {
					sampleDone <- e
					return
				}
				r.Samples = append(r.Samples, s)
			}
		}
	}()
	type backupResult struct {
		s snapshot
		e error
	}
	var snapDone chan backupResult
	if b.Scenario == "maintenance" {
		snapDone = make(chan backupResult, 1)
		go func() {
			time.Sleep(time.Until(start.Add(b.Duration / 2)))
			s, e := b.backup()
			snapDone <- backupResult{s, e}
		}()
	}
	queue := b.Queue
	if queue == 0 {
		queue = b.Workers
	}
	jobs := make(chan job, queue)
	var wg sync.WaitGroup
	for k := 0; k < b.Workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				m.Lock()
				m.active++
				if m.active > m.peak {
					m.peak = m.active
				}
				m.Unlock()
				m.observe("dispatch_lag", time.Since(j.due))
				e := b.work(j.i, j.due, end, body, &m)
				m.Lock()
				m.active--
				if e != nil {
					m.failed++
					msg := e.Error()
					if len(m.errors) > 20 {
						msg = "other errors"
					}
					m.errors[msg]++
				}
				m.Unlock()
			}
		}()
	}
	for i := 0; i < n; i++ {
		delay := offset(i, b.Rate, b.Scenario == "burst")
		if b.Scenario == "trace" {
			// Normalize each complete capture to its target-rate duration.
			// Entire cycles repeat in source order. Partial windows use a prefix.
			count := len(b.shapes)
			cycle := offset(count, b.Rate, false)
			span := b.shapes[count-1].at.Sub(b.shapes[0].at)
			fraction := float64(b.shapes[i%count].at.Sub(b.shapes[0].at)) / float64(span)
			delay = time.Duration(i/count)*cycle + time.Duration(fraction*float64(cycle))
		}
		due := start.Add(delay)
		time.Sleep(time.Until(due))
		if time.Now().After(end) {
			r.Dropped += n - i
			break
		}
		select {
		case jobs <- job{i, due}:
		default:
			r.Dropped++
		}
	}
	close(jobs)
	time.Sleep(time.Until(end))
	wg.Wait()
	finished := time.Now()
	close(done)
	if e = <-sampleDone; e != nil {
		return r, e
	}
	last, e := b.sample(start)
	if e != nil {
		return r, e
	}
	r.Samples = append(r.Samples, last)
	r.EndRSSKiB = last.RSSKiB
	r.PeakRSSKiB = last.HWMKiB
	for _, s := range r.Samples {
		if s.WALBytes > r.PeakWALBytes {
			r.PeakWALBytes = s.WALBytes
		}
	}
	r.CPUPercentOneCore = float64(last.CPUTicks-base.CPUTicks) / ticks / (last.Seconds - base.Seconds) * 100
	r.Confirmed = m.confirmed
	r.ConfirmedInWindow = m.inWindow
	r.WorkflowErrors = m.failed
	r.Errors = m.errors
	r.PeakInFlight = m.peak
	r.DrainSeconds = finished.Sub(end).Seconds()
	r.WindowRate = float64(m.inWindow) * 60 / b.Duration.Seconds()
	r.DrainedRate = float64(m.confirmed) * 60 / finished.Sub(start).Seconds()
	r.Latency = map[string]latency{}
	for k, h := range m.hist {
		r.Latency[k] = h.summary()
	}
	if snapDone != nil {
		backup := <-snapDone
		r.Backup = &backup.s
		if e = backup.e; e != nil {
			return r, fmt.Errorf("backup: %w", e)
		}
	}
	if b.Cooldown > 0 {
		deadline := time.Now().Add(b.Cooldown)
		for time.Now().Before(deadline) {
			time.Sleep(min(time.Second, time.Until(deadline)))
			idle, e := b.sample(start)
			if e != nil {
				return r, e
			}
			r.IdleSamples = append(r.IdleSamples, idle)
		}
	}
	if b.Scenario == "crash" {
		if e = b.proc.Process.Kill(); e != nil {
			return r, e
		}
		_ = b.proc.Wait()
		b.proc = nil
	} else if e = b.stop(); e != nil {
		return r, e
	}
	if b.Scenario == "maintenance" || b.Scenario == "crash" {
		t := time.Now()
		if e = b.start(); e != nil {
			return r, e
		}
		r.RestartMS = float64(time.Since(t)) / 1e6
		body = b.liveBody(0, body)
		src, dst := b.route(0)
		var persisted struct{ ID string }
		// A replay after restart must resolve the original operation. The final
		// count below also rejects a replay that silently created another row.
		payload := map[string]string{"to": name(dst), "key": key(0), "subject": "load-0", "body": body}
		if e = b.api(b.keys[src], "/api/agent/send", payload, &persisted); e != nil {
			return r, fmt.Errorf("restart replay: %w", e)
		}
		var detail struct{ ID, Body string }
		if e = b.api(b.keys[dst], "/api/agent/message/"+persisted.ID, nil, &detail); e != nil {
			return r, fmt.Errorf("restart read: %w", e)
		}
		if persisted.ID == "" || persisted.ID != m.firstID || detail.ID != persisted.ID || detail.Body != body {
			return r, errors.New("restart payload mismatch")
		}
		if e = b.stop(); e != nil {
			return r, e
		}
	}
	r.StoredByInbox = make(map[string]int, b.Seats)
	for i := 0; i < b.Seats; i++ {
		r.StoredByInbox[name(i)] = 0
	}
	if e = b.verify(m.confirmed, body, m.acceptedIDs, r.StoredByInbox); e != nil {
		return r, e
	}
	r.Persisted = m.confirmed
	if r.Dropped != 0 || m.failed != 0 || m.confirmed != n {
		return r, errors.New("offered workload not completed cleanly")
	}
	return r, budgets(b.config, r)
}
func budgets(c config, r result) error {
	if r.Scheduled > 0 && float64(r.ConfirmedInWindow)*100/float64(r.Scheduled) < c.MinWindowPercent {
		return errors.New("in-window throughput budget missed")
	}
	if c.MaxRSSMiB > 0 && float64(r.PeakRSSKiB)/1024 > float64(c.MaxRSSMiB) {
		return errors.New("daemon RSS budget exceeded")
	}
	if c.MaxSendP95 > 0 && r.Latency["send"].P95ms > float64(c.MaxSendP95)/1e6 {
		return errors.New("send p95 budget exceeded")
	}
	return nil
}
