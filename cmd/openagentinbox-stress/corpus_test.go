package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

func TestCorpusHistoryAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.jsonl")
	records := []string{
		`{"id":"secret-1","message_id":"wire-1","from":"person-a","to":"person-b, person-c","conversation":"sensitive-topic","at":"2026-01-01T08:00:00Z","body":"Subject: Private subject\nPriority: p2\n\nPrivate sentence."}`,
		`{"id":"secret-2","from":"person-b","to":"person-a","conversation":"sensitive-topic","in_reply_to":"wire-1","at":"2026-01-01T08:00:30Z","body":"Priority: p0\n\nPrivate response."}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b := bench{config: config{Seats: 3, Corpus: path, Scenario: "trace", Rate: 2, Duration: time.Minute}}
	if err := b.readCorpus(); err != nil {
		t.Fatal(err)
	}
	if b.traceOffset(0) != 0 || b.traceOffset(1) != 30*time.Second || b.traceOffset(2) != time.Minute {
		t.Fatal("trace endpoints overlap the next cycle")
	}
	if b.History != 2 || b.historyDeliveries != 3 || b.shapes[1].parent != 0 || b.shapes[1].priority != 0 {
		t.Fatal("history shape mismatch")
	}
	store, err := deliverystore.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = b.seedCorpus(store); err != nil {
		t.Fatal(err)
	}
	var leaked, deliveries, linked int
	err = store.Reader().QueryRow(`SELECT (SELECT count(*) FROM messages WHERE instr(CAST(body AS TEXT),'Private') OR sender LIKE 'person-%' OR id LIKE 'secret-%' OR conversation_id='sensitive-topic'),(SELECT count(*) FROM deliveries),(SELECT count(*) FROM messages WHERE parent_id='history-0')`).Scan(&leaked, &deliveries, &linked)
	if err != nil || leaked != 0 || deliveries != 3 || linked != 1 {
		t.Fatal(err, leaked, deliveries, linked)
	}
	if src, dst := b.route(0); src != 0 || dst != 1 {
		t.Fatal("wrong trace route", src, dst)
	}
	if len(b.liveBody(1, "")) != len("Priority: p0\n\nPrivate response.") {
		t.Fatal("replay size changed")
	}
}
