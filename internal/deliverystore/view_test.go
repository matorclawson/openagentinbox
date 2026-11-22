package deliverystore

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedView accepts n0 p0, n1 p1 and n2 p2 messages to "beta", oldest first, interleaved so that creation order differs from priority order.
func seedView(t *testing.T, s *Store, n0, n1, n2 int) {
	t.Helper()
	i := 0
	add := func(prio int) {
		i++
		m := Message{ID: fmt.Sprintf("v%03d", i), Sender: "alpha", Subject: fmt.Sprintf("s%d", i), Priority: prio, Recipients: []string{"beta"}, Body: []byte("b")}
		if _, err := s.Accept(fmt.Sprintf("k%d", i), m, t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for k := 0; k < n1; k++ {
		add(1)
		if k < n0 {
			add(0)
		}
		if k < n2 {
			add(2)
		}
	}
}

func ids(v View) string {
	var s []string
	for _, r := range v.Rows {
		s = append(s, r.ID)
	}
	return strings.Join(s, ",")
}

func TestInboxViewWithLimitIsEveryP0AndTheNewestN_P1_P2Counted(t *testing.T) {
	s := open(t)
	seedView(t, s, 2, 6, 3) // 6 p1, 2 p0, 3 p2 interleaved: p1,p0,p2, p1,p0,p2, p1,p2, p1, p1, p1
	v, err := s.InboxView("beta", 3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != 11 {
		t.Fatalf("total counts every unread, p2 too: %d", v.Total)
	}
	for _, r := range v.Rows {
		if r.Priority == 2 {
			t.Fatalf("a p2 is counted, never shown with --limit: %s", ids(v))
		}
	}
	p0, p1 := 0, 0
	for _, r := range v.Rows {
		if r.Priority == 0 {
			p0++
		} else {
			p1++
		}
	}
	if p0 != 2 || p1 != 3 {
		t.Fatalf("every p0 and the newest 3 p1: got p0=%d p1=%d (%s)", p0, p1, ids(v))
	}
	for i := 1; i < len(v.Rows); i++ {
		if v.Rows[i].createdMilli < v.Rows[i-1].createdMilli {
			t.Fatalf("oldest first: %s", ids(v))
		}
	}
	if v.Rows[len(v.Rows)-1].Subject != "s11" { // the newest message overall is a p1 and is in
		t.Fatalf("the newest p1 must be shown: %s", ids(v))
	}
}

func TestInboxViewWithoutLimitShowsAllWithinTheRowCapAndBoundsBeyondIt(t *testing.T) {
	s := open(t)
	seedView(t, s, 2, 6, 3)
	v, _ := s.InboxView("beta", -1, 100)
	if len(v.Rows) != 11 || v.Total != 11 {
		t.Fatalf("within the cap every row, p2 too: %d of %d", len(v.Rows), v.Total)
	}
	for i := 1; i < len(v.Rows); i++ { // within the cap the CLI keeps unread_json's order: p0, p1, p2, each oldest first
		a, b := v.Rows[i-1], v.Rows[i]
		if a.Priority > b.Priority || (a.Priority == b.Priority && a.createdMilli > b.createdMilli) {
			t.Fatalf("within the cap the order is rank then age: %s", ids(v))
		}
	}
	v, _ = s.InboxView("beta", -1, 5) // over the cap: every p0 and the newest others, 5 in all
	if len(v.Rows) != 5 || v.Total != 11 {
		t.Fatalf("beyond the cap: %d rows of %d", len(v.Rows), v.Total)
	}
	p0 := 0
	for _, r := range v.Rows {
		if r.Priority == 0 {
			p0++
		}
	}
	if p0 != 2 {
		t.Fatalf("every p0 is kept: %s", ids(v))
	}
	v, _ = s.InboxView("beta", -1, 1) // fewer slots than p0s: all p0 still shown
	if len(v.Rows) != 2 {
		t.Fatalf("p0 are never dropped: %s", ids(v))
	}
}

func TestPendingViewIsReadOrNotAndStatesFollow(t *testing.T) {
	s := open(t)
	seedView(t, s, 1, 5, 0)
	if err := s.Notice("beta", "v001", t0); err != nil { // read, still pending
		t.Fatal(err)
	}
	if err := s.Answer("beta", "v002", t0); err != nil { // the p0: answered, gone
		t.Fatal(err)
	}
	v, err := s.PendingView("beta", 3)
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != 5 { // 5 p1 pending (the p0 is answered)
		t.Fatalf("total pending: %d", v.Total)
	}
	if len(v.Rows) != 3 {
		t.Fatalf("newest 3 p1 (no p0 left): %s", ids(v))
	}
	all, _ := s.PendingView("beta", 50)
	if all.Rows[0].ID != "v001" || all.Rows[0].LogState() != "acknowledged" || all.Rows[1].LogState() != "delivered" {
		t.Fatalf("log_state follows read state: %s %s %s", all.Rows[0].ID, all.Rows[0].LogState(), all.Rows[1].LogState())
	}
	if _, err := s.PendingView("beta", 0); err == nil {
		t.Fatal("a limit below 1 is refused, as the CLI does")
	}
}

func TestFinalMessagesAreNeverPendingButAreUnread(t *testing.T) {
	s := open(t)
	if _, err := s.Accept("f", Message{ID: "fin", Sender: "alpha", Subject: "done", Priority: 1, Final: true, Recipients: []string{"beta"}, Body: []byte("b")}, t0); err != nil {
		t.Fatal(err)
	}
	pv, _ := s.PendingView("beta", 50)
	iv, _ := s.InboxView("beta", 50, 100)
	if pv.Total != 0 || iv.Total != 1 || !iv.Rows[0].Final {
		t.Fatalf("final: pending %d, unread %d, final flag %v", pv.Total, iv.Total, len(iv.Rows) > 0 && iv.Rows[0].Final)
	}
}

// The view's queries must use the partial indexes (no scan of deliveries).
func TestViewQueriesUseTheIntendedIndexes(t *testing.T) {
	s := open(t)
	for _, c := range []struct{ name, cond string }{{"unread", unreadCond}, {"pending", pendingCond}} {
		for _, extra := range []string{` AND m.priority = 0`, ` AND m.priority = 1`} {
			pl := plan(t, s, `SELECT `+rowCols+` FROM deliveries d `+c.cond+extra+` ORDER BY d.seq DESC LIMIT ?`, "beta", 5)
			want := "deliveries_" + c.name
			if !strings.Contains(pl, want) || strings.Contains(pl, "SCAN d") || strings.Contains(pl, "USE TEMP B-TREE") {
				t.Fatalf("%s%s: %s", c.name, extra, pl)
			}
		}
	}
}
