// Corpus simulation retains routing and sizes in memory. Content, names and
// production identifiers are replaced before writing the disposable database.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

type envelopeShape struct {
	src, size, topic, parent, priority int
	dst                                []int
	at                                 time.Time
	hasSubject                         bool
}

func (b *bench) readCorpus() error {
	f, err := os.Open(b.Corpus)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 65536), 2<<20)
	seats, threads, ids := map[string]int{}, map[string]int{}, map[string]int{}
	intern := func(m map[string]int, k string) int {
		if v, ok := m[k]; ok {
			return v
		}
		v := len(m)
		m[k] = v
		return v
	}
	for scanner.Scan() {
		var raw struct {
			ID, From, To, Body, Conversation string
			At                               time.Time
			Parent                           string `json:"in_reply_to"`
			MessageID                        string `json:"message_id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			return fmt.Errorf("invalid corpus row %d", len(b.shapes)+1)
		}
		if raw.From == "" || raw.To == "" || raw.ID == "" || raw.At.IsZero() || len(raw.Body) == 0 || len(raw.Body) > 48000 {
			return fmt.Errorf("unsupported envelope at row %d", len(b.shapes)+1)
		}
		if b.Scenario == "trace" && len(b.shapes) > 0 && raw.At.Before(b.shapes[len(b.shapes)-1].at) {
			return fmt.Errorf("trace requires chronological rows")
		}
		group := raw.Conversation
		if group == "" {
			group = raw.ID
		}
		sh := envelopeShape{src: intern(seats, raw.From), size: len(raw.Body), topic: intern(threads, group), at: raw.At, parent: -1, priority: 1}
		for _, line := range strings.SplitN(raw.Body, "\n", 4) {
			if strings.HasPrefix(line, "Subject: ") {
				sh.hasSubject = true
			}
			if line == "Priority: p2" {
				sh.priority = 2
			}
			if line == "Priority: p0" {
				sh.priority = 0
			}
		}
		if n, ok := ids[raw.Parent]; ok {
			sh.parent = n
		}
		for _, to := range strings.Split(raw.To, ",") {
			to = strings.TrimSpace(to)
			if to == "" {
				return fmt.Errorf("empty recipient at row %d", len(b.shapes)+1)
			}
			sh.dst = append(sh.dst, intern(seats, to))
		}
		ids[raw.ID] = len(b.shapes)
		if raw.MessageID != "" {
			ids[raw.MessageID] = len(b.shapes)
		}
		b.historyDeliveries += len(sh.dst)
		b.shapes = append(b.shapes, sh)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(b.shapes) == 0 || len(seats) > b.Seats {
		return fmt.Errorf("corpus needs %d seats and at least one message", len(seats))
	}
	if b.Scenario == "trace" && !b.shapes[0].at.Before(b.shapes[len(b.shapes)-1].at) {
		return fmt.Errorf("trace needs a positive capture duration")
	}
	if b.Scenario == "trace" {
		scheduled := int64(b.Rate) * b.Duration.Milliseconds() / 60000
		if scheduled%int64(len(b.shapes)) != 0 {
			return fmt.Errorf("trace requires complete cycles of %d messages; adjust rate or duration", len(b.shapes))
		}
	}
	b.History = len(b.shapes)
	return nil
}

func (b *bench) seedCorpus(s *deliverystore.Store) error {
	for i := 0; i < len(b.shapes); {
		minute := b.shapes[i].at.Truncate(time.Minute)
		var batch []deliverystore.Message
		for i < len(b.shapes) && len(batch) < 256 && b.shapes[i].at.Truncate(time.Minute) == minute {
			sh := b.shapes[i]
			subject := ""
			if sh.hasSubject {
				subject = fmt.Sprintf("Dispatch %d", sh.topic)
			}
			head := fmt.Sprintf("Priority: p%d\n\n", sh.priority)
			if subject != "" {
				head = "Subject: " + subject + "\n" + head
			}
			body := (head + strings.Repeat("x", sh.size))[:sh.size]
			m := deliverystore.Message{ID: fmt.Sprintf("history-%d", i), ConversationID: fmt.Sprintf("topic-%d", sh.topic), Sender: name(sh.src), Priority: sh.priority, Subject: subject, Body: []byte(body)}
			if sh.parent >= 0 {
				m.ParentID = fmt.Sprintf("history-%d", sh.parent)
			}
			for _, dst := range sh.dst {
				m.Recipients = append(m.Recipients, name(dst))
			}
			batch = append(batch, m)
			i++
		}
		if err := s.AcceptBatch(batch, minute); err != nil {
			return err
		}
	}
	return nil
}

func (b *bench) liveShape(i int) envelopeShape {
	if b.Scenario == "trace" {
		return b.shapes[i%len(b.shapes)]
	}
	// Spread short trials across the entire history using deterministic samples.
	return b.shapes[(uint64(i)*7919)%uint64(len(b.shapes))]
}
func (b *bench) liveBody(i int, fallback string) string {
	if len(b.shapes) == 0 {
		return fallback
	}
	return strings.Repeat("x", b.liveShape(i).size)
}
