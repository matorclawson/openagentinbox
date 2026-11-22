package main

import (
	"testing"
	"time"
)

func TestSchedule(t *testing.T) {
	for _, tc := range []struct {
		i, rate int
		burst   bool
		want    time.Duration
	}{
		{100, 100, false, time.Minute}, {9999, 10000, false, 59994 * time.Millisecond},
		{100, 600, true, 10 * time.Second}, {11, 600, true, 220 * time.Millisecond},
		{1440000000, 1000000, false, 24 * time.Hour},
	} {
		if got := offset(tc.i, tc.rate, tc.burst); got != tc.want {
			t.Errorf("offset %v = %v", tc, got)
		}
	}
}
func TestHistogramKeepsTail(t *testing.T) {
	var h hist
	for i := 0; i < 99; i++ {
		h.add(time.Millisecond)
	}
	h.add(8 * time.Second)
	s := h.summary()
	if s.Count != 100 || s.P95ms < 1 || s.P95ms > 1.06 || s.MaxMs != 8000 {
		t.Fatal(s)
	}
}

func TestBudgetsUseWindowAndPeak(t *testing.T) {
	r := result{Scheduled: 100, Confirmed: 100, ConfirmedInWindow: 95, PeakRSSKiB: 1025, Latency: map[string]latency{"send": {P95ms: 2}}}
	for _, c := range []config{{MinWindowPercent: 96}, {MaxRSSMiB: 1}, {MaxSendP95: time.Millisecond}} {
		if budgets(c, r) == nil {
			t.Fatalf("missed budget: %+v", c)
		}
	}
	if e := budgets(config{MinWindowPercent: 95, MaxRSSMiB: 2, MaxSendP95: 2 * time.Millisecond}, r); e != nil {
		t.Fatal(e)
	}
}
