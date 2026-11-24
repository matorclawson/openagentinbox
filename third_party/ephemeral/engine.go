// Package ephemeral runs durable, versioned workflows on a local SQLite database.
package ephemeral

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const runnableStates = "state IN ('ready','timer','leased','pending','join_ready')"

var schema = []string{
	"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON",
	`CREATE TABLE IF NOT EXISTS definitions(name TEXT,version INTEGER,spec TEXT NOT NULL,PRIMARY KEY(name,version))`,
	`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,name TEXT,version INTEGER,queue TEXT,state TEXT,step INTEGER DEFAULT 0,attempt INTEGER DEFAULT 0,due INTEGER DEFAULT 0,token TEXT DEFAULT '',value TEXT,FOREIGN KEY(name,version) REFERENCES definitions)`,
	`CREATE TABLE IF NOT EXISTS start_requests(run TEXT PRIMARY KEY REFERENCES runs, digest TEXT NOT NULL)`,
	"CREATE INDEX IF NOT EXISTS runnable_due ON runs(queue,due,id) WHERE " + runnableStates,
	`CREATE INDEX IF NOT EXISTS by_state ON runs(state,id)`,
	`DROP INDEX IF EXISTS ready`,
	`CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY,run TEXT REFERENCES runs,at INTEGER,kind TEXT,step INTEGER,data TEXT)`,
	`CREATE INDEX IF NOT EXISTS timeline ON events(run,seq)`,
	`CREATE TABLE IF NOT EXISTS attrs(run TEXT REFERENCES runs,k TEXT,v TEXT,PRIMARY KEY(run,k))`,
	`CREATE INDEX IF NOT EXISTS lookup ON attrs(k,v,run)`,
	`CREATE TABLE IF NOT EXISTS signals(run TEXT REFERENCES runs,id TEXT,name TEXT,value TEXT,used INTEGER DEFAULT 0,PRIMARY KEY(run,id))`,
	`CREATE INDEX IF NOT EXISTS inbox ON signals(run,name,used)`,
	`CREATE TABLE IF NOT EXISTS children(parent TEXT REFERENCES runs, step INTEGER, pos INTEGER, child TEXT UNIQUE REFERENCES runs, PRIMARY KEY(parent,step,pos))`,
}

type Step struct {
	RetryDelays   []int64           `json:"retry_delays_ms,omitempty"`
	Label         string            `json:"label,omitempty"`
	Next          string            `json:"next,omitempty"`
	Field         string            `json:"field,omitempty"`
	Cases         map[string]string `json:"cases,omitempty"`
	Default       string            `json:"default,omitempty"`
	Children      []Child           `json:"children,omitempty"`
	Kind          string            `json:"kind"`
	Name          string            `json:"name,omitempty"`
	Millis        int64             `json:"millis,omitempty"`
	Attempts      int               `json:"attempts,omitempty"`
	BackoffMillis int64             `json:"backoff_ms,omitempty"`
}
type Task struct {
	Run, Activity, Token string
	Step, Attempt        int
	Input                json.RawMessage
}

var ErrMissing = errors.New("run does not exist")

