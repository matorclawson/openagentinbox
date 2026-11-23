package console

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/google/uuid"
	"net/http"
	"net/mail"
	"slices"
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
	if q.Get("outline") != "" {
		return s.threadOutline(r, box, r.PathValue("root"))
	}
	if q.Get("zoom") != "" {
		return s.threadTime(r, box, r.PathValue("root"))
	}
	before := int64(1<<63 - 1)
	if v := q.Get("before"); v != "" {
		before, e = strconv.ParseInt(v, 10, 64)
		if e != nil || before <= 0 {
			return nil, failure{400, "Invalid cursor"}
		}
	}
	previewField := "''"
	if strings.HasPrefix(r.URL.Path, "/api/desk/") || q.Get("preview") == "1" {
		previewField = "substr(m.display_body,1,768)"
	}
	fields := previewField + ",m.seq,m.id,m.sender,CASE WHEN m.sender=? THEN coalesce((SELECT group_concat(seat,', ') FROM deliveries WHERE seq=m.seq),'') ELSE ? END,m.subject,m.created_text,coalesce(d.accepted_at,0),coalesce(r.opened,0),coalesce(r.replied,0),coalesce(r.due,0),"
	// Each page reads only the latest envelope and its recipient-scoped receipt.
	// For a fan-out send there is no single recipient whose open represents the group.
	const receipt = ` LEFT JOIN deliveries d ON d.seq=m.seq AND d.seat=CASE WHEN m.sender=? THEN
 (SELECT min(seat) FROM deliveries WHERE seq=m.seq HAVING count(*)=1) ELSE ? END
 LEFT JOIN mail_receipts r ON r.id=m.id AND r.seat=d.seat `
	query := "SELECT " + fields + `h.root,h.n,EXISTS(SELECT 1 FROM thread_heads other WHERE other.box=h.box AND other.root=h.root AND other.folder=CASE h.folder WHEN 'sent' THEN 'inbox' ELSE 'sent' END AND (other.latest<>h.latest OR other.n>1))
 FROM thread_heads h JOIN messages m ON m.seq=h.latest` + receipt + "WHERE h.box=? AND h.folder=? AND h.latest<? ORDER BY h.latest DESC LIMIT 41"
	args := []any{box, box, box, box, box, folder, before}
	root := r.PathValue("root")
	after, anchor := q.Get("after"), q.Get("atSeq")
	ascending := after != ""
	if (ascending || anchor != "") && (root == "" || q.Get("before") != "" || ascending && anchor != "") {
		return nil, failure{400, "Invalid thread window"}
	}
	cmp, direction := "<", "DESC"
	bound := before
	if ascending || anchor != "" {
		value := anchor
		cmp = "<="
		if ascending {
			value = after
			cmp = ">"
			direction = "ASC"
		}
		bound, e = strconv.ParseInt(value, 10, 64)
		if e != nil || bound < 0 {
			return nil, failure{400, "Invalid thread position"}
		}
	}
	const visible = "(m.sender=? OR EXISTS(SELECT 1 FROM deliveries v WHERE v.seq=m.seq AND v.seat=?))"
	if value := q.Get("number"); value != "" {
		number, err := strconv.ParseInt(value, 10, 64)
		if root == "" || err != nil || number < 1 || ascending || anchor != "" || q.Get("before") != "" || q.Get("q") != "" {
			return nil, failure{400, "Invalid message number"}
		}
		err = s.store.Reader().QueryRowContext(r.Context(), "SELECT t.seq FROM thread_members t CROSS JOIN messages m USING(seq) WHERE t.root=? AND "+visible+" ORDER BY t.seq LIMIT 1 OFFSET ?", root, box, box, number-1).Scan(&bound)
		if err == sql.ErrNoRows {
			return nil, failure{404, "No message at that position in this conversation"}
		}
		if err != nil {
			return nil, err
		}
		anchor = strconv.FormatInt(bound, 10)
		cmp = "<="
	}
	if root != "" {
		query = "SELECT " + fields + "t.root,1,0 FROM thread_members t CROSS JOIN messages m USING(seq)" + receipt + "WHERE t.root=? AND t.seq" + cmp + "? AND " + visible + " ORDER BY t.seq " + direction + " LIMIT 41"
		args = []any{box, box, box, box, root, bound, box, box}
	}
	term := strings.TrimSpace(q.Get("q"))
	queue := q.Get("view")
	if len(term) > 512 || (queue != "" && (queue != "requests" || folder != "inbox")) {
		return nil, failure{400, "Invalid mailbox filter"}
	}
	searching := term != "" || queue != ""
	var enriched bool
	if searching {
		// Search individual messages, including older replies hidden by a newer thread head.
		source := "messages m"
		_, idErr := uuid.Parse(term)
		textSearch := term != "" && idErr != nil
		if textSearch {
			// Drive newest-first from matching postings. SQLite otherwise may run
			// the entire prefix query again for every envelope in the mailbox.
			source = "inbox_search CROSS JOIN messages m ON m.seq=inbox_search.rowid"
		}
		extra := ""
		if term != "" && (strings.HasPrefix(r.URL.Path, "/api/desk/") || q.Get("preview") == "1") {
			enriched = true
			excerpt := "''"
			if textSearch {
				excerpt = "substr(snippet(inbox_search,-1,char(2),char(3),' … ',18),1,1024)"
			}
			extra = "," + excerpt + ",coalesce((SELECT CASE WHEN p.seq<>m.seq THEN coalesce(nullif(p.subject,''),substr(p.display_body,1,512)) END FROM thread_members f CROSS JOIN messages p USING(seq) WHERE f.root=t.root AND (p.sender=? OR EXISTS(SELECT 1 FROM deliveries rd WHERE rd.seq=p.seq AND rd.seat=?)) ORDER BY f.seq LIMIT 1), '')"
		}
		query = "SELECT " + fields + "t.root,1,0" + extra + " FROM " + source + " CROSS JOIN thread_members t USING(seq)"
		args = []any{box, box}
		if enriched {
			args = append(args, box, box)
		}
		var where []string
		order := "m.seq"
		if root != "" {
			where = append(where, "t.root=? AND "+visible)
		} else if folder == "inbox" {
			query += " CROSS JOIN deliveries visible ON visible.seq=m.seq AND visible.seat=?"
			args = append(args, box)
		} else {
			where = append(where, "m.sender=?")
		}
		query += receipt
		args = append(args, box, box)
		if root != "" {
			args = append(args, root, box, box)
		} else if folder == "sent" {
			args = append(args, box)
		}
		if queue == "requests" {
			where = append(where, "m.id IN (SELECT id FROM mail_receipts WHERE seat=? AND state IN ('pending','accepted','deferred'))")
			args = append(args, box)
		}
		if term != "" {
			if !textSearch {
				where = append(where, "m.id=?")
				args = append(args, strings.ToLower(term))
			} else {
				expr := searchWords(term)
				if expr == "" {
					return object{"rows": []object{}, "next": nil}, nil
				}
				order = "inbox_search.rowid"
				where = append(where, "inbox_search MATCH ? AND inbox_search.rowid"+cmp+"?")
				args = append(args, expr, bound)
			}
		}
		where = append(where, "m.seq"+cmp+"?")
		args = append(args, bound)
		query += " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order + " " + direction + " LIMIT 41"
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
		var preview, id, src, dst, sub, at, root string
		var n int
		var received, opened, replied, due int64
		var other bool
		var excerpt, threadTitle string
		dest := []any{&preview, &last, &id, &src, &dst, &sub, &at, &received, &opened, &replied, &due, &root, &n, &other}
		if enriched {
			dest = append(dest, &excerpt, &threadTitle)
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, e
		}
		row := object{"id": id, "src": src, "dst": dst, "subject": sub, "at": at, "thread": root, "count": n, "conversation": n > 1 || other, "received": received, "opened": opened, "replied": replied}
		if enriched || r.PathValue("root") != "" {
			row["position"] = strconv.FormatInt(last, 10)
		}
		if enriched {
			row["excerpt"] = excerpt
			row["thread_title"] = threadTitle
		}
		if due > 0 {
			row["due"] = due
		}
		if preview != "" {
			row["display"] = preview
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	if root != "" && len(result) > 0 {
		if ascending {
			slices.Reverse(result)
		}
		var older, newer any
		if ascending && bound > 0 || !ascending && next != nil {
			older = result[len(result)-1]["position"]
		}
		if ascending && next != nil || !ascending && (q.Get("before") != "" || anchor != "") {
			newer = result[0]["position"]
		}
		if anchor != "" && newer != nil {
			var more bool
			if e = s.store.Reader().QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM thread_members t CROSS JOIN messages m USING(seq) WHERE t.root=? AND t.seq>? AND "+visible+")", root, newer, box, box).Scan(&more); e != nil {
				return nil, e
			}
			if !more {
				newer = nil
			}
		}
		// Pick a stable heading from the first envelope visible to this mailbox.
		var title string
		if e = s.store.Reader().QueryRowContext(r.Context(), "SELECT coalesce(nullif(m.subject,''),substr(m.display_body,1,512)) FROM thread_members t CROSS JOIN messages m USING(seq) WHERE t.root=? AND "+visible+" ORDER BY t.seq LIMIT 1", root, box, box).Scan(&title); e != nil && e != sql.ErrNoRows {
			return nil, e
		}
		var receivedCount, sentCount int
		if e = s.store.Reader().QueryRowContext(r.Context(), "SELECT coalesce(sum(CASE folder WHEN 'inbox' THEN n END),0),coalesce(sum(CASE folder WHEN 'sent' THEN n END),0) FROM thread_heads WHERE box=? AND root=? AND folder IN ('inbox','sent')", box, root).Scan(&receivedCount, &sentCount); e != nil {
			return nil, e
		}
		return object{"anchor": anchor, "inbox_count": receivedCount, "sent_count": sentCount, "rows": result, "next": older, "newer": newer, "title": title}, nil
	}
	return object{"rows": result, "next": next}, rows.Err()
}
