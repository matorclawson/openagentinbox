package deliverystore

import (
	"context"
	"errors"
	"testing"
	"time"

	dost "github.com/rodriveracom/openagentinbox/third_party/dostoevsky"
)

func TestSQLiteConsumerUsesSparseCursorsAndExactByteBudget(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	for i, id := range []string{"first", "foreign", "last"} {
		m := nativeMessage(id)
		if i == 1 {
			m.Recipients = []string{"c"}
		}
		if _, err := s.AcceptRecord(ctx, m, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	r := UnreadReader{s, "b"}
	partition := dost.Partition{Topic: "unread"}
	first, more, err := r.Available(ctx, 0, dost.Limits{Records: 1, Bytes: 4096})
	if err != nil || !more || len(first.Records) != 1 {
		t.Fatal(first, more, err)
	}
	row := first.Records[0]
	budget := len(row.Key) + len(row.ID) + len(row.Value)
	for _, n := range []int{budget - 1, budget} {
		p, more, err := r.Available(ctx, 0, dost.Limits{Records: 50, Bytes: n})
		if n < budget {
			if !errors.Is(err, dost.ErrLimit) || p.Next != 0 {
				t.Fatal(p, err)
			}
		} else if err != nil || !more || p.Next != first.Next || len(p.Records) != 1 {
			t.Fatal(p, more, err)
		}
	}
	stopped := errors.New("drained")
	expected := int64(0)
	err = dost.ConsumePages(ctx, r, partition, 0, dost.Limits{Records: 1, Bytes: 4096}, func(_ context.Context, after int64, p dost.Page) error {
		if after != expected || len(p.Records) != 1 {
			t.Fatalf("after=%d page=%v", after, p)
		}
		expected = p.Next
		if after != 0 {
			if p.Next <= after+1 || p.Records[0].ID != "last" {
				t.Fatal(p)
			}
			return stopped
		}
		return nil
	})
	if !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	p, err := r.ReadBatch(ctx, partition, expected, dost.Limits{Records: 5, Bytes: 4096}, false)
	if !errors.Is(err, dost.ErrEmpty) || p.Next != expected {
		t.Fatal(p, err)
	}
}