func num(v string) int                            { i, _ := strconv.Atoi(v); return i }
func now() int64                                  { return time.Now().UnixMilli() }
func (s *Store) exec(q string, args ...any) error { _, e := s.query(q, args...); return e }
func (s *Store) Register(name string, version int, steps []Step) error {
	if name == "" || version < 1 || len(steps) == 0 {
		return errors.New("name, positive version and steps required")
	}
	if e := validateGraph(steps); e != nil {
		return e
	}
	for _, x := range steps {
		if len(x.RetryDelays) > 0 {
			if x.Kind != "activity" || len(x.RetryDelays) != x.Attempts-1 || x.BackoffMillis != 0 {
				return errors.New("retry_delays_ms needs one delay per retry and zero fixed backoff")
			}
			for _, d := range x.RetryDelays {
				if d < 0 || d > 315360000000 {
					return errors.New("retry delay outside supported range")
				}
			}
		}
		switch x.Kind {
		case "activity", "signal":
			if x.Name == "" {
				return errors.New("step name required")
			}
		case "timer", "switch", "child", "parallel":
		default:
			return fmt.Errorf("unknown kind %q", x.Kind)
		}
		if x.Millis < 0 || x.Millis > 315360000000 || x.BackoffMillis < 0 || x.BackoffMillis > 315360000000 || x.Attempts < 0 {
			return errors.New("invalid timer or retry policy")
		}
	}
	b, _ := json.Marshal(steps)
	return s.tx(func() error {
		r, e := s.query("SELECT spec FROM definitions WHERE name=? AND version=?", name, version)
		if e != nil {
			return e
		}
		if len(r) > 0 {
			if r[0][0] == string(b) {
				return nil
			}
			return errors.New("version already registered with another definition")
		}
		for _, step := range steps {
			for _, child := range step.Children {
				r, e := s.query("SELECT 1 FROM definitions WHERE name=? AND version=?", child.Name, child.Version)
				if e != nil {
					return e
				}
				if len(r) == 0 {
					return fmt.Errorf("register child %s version %d first", child.Name, child.Version)
				}
			}
		}
		return s.exec("INSERT INTO definitions VALUES(?,?,?)", name, version, string(b))
	})
}
func (s *Store) event(id, kind string, step int, data string) error {
	return s.exec("INSERT INTO events(run,at,kind,step,data) VALUES(?,?,?,?,?)", id, now(), kind, step, data)
}
func (s *Store) Start(id, name string, version int, queue string, input json.RawMessage, attrs map[string]string) error {
	if id == "" || queue == "" || !json.Valid(input) {
		return errors.New("id, queue and JSON input required")
	}
	return s.tx(func() error { return s.start(id, name, version, queue, input, attrs) })
}

func (s *Store) start(id, name string, version int, queue string, input json.RawMessage, attrs map[string]string) error {

	if e := s.exec("INSERT INTO runs(id,name,version,queue,state,value) VALUES(?,?,?,?,'new',?)", id, name, version, queue, string(input)); e != nil {
		return e
	}
	if e := s.event(id, "WorkflowStarted", 0, string(input)); e != nil {
		return e
	}
	if e := s.attributes(id, attrs); e != nil {
		return e
	}
	return s.advance(id, 0, string(input))
}
func (s *Store) attributes(id string, attrs map[string]string) error {
	if len(attrs) == 0 {
		return nil
	}
	for k, v := range attrs {
		if k == "" {
			return errors.New("empty attribute key")
		}
		if e := s.exec("INSERT INTO attrs VALUES(?,?,?) ON CONFLICT(run,k) DO UPDATE SET v=excluded.v", id, k, v); e != nil {
			return e
		}
	}
	b, _ := json.Marshal(attrs)
	return s.event(id, "AttributesUpdated", -1, string(b))
}
func (s *Store) SetAttributes(id string, attrs map[string]string) error {
	return s.tx(func() error {
		if _, e := s.run(id); e != nil {
			return e
		}
		return s.attributes(id, attrs)
	})
}

type run struct {
	state, value, token string
	step, attempt       int
	due                 int64
	steps               []Step
}

func (s *Store) run(id string) (run, error) {
	var a run
	r, e := s.query("SELECT state,value,token,step,attempt,due,spec FROM runs JOIN definitions USING(name,version) WHERE id=?", id)
	if e != nil {
		return a, e
	}
	if len(r) == 0 {
		return a, ErrMissing
	}
	v := r[0]
	a.state, a.value, a.token = v[0], v[1], v[2]
	a.step, a.attempt = num(v[3]), num(v[4])
	a.due, _ = strconv.ParseInt(v[5], 10, 64)
	return a, json.Unmarshal([]byte(v[6]), &a.steps)
}
func (s *Store) advance(id string, index int, value string) error {
	a, e := s.run(id)
	if e != nil {
		return e
	}
	return s.advanceRun(id, a, index, value)
}

