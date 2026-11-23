package console

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

// Model an unrelated transaction that holds the only writer for 25 ms.
// Time only the notice read. Await the holder before starting the next sample.
func BenchmarkNoticeDuringWrite(b *testing.B) {
	st, err := deliverystore.Open(filepath.Join(b.TempDir(), "notice.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	s := &server{store: st, db: st.Writer(), operator: "operator"}
	req := httptest.NewRequest("GET", "/api/desk/notice?after=0", nil)
	var elapsed time.Duration
	for range b.N {
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		done := make(chan error, 1)
		timer := time.AfterFunc(25*time.Millisecond, func() { done <- tx.Rollback() })
		start := time.Now()
		_, readErr := s.notice(nil, req)
		elapsed += time.Since(start)
		if err = <-done; err != nil {
			b.Fatal(err)
		}
		timer.Stop()
		if readErr != nil {
			b.Fatal(readErr)
		}
	}
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(b.N), "notice-ns/op")
}
