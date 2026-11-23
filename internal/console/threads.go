package console

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
)

// Folder heads make the first page an index seek even for large mailboxes.
// Members resolve independently of mailbox access; reads always scope to the actor.
const threadTables = `
CREATE TABLE thread_members(seq INTEGER PRIMARY KEY,root TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS delivery_by_message ON deliveries(seq,seat);
CREATE INDEX thread_order ON thread_members(root,seq);
CREATE TABLE thread_heads(box TEXT,folder TEXT,root TEXT,latest INTEGER NOT NULL,n INTEGER NOT NULL,PRIMARY KEY(box,folder,root)) WITHOUT ROWID;
CREATE INDEX thread_page ON thread_heads(box,folder,latest DESC);
CREATE INDEX IF NOT EXISTS thread_parent ON messages(parent_id);
CREATE INDEX IF NOT EXISTS email_wire_id ON email_original(wire_id) WHERE wire_id<>'';
`
const threadTriggers = `
CREATE TRIGGER thread_message AFTER INSERT ON messages BEGIN
 INSERT INTO thread_members VALUES(new.seq,coalesce((SELECT t.root FROM messages p JOIN thread_members t USING(seq) WHERE p.id=new.parent_id),nullif(new.conversation_id,''),new.id));
 INSERT INTO thread_heads SELECT new.sender,'sent',root,new.seq,1 FROM thread_members WHERE seq=new.seq
 ON CONFLICT(box,folder,root) DO UPDATE SET latest=max(latest,excluded.latest),n=n+1;
END;
CREATE TRIGGER thread_delivery AFTER INSERT ON deliveries BEGIN
 INSERT INTO thread_heads SELECT new.seat,'inbox',root,new.seq,1 FROM thread_members WHERE seq=new.seq
 ON CONFLICT(box,folder,root) DO UPDATE SET latest=max(latest,excluded.latest),n=n+1;
END;
CREATE TRIGGER thread_remove_delivery AFTER DELETE ON deliveries BEGIN
 DELETE FROM thread_heads WHERE box=old.seat AND folder='inbox' AND root=(SELECT root FROM thread_members WHERE seq=old.seq) AND n=1;
 UPDATE thread_heads SET n=n-1,latest=CASE WHEN latest=old.seq THEN (SELECT max(t.seq) FROM thread_members t JOIN deliveries d USING(seq) WHERE t.root=thread_heads.root AND d.seat=old.seat) ELSE latest END WHERE box=old.seat AND folder='inbox' AND root=(SELECT root FROM thread_members WHERE seq=old.seq);
END;`