func (s *Store) advanceRun(id string, a run, index int, value string) error {
	var e error
	for index < len(a.steps) {
		x := a.steps[index]
		state, kind, due := "ready", "ActivityScheduled", now()
		switch x.Kind {
		case "switch":
			var fields map[string]json.RawMessage
			_ = json.Unmarshal([]byte(value), &fields)
			var key *string
			target := x.Default
			if json.Unmarshal(fields[x.Field], &key) == nil && key != nil {
				if label, ok := x.Cases[*key]; ok {
					target = label
				}
			}
			if e = s.event(id, "BranchSelected", index, target); e != nil {
				return e
			}
			index = destination(a.steps, index, target)
			continue
		case "parallel", "child":
			return s.spawn(id, index, value, x)
		case "timer":
			state, kind, due = "timer", "TimerStarted", now()+x.Millis
		case "signal":
			state, kind = "waiting", "SignalWaitStarted"
		}
		data, _ := json.Marshal(map[string]any{"name": x.Name, "due": due})
		if e = s.event(id, kind, index, string(data)); e != nil {
			return e
		}
		if state == "waiting" {
			r, e := s.query("SELECT id,value FROM signals WHERE run=? AND name=? AND used=0 ORDER BY rowid LIMIT 1", id, x.Name)
			if e != nil {
				return e
			}
			if len(r) > 0 {
				value = r[0][1]
				if e = s.exec("UPDATE signals SET used=1 WHERE run=? AND id=?", id, r[0][0]); e != nil {
					return e
				}
				if e = s.event(id, "SignalConsumed", index, r[0][0]); e != nil {
					return e
				}
				index = destination(a.steps, index, x.Next)
				continue
			}
		}
		return s.exec("UPDATE runs SET step=?,value=?,state=?,due=?,attempt=0,token='' WHERE id=?", index, value, state, due, id)
	}
	if e = s.event(id, "WorkflowCompleted", index, value); e != nil {
		return e
	}
	if e = s.exec("UPDATE runs SET state='completed',step=?,value=?,token='',due=0 WHERE id=?", index, value, id); e != nil {
		return e
	}
	return s.wakeParent(id)
}
func (s *Store) Signal(id, key, name string, value json.RawMessage) error {
	if key == "" || name == "" || !json.Valid(value) {
		return errors.New("signal ID, name and JSON required")
	}
	return s.tx(func() error {
		a, e := s.run(id)
		if e != nil {
			return e
		}
		r, e := s.query("SELECT name,value FROM signals WHERE run=? AND id=?", id, key)
		if e != nil {
			return e
		}
		if len(r) > 0 {
			if r[0][0] == name && r[0][1] == string(value) {
				return nil
			}
			return errors.New("signal ID conflict")
		}
		if terminal(a.state) {
			return errors.New("run closed")
		}
		if e = s.exec("INSERT INTO signals(run,id,name,value) VALUES(?,?,?,?)", id, key, name, string(value)); e != nil {
			return e
		}
		data, _ := json.Marshal(map[string]any{"id": key, "name": name, "value": value})
		if e = s.event(id, "SignalReceived", a.step, string(data)); e != nil {
			return e
		}
		if a.state == "waiting" && a.steps[a.step].Name == name {
			if e = s.exec("UPDATE signals SET used=1 WHERE run=? AND id=?", id, key); e != nil {
				return e
			}
			if e = s.event(id, "SignalConsumed", a.step, key); e != nil {
				return e
			}
			return s.advanceRun(id, a, destination(a.steps, a.step, a.steps[a.step].Next), string(value))
		}
		return nil
	})
}
func terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}
func (s *Store) Cancel(id string) error {
	return s.tx(func() error { return s.cancelTree(id) })
}
func (s *Store) retry(id string, a run, reason string, permanent bool) error {
	state := "ready"
	kind := "RetryScheduled"
	due := int64(0)
	step := a.steps[a.step]
	if permanent || a.attempt >= max(1, step.Attempts) {
		state = "failed"
		kind = "WorkflowFailed"
	} else {
		delay := step.BackoffMillis
		if len(step.RetryDelays) > 0 {
			delay = step.RetryDelays[a.attempt-1]
		}
		due = now() + delay
	}
	if e := s.event(id, kind, a.step, reason); e != nil {
		return e
	}
	if e := s.exec("UPDATE runs SET state=?,due=?,token='' WHERE id=?", state, due, id); e != nil {
		return e
	}
	if state == "failed" {
		return s.wakeParent(id)
	}
	return nil
}

