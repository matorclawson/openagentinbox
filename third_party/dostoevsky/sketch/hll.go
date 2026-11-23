// Package sketch provides approximate analytics for streaming workloads.
package sketch

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// HyperLogLog estimates the number of distinct byte strings.
// Its methods require external synchronization when used concurrently.
type HyperLogLog struct {
	precision uint8
	registers []byte
}

// NewHyperLogLog allocates 2^precision bytes for precision in [4, 18].
func NewHyperLogLog(precision uint8) (*HyperLogLog, error) {
	if precision < 4 || precision > 18 {
		return nil, errors.New("precision must be between 4 and 18")
	}
	return &HyperLogLog{precision, make([]byte, 1<<precision)}, nil
}
func (h *HyperLogLog) Add(value []byte) {
	hash := sha256.Sum256(value)
	x := binary.BigEndian.Uint64(hash[:8])
	index := x >> (64 - h.precision)
	rank := bits.LeadingZeros64((x<<h.precision)|(uint64(1)<<(h.precision-1))) + 1
	if byte(rank) > h.registers[index] {
		h.registers[index] = byte(rank)
	}
}

// Merge combines sketches with the same precision.
func (h *HyperLogLog) Merge(other *HyperLogLog) error {
	if other == nil || h.precision != other.precision {
		return errors.New("incompatible precision")
	}
	for i, v := range other.registers {
		if v > h.registers[i] {
			h.registers[i] = v
		}
	}
	return nil
}

// Estimate returns an approximate distinct count.
func (h *HyperLogLog) Estimate() float64 {
	m := float64(len(h.registers))
	sum := 0.0
	zeros := 0
	for _, v := range h.registers {
		sum += math.Ldexp(1, -int(v))
		if v == 0 {
			zeros++
		}
	}
	alpha := 0.7213 / (1 + 1.079/m)
	switch len(h.registers) {
	case 16:
		alpha = 0.673
	case 32:
		alpha = 0.697
	case 64:
		alpha = 0.709
	}
	estimate := alpha * m * m / sum
	if estimate <= 2.5*m && zeros > 0 {
		return m * math.Log(m/float64(zeros))
	}
	return estimate
}
func (h *HyperLogLog) MarshalBinary() ([]byte, error) {
	out := make([]byte, 5+len(h.registers))
	copy(out, "HLL1")
	out[4] = h.precision
	copy(out[5:], h.registers)
	return out, nil
}
func (h *HyperLogLog) UnmarshalBinary(data []byte) error {
	if len(data) < 5 || string(data[:4]) != "HLL1" {
		return errors.New("invalid sketch")
	}
	p := data[4]
	if p < 4 || p > 18 || len(data) != 5+(1<<p) {
		return errors.New("invalid sketch size")
	}
	for _, v := range data[5:] {
		if v > 65-p {
			return errors.New("invalid register")
		}
	}
	h.precision = p
	h.registers = append([]byte(nil), data[5:]...)
	return nil
}