type mailParentDB interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// A wire Message-ID is untrusted. Only a unique match for this correspondent
// in this mailbox may establish ancestry. Subjects are never identity keys.
func mailParent(ctx context.Context, db mailParentDB, h mail.Header, box, sender string, before int64) (int64, error) {
	refs := strings.Fields(h.Get("References") + " " + h.Get("In-Reply-To"))
	for i := len(refs) - 1; i >= 0 && i >= len(refs)-12; i-- {
		wire := refs[i]
		if len(wire) > 256 || len(wire) < 3 || wire[0] != '<' || wire[len(wire)-1] != '>' {
			continue
		}
		id, domain, _ := strings.Cut(wire[1:len(wire)-1], "@")
		var seq int64
		err := db.QueryRowContext(ctx, `SELECT coalesce(CASE WHEN count(*)=1 THEN min(seq) END,0) FROM (
 SELECT m.seq FROM email_outbox o JOIN messages m ON m.id=o.id WHERE o.id=? AND o.box=? AND o.destination=? COLLATE NOCASE AND substr(o.origin,instr(o.origin,'@')+1)=? AND m.seq<?
 UNION ALL SELECT m.seq FROM email_original o JOIN messages m ON m.id=o.id JOIN deliveries d USING(seq) WHERE o.wire_id=? AND o.wire_id<>'' AND m.sender=? AND d.seat=? AND m.seq<? LIMIT 2)`, id, box, strings.TrimPrefix(sender, "external:"), strings.ToLower(domain), before, wire, sender, box, before).Scan(&seq)
		if err != nil || seq != 0 {
			return seq, err
		}
	}
	return 0, nil
}
func historicalMailEdges(tx *sql.Tx) error {
	rows, e := tx.Query(`SELECT m.seq,m.sender,d.seat,substr(o.raw,1,65536) FROM email_original o JOIN messages m ON m.id=o.id JOIN deliveries d USING(seq) WHERE coalesce(m.parent_id,'')=''`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var src, box string
		var head []byte
		if e = rows.Scan(&seq, &src, &box, &head); e != nil {
			return e
		}
		msg, err := mail.ReadMessage(bytes.NewReader(head))
		if err != nil {
			continue
		}
		parent, err := mailParent(context.Background(), tx, msg.Header, box, src, seq)
		if err != nil {
			return err
		}
		if parent > 0 {
			if _, err = tx.Exec("INSERT OR IGNORE INTO historical_edges VALUES(?,?)", seq, parent); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}
func initThreads(db *sql.DB) error {
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var done bool
	if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='thread_members' AND type='table')").Scan(&done); e != nil || done {
		return e
	}
	_, e = tx.Exec(threadTables + `CREATE TEMP TABLE historical_edges(seq INTEGER PRIMARY KEY,parent INTEGER NOT NULL);
 INSERT INTO historical_edges SELECT c.seq,p.seq FROM messages c JOIN messages p ON p.id=c.parent_id AND p.seq<c.seq;
 CREATE INDEX edge_children ON historical_edges(parent);`)
	if e != nil {
		return e
	}
	if e = historicalMailEdges(tx); e != nil {
		return e
	}
	_, e = tx.Exec(`WITH RECURSIVE tree(seq,root) AS (
 SELECT m.seq,coalesce(nullif(m.conversation_id,''),m.id) FROM messages m WHERE NOT EXISTS(SELECT 1 FROM historical_edges e WHERE e.seq=m.seq)
 UNION ALL SELECT e.seq,t.root FROM tree t JOIN historical_edges e ON e.parent=t.seq)
 INSERT INTO thread_members SELECT seq,root FROM tree;
 DROP TABLE historical_edges;
 INSERT INTO thread_heads SELECT m.sender,'sent',t.root,max(m.seq),count(*) FROM messages m JOIN thread_members t USING(seq) GROUP BY m.sender,t.root;
 INSERT INTO thread_heads SELECT d.seat,'inbox',t.root,max(d.seq),count(*) FROM deliveries d JOIN thread_members t USING(seq) GROUP BY d.seat,t.root;
 ` + threadTriggers)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *server) threads(_ http.ResponseWriter, r *http.Request) (any, error) {
	box, e := s.actor(r)
	if e != nil {
		return nil, e
	}
	q := r.URL.Query()
	if strings.HasPrefix(r.URL.Path, "/api/desk/") && q.Get("box") != "" && q.Get("box") != s.operator {
		e = s.store.Reader().QueryRowContext(r.Context(), "SELECT box FROM addresses WHERE address=?", q.Get("box")).Scan(&box)
		if e != nil {
			return nil, e
		}
	}
	folder := q.Get("folder")
	if folder == "" {
		folder = "inbox"
	}
	if folder != "inbox" && folder != "sent" {
		return nil, failure{400, "Unknown folder"}
	}
	before := int64(1<<63 - 1)
	if v := q.Get("before"); v != "" {
		before, e = strconv.ParseInt(v, 10, 64)
		if e != nil || before <= 0 {
			return nil, failure{400, "Invalid cursor"}
		}
	}
	const fields = "m.seq,m.id,m.sender,CASE WHEN m.sender=? THEN coalesce((SELECT group_concat(seat,', ') FROM deliveries WHERE seq=m.seq),'') ELSE ? END,m.subject,m.created_text,coalesce(d.accepted_at,0),coalesce(r.opened,0),coalesce(r.replied,0),"
	// Each page reads only the latest envelope and its recipient-scoped receipt.
	// For a fan-out send there is no single recipient whose open represents the group.
	const receipt = ` LEFT JOIN deliveries d ON d.seq=m.seq AND d.seat=CASE WHEN m.sender=? THEN
 (SELECT min(seat) FROM deliveries WHERE seq=m.seq HAVING count(*)=1) ELSE ? END
 LEFT JOIN mail_receipts r ON r.id=m.id AND r.seat=d.seat `
	query := "SELECT " + fields + `h.root,h.n,EXISTS(SELECT 1 FROM thread_heads other WHERE other.box=h.box AND other.root=h.root AND other.folder<>h.folder AND (other.latest<>h.latest OR other.n>1))
 FROM thread_heads h JOIN messages m ON m.seq=h.latest` + receipt + "WHERE h.box=? AND h.folder=? AND h.latest<? ORDER BY h.latest DESC LIMIT 41"
	args := []any{box, box, box, box, box, folder, before}
	if root := r.PathValue("root"); root != "" {
		query = "SELECT " + fields + "t.root,1,0 FROM thread_members t JOIN messages m USING(seq)" + receipt + "WHERE t.root=? AND t.seq<? AND (m.sender=? OR EXISTS(SELECT 1 FROM deliveries v WHERE v.seq=m.seq AND v.seat=?)) ORDER BY t.seq DESC LIMIT 41"
		args = []any{box, box, box, box, root, before, box, box}
	}
	rows, e := s.store.Reader().QueryContext(r.Context(), query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []object{}
	var last int64
	var next any
	for rows.Next() {
		if len(result) == 40 {
			next = strconv.FormatInt(last, 10)
			break
		}
		var id, src, dst, sub, at, root string
		var n int
		var received, opened, replied int64
		var other bool
		if e = rows.Scan(&last, &id, &src, &dst, &sub, &at, &received, &opened, &replied, &root, &n, &other); e != nil {
			return nil, e
		}
		result = append(result, object{"id": id, "src": src, "dst": dst, "subject": sub, "at": at, "thread": root, "count": n, "conversation": n > 1 || other, "received": received, "opened": opened, "replied": replied})
	}
	return object{"rows": result, "next": next}, rows.Err()
}