// Poll also advances due timers and expired leases on this queue. Call continuously.
func (s *Store) Poll(queue string, lease time.Duration) (*Task, error) {
	if lease.Milliseconds() < 1 {
		return nil, errors.New("lease must be at least 1ms")
	}
	due, ok, err := s.NextDue(queue)
	if err != nil || !ok || due > now() {
		return nil, err
	}
	var task *Task
	e := s.tx(func() error {
		for i := 0; i < 32; i++ {
			r, e := s.query("SELECT id FROM runs WHERE queue=? AND "+runnableStates+" AND due<=? ORDER BY due,id LIMIT 1", queue, now())
			if e != nil || len(r) == 0 {
				return e
			}
			id := r[0][0]
			a, e := s.run(id)
			if e != nil {
				return e
			}
			switch a.state {
			case "pending":
				e = s.advanceRun(id, a, 0, a.value)
			case "join_ready":
				e = s.join(id, a)
			case "timer":
				if e = s.event(id, "TimerFired", a.step, ""); e == nil {
					e = s.advanceRun(id, a, destination(a.steps, a.step, a.steps[a.step].Next), a.value)
				}
			case "leased":
				if e = s.event(id, "ActivityTimedOut", a.step, ""); e == nil {
					e = s.retry(id, a, "lease expired", false)
				}
			default:
				b := make([]byte, 16)
				if _, e = rand.Read(b); e != nil {
					return e
				}
				token := hex.EncodeToString(b)
				if e = s.exec("UPDATE runs SET state='leased',due=?,attempt=attempt+1,token=? WHERE id=?", now()+lease.Milliseconds(), token, id); e != nil {
					return e
				}
				task = &Task{id, a.steps[a.step].Name, token, a.step, a.attempt + 1, json.RawMessage(a.value)}
				return s.event(id, "ActivityStarted", a.step, strconv.Itoa(task.Attempt))
			}
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return task, nil
}
func (s *Store) Finish(id, token string, value json.RawMessage, failure string) error {
	return s.finish(id, token, value, failure, false)
}

// FailPermanent closes the run immediately when another attempt cannot succeed.
func (s *Store) FailPermanent(id, token, reason string) error {
	if reason == "" {
		return errors.New("failure reason required")
	}
	return s.finish(id, token, nil, reason, true)
}
func (s *Store) finish(id, token string, value json.RawMessage, failure string, permanent bool) error {
	return s.tx(func() error {
		a, e := s.run(id)
		if e != nil {
			return e
		}
		if a.state != "leased" || a.token != token || a.due <= now() {
			return errors.New("stale activity lease")
		}
		if failure != "" {
			if e = s.event(id, "ActivityFailed", a.step, failure); e != nil {
				return e
			}
			return s.retry(id, a, failure, permanent)
		}
		if !json.Valid(value) {
			return errors.New("invalid JSON result")
		}
		if e = s.event(id, "ActivityCompleted", a.step, string(value)); e != nil {
			return e
		}
		return s.advanceRun(id, a, destination(a.steps, a.step, a.steps[a.step].Next), string(value))
	})
}

// Heartbeat extends the current attempt's lease. An expired attempt cannot renew.
func (s *Store) Heartbeat(id, token string, lease time.Duration) error {
	if lease.Milliseconds() < 1 {
		return errors.New("lease must be at least 1ms")
	}
	return s.tx(func() error {
		a, e := s.run(id)
		if e != nil {
			return e
		}
		t := now()
		if a.state != "leased" || a.token != token || a.due <= t {
			return errors.New("stale activity lease")
		}
		return s.exec("UPDATE runs SET due=? WHERE id=?", t+lease.Milliseconds(), id)
	})
}
