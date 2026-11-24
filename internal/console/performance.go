package console

import (
	"sync"
	"time"

	"github.com/rodriveracom/dostoevsky/sketch"
)

// Lifetime counters for this daemon process. Separate from durable delivery receipts.
type mailPerformance struct {
	mu           sync.Mutex
	sends, reads sketch.LatencyHistogram
	recipients   *sketch.HyperLogLog
}

func (p *mailPerformance) observe(send bool, recipient string, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if send {
		if p.recipients == nil {
			p.recipients, _ = sketch.NewHyperLogLog(10)
		}
		p.recipients.Add([]byte(recipient))
		p.sends.Observe(elapsed)
	} else {
		p.reads.Observe(elapsed)
	}
}

func (p *mailPerformance) snapshot() object {
	p.mu.Lock()
	defer p.mu.Unlock()
	estimate := 0.0
	if p.recipients != nil {
		estimate = p.recipients.Estimate()
	}
	summary := func(h *sketch.LatencyHistogram) object {
		upper, _ := h.PercentileUpper(95)
		return object{"count": h.Count(), "p95_upper_ms": float64(upper) / float64(time.Millisecond)}
	}
	return object{"window": "since_restart", "successful_sends": summary(&p.sends), "successful_body_reads": summary(&p.reads), "estimated_recipient_mailboxes": estimate}
}
