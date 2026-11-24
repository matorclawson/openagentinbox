package sketch

import (
	"math/bits"
	"time"
)

// LatencyHistogram summarizes nonnegative durations in 960 fixed buckets.
// Its zero value is ready to use. Use one per worker or synchronize access.
type LatencyHistogram struct {
	buckets [960]uint64
	count   uint64
}

// Observe adds a sample. It returns false for negative durations or a full counter.
func (h *LatencyHistogram) Observe(d time.Duration) bool {
	if d < 0 || h.count == ^uint64(0) {
		return false
	}
	n := uint64(d)
	shift := max(0, bits.Len64(n)-5)
	index := 16*shift + int(n>>shift)
	h.buckets[index]++
	h.count++
	return true
}

// Count returns the exact number of samples.
func (h *LatencyHistogram) Count() uint64 { return h.count }

// Merge adds all samples in other. A nil input or count overflow leaves h unchanged.
func (h *LatencyHistogram) Merge(other *LatencyHistogram) bool {
	if other == nil || other.count > ^uint64(0)-h.count {
		return false
	}
	for i, n := range other.buckets {
		h.buckets[i] += n
	}
	h.count += other.count
	return true
}

// PercentileUpper returns the upper bound of the nearest-rank percentile.
// The bound is exact through 31 ns and exceeds a positive sample by less
// than 6.25%. Empty histograms and percentiles outside [1, 100] return false.
func (h *LatencyHistogram) PercentileUpper(p int) (time.Duration, bool) {
	if h.count == 0 || p < 1 || p > 100 {
		return 0, false
	}
	percent := uint64(p)
	rank := h.count/100*percent + (h.count%100*percent+99)/100
	for i, n := range h.buckets {
		if rank <= n {
			if i < 16 {
				return time.Duration(i), true
			}
			shift := i/16 - 1
			return time.Duration((uint64(17+i%16) << shift) - 1), true
		}
		rank -= n
	}
	return 0, false
}
