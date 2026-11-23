package ephemeral

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Child identifies an immutable workflow. An empty queue inherits the parent's queue.
type Child struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Queue   string `json:"queue,omitempty"`
}

func validateGraph(steps []Step) error {
	labels := map[string]int{}
	for i, x := range steps {
		if x.Label != "" {
			if _, ok := labels[x.Label]; ok || x.Label == "$end" {
				return fmt.Errorf("invalid or duplicate label %q", x.Label)
			}
			labels[x.Label] = i
		}
	}
	for i, x := range steps {
		targets := []string{}
		if x.Next != "" {
			targets = append(targets, x.Next)
		}
		if x.Kind == "switch" {
			if x.Field == "" || x.Default == "" || len(x.Cases) == 0 || x.Next != "" {
				return errors.New("switch needs field, cases, default and no next")
			}
			targets = append(targets, x.Default)
			for _, target := range x.Cases {
				targets = append(targets, target)
			}
		} else if len(x.Cases) > 0 || x.Default != "" || x.Field != "" {
			return errors.New("branch fields require switch")
		}
		for _, target := range targets {
			if target == "$end" {
				continue
			}
			j, ok := labels[target]
			if !ok || j <= i {
				return fmt.Errorf("step %d needs a forward target, got %q", i, target)
			}
		}
		switch x.Kind {
		case "child":
			if len(x.Children) != 1 {
				return errors.New("child step needs one child")
			}
		case "parallel":
			if len(x.Children) < 1 || len(x.Children) > 64 {
				return errors.New("parallel needs 1..64 children")
			}
		default:
			if len(x.Children) > 0 {
				return errors.New("unexpected children")
			}
		}
		for _, c := range x.Children {
			if c.Name == "" || c.Version < 1 {
				return errors.New("child needs name and version")
			}
		}
	}
	return nil
}
func destination(steps []Step, i int, label string) int {
	if label == "" {
		return i + 1
	}
	for j := i + 1; j < len(steps); j++ {
		if steps[j].Label == label {
			return j
		}
	}
	return len(steps)
}
func (s *Store) spawn(id string, step int, value string, x Step) error {
	r, e := s.query("SELECT queue FROM runs WHERE id=?", id)
	if e != nil {
		return e
	}
	ids := make([]string, len(x.Children))
	for i, c := range x.Children {
		var b [16]byte
		if _, e := rand.Read(b[:]); e != nil {
			return e
		}
		child := "child-" + hex.EncodeToString(b[:])
		ids[i] = child
		q := c.Queue
		if q == "" {
			q = r[0][0]
		}
		if e = s.exec("INSERT INTO runs(id,name,version,queue,state,value,due) VALUES(?,?,?,?,'pending',?,?)", child, c.Name, c.Version, q, value, now()); e != nil {
			return e
		}
		if e = s.exec("INSERT INTO children VALUES(?,?,?,?)", id, step, i, child); e != nil {
			return e
		}
		if e = s.event(child, "WorkflowStarted", 0, value); e != nil {
			return e
		}
	}
	data, _ := json.Marshal(ids)
	if e = s.event(id, "ChildrenStarted", step, string(data)); e != nil {
		return e
	}
	return s.exec("UPDATE runs SET state='joining',step=?,value=?,token='',due=0,attempt=0 WHERE id=?", step, value, id)
}
func (s *Store) wakeParent(child string) error {
	return s.exec(`UPDATE runs SET state='join_ready',due=? WHERE state='joining' AND (id,step)=(SELECT parent,step FROM children WHERE child=?) AND NOT EXISTS(SELECT 1 FROM children c JOIN runs r ON c.child=r.id WHERE c.parent=runs.id AND c.step=runs.step AND r.state NOT IN ('completed','failed','cancelled'))`, now(), child)
}
func (s *Store) join(id string, a run) error {
	r, e := s.query("SELECT c.child,r.state,r.value FROM children c JOIN runs r ON r.id=c.child WHERE c.parent=? AND c.step=? ORDER BY c.pos", id, a.step)
	if e != nil {
		return e
	}
	results := make([]json.RawMessage, len(r))
	for i, row := range r {
		if row[1] != "completed" {
			reason := fmt.Sprintf("child %s %s", row[0], row[1])
			if e = s.event(id, "WorkflowFailed", a.step, reason); e != nil {
				return e
			}
			if e = s.exec("UPDATE runs SET state='failed',due=0 WHERE id=?", id); e != nil {
				return e
			}
			return s.wakeParent(id)
		}
		results[i] = json.RawMessage(row[2])
	}
	value, e := json.Marshal(results)
	if e != nil {
		return e
	}
	x := a.steps[a.step]
	if x.Kind == "child" {
		value = results[0]
	}
	if e = s.event(id, "ChildrenJoined", a.step, string(value)); e != nil {
		return e
	}
	return s.advanceRun(id, a, destination(a.steps, a.step, x.Next), string(value))
}
func (s *Store) cancelTree(id string) error {
	a, e := s.run(id)
	if e != nil || terminal(a.state) {
		return e
	}
	if e = s.event(id, "WorkflowCancelled", a.step, ""); e != nil {
		return e
	}
	if e = s.exec("UPDATE runs SET state='cancelled',token='',due=0 WHERE id=?", id); e != nil {
		return e
	}
	r, e := s.query("SELECT child FROM children JOIN runs ON id=child WHERE parent=? AND state NOT IN ('completed','failed','cancelled')", id)
	if e != nil {
		return e
	}
	for _, row := range r {
		if e = s.cancelTree(row[0]); e != nil {
			return e
		}
	}
	return s.wakeParent(id)
}
