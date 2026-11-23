package sketch

import (
	"math"
	"sort"
	"testing"
	"time"
)

func TestLatencyBoundsAndMerge(t *testing.T) {
	samples := []time.Duration{0, 0, 1, math.MaxInt64}
	for shift := 0; shift <= 58; shift++ {
		for mantissa := 16; mantissa < 32; mantissa++ {
			start := time.Duration(mantissa) << shift
			samples = append(samples, start-1, start, start+1)
		}
	}
	var a, b LatencyHistogram
	for i, d := range samples {
		var single LatencyHistogram
		single.Observe(d)
		checkBound(t, &single, 100, d)
		if i%2 == 0 {
			a.Observe(d)
		} else {
			b.Observe(d)
		}
	}
	if !a.Merge(&b) || a.Count() != uint64(len(samples)) {
		t.Fatal("merged count")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for p := 1; p <= 100; p++ {
		checkBound(t, &a, p, samples[(p*len(samples)-1)/100])
	}
}

func checkBound(t *testing.T, h *LatencyHistogram, p int, want time.Duration) {
	t.Helper()
	got, ok := h.PercentileUpper(p)
	if !ok || got < want || (got > want && got-want > (want-1)/16) {
		t.Fatalf("p%d exact %v upper %v", p, want, got)
	}
}

func TestLatencyLimits(t *testing.T) {
	var h LatencyHistogram
	if _, ok := h.PercentileUpper(95); ok || h.Observe(-1) || h.Merge(nil) {
		t.Fatal("invalid input")
	}
	h.buckets[1] = math.MaxUint64
	h.count = math.MaxUint64
	if h.Observe(1) || h.Merge(&h) {
		t.Fatal("overflow")
	}
	for _, p := range []int{0, 101} {
		if _, ok := h.PercentileUpper(p); ok {
			t.Fatal(p)
		}
	}
	checkBound(t, &h, 99, 1)
}
