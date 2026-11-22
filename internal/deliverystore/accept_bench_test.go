package deliverystore

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Exercise the indexed, FULL-synchronous write paths on a real database.
func BenchmarkDurableAccept(b *testing.B) {
	for _, path := range []string{"native", "v2"} {
		b.Run(path, func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "messages.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			for _, seat := range []string{"sender", "reader"} {
				if err = s.PutSeat(SeatRec{Seat: seat}); err != nil {
					b.Fatal(err)
				}
			}
			m := Message{Sender: "sender", Recipients: []string{"reader"}, Body: make([]byte, 4096), Priority: 1}
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.ID = "post-" + strconv.Itoa(i)
				if path == "native" {
					_, err = s.AcceptRecord(ctx, m, time.Now())
				} else {
					_, err = s.AcceptContext(ctx, m.ID, m, time.Now())
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}
