package deliverystore

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Adversarial backlog: no p0, a small actionable set buried under courtesy mail.
// The old p0 selection scans every unread delivery to discover there are none.
func BenchmarkBacklogView(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			for start := 0; start < n; start += 1000 {
				msgs := make([]Message, 0, 1000)
				for i := start; i < start+1000; i++ {
					p := 2
					if i < 10 {
						p = 1
					}
					msgs = append(msgs, Message{ID: fmt.Sprint(i), Sender: "a", Recipients: []string{"b"}, Priority: p, Body: []byte("Priority: p2\n\nSynthetic")})
				}
				if err := s.AcceptBatch(msgs, time.Unix(1, 0)); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				v, err := s.InboxView("b", 5, 100)
				if err != nil || v.Total != n || len(v.Rows) != 5 {
					b.Fatalf("%+v %v", v, err)
				}
			}
		})
	}
}
