package deliverystore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestConsoleUpgradeIsAtomicAndKeepsCursorAndRetry(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "console.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, e = s.db.Exec(`CREATE TABLE letters(seq INTEGER PRIMARY KEY,id TEXT,src TEXT,dst TEXT,subject TEXT,body TEXT,at TEXT,operation TEXT);
 INSERT INTO letters VALUES(37,'id1','amy','rod','hello','hello world','2026-01-01T00:00:00.000Z','key'),(48,'id2','rod','amy','reply','yes','invalid','key');`)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if s.UpgradeConsole(ctx) == nil {
		t.Fatal("accepted invalid history")
	}
	var n int
	if e = s.db.QueryRow("SELECT count(*) FROM messages").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if _, e = s.db.Exec("UPDATE letters SET at='2026-01-02T00:00:00.000Z' WHERE seq=48"); e != nil {
		t.Fatal(e)
	}
	if e = s.UpgradeConsole(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.UpgradeConsole(ctx); e != nil {
		t.Fatal(e)
	}
	var seq int
	var body []byte
	if e = s.db.QueryRow("SELECT seq,body FROM messages JOIN deliveries USING(seq) WHERE id='id2' AND seat='amy'").Scan(&seq, &body); e != nil || seq != 48 || string(body) != "yes" {
		t.Fatal(seq, string(body), e)
	}
	id, e := s.Accept("console/amy/key", Message{ID: "retry", Sender: "amy", Recipients: []string{"rod"}, Subject: "hello", Body: []byte("hello world"), Priority: 1}, time.Now())
	if e != nil || id != "id1" {
		t.Fatal(id, e)
	}
}
