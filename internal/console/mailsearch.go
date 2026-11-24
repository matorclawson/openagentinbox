package console

import (
	"database/sql"
	"strings"
	"unicode"
)

// External content keeps a single authoritative copy of the text. The index,
// its initial backfill and subsequent message changes are transactional.
func initMailSearch(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var installed bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='inbox_search')").Scan(&installed); err != nil || installed {
		return err
	}
	_, err = tx.Exec(`
CREATE VIRTUAL TABLE inbox_search USING fts5(subject,sender,body,content=messages,content_rowid=seq);
INSERT INTO inbox_search(inbox_search) VALUES('rebuild');
CREATE TRIGGER index_mail_insert AFTER INSERT ON messages BEGIN
 INSERT INTO inbox_search(rowid,subject,sender,body) VALUES(new.seq,new.subject,new.sender,new.body);
END;
CREATE TRIGGER index_mail_delete AFTER DELETE ON messages BEGIN
 INSERT INTO inbox_search(inbox_search,rowid,subject,sender,body) VALUES('delete',old.seq,old.subject,old.sender,old.body);
END;
CREATE TRIGGER index_mail_update AFTER UPDATE OF subject,sender,body ON messages BEGIN
 INSERT INTO inbox_search(inbox_search,rowid,subject,sender,body) VALUES('delete',old.seq,old.subject,old.sender,old.body);
 INSERT INTO inbox_search(rowid,subject,sender,body) VALUES(new.seq,new.subject,new.sender,new.body);
END;
CREATE INDEX receipt_open_requests ON mail_receipts(seat,id) WHERE state IN ('pending','accepted','deferred');`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func searchWords(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for i, word := range words {
		words[i] = `"` + word + `"*`
	}
	return strings.Join(words, " AND ")
}
