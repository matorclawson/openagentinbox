package sketch

import (
	"fmt"
	"math"
	"testing"
)

func TestHyperLogLog(t *testing.T) {
	a, _ := NewHyperLogLog(14)
	b, _ := NewHyperLogLog(14)
	if a.Estimate() != 0 {
		t.Fatal("empty estimate")
	}
	for i := 0; i < 100000; i++ {
		v := []byte(fmt.Sprint(i))
		if i%2 == 0 {
			a.Add(v)
		} else {
			b.Add(v)
		}
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if math.Abs(a.Estimate()-100000) > 4000 {
		t.Fatal(a.Estimate())
	}
	raw, _ := a.MarshalBinary()
	var restored HyperLogLog
	if err := restored.UnmarshalBinary(raw); err != nil || restored.Estimate() != a.Estimate() {
		t.Fatal(err)
	}
	if restored.UnmarshalBinary(raw[:len(raw)-1]) == nil {
		t.Fatal("truncated sketch accepted")
	}
}
