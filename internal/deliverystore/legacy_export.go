package deliverystore

// This is an offline rollback format, never a live mail store or request path.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var legacyName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,255}$`)

type rawRecord map[string]json.RawMessage

func (r rawRecord) put(k string, v any) { b, _ := json.Marshal(v); r[k] = b }
func record(raw string) (rawRecord, error) {
	r := rawRecord{}
	e := json.Unmarshal([]byte(raw), &r)
	if r == nil {
		return nil, errors.New("null metadata")
	}
	return r, e
}
func (r rawRecord) defaultValue(k string, v any) {
	if _, ok := r[k]; !ok {
		r.put(k, v)
	}
}
func (r rawRecord) stamp(k string, ms int64) {
	if ms == 0 {
		r.defaultValue(k, nil)
		return
	}
	var old string
	json.Unmarshal(r[k], &old)
	t, e := time.Parse(time.RFC3339Nano, old)
	if e == nil && t.UnixMilli() == ms {
		return
	}
	r.put(k, time.UnixMilli(ms).UTC().Format(time.RFC3339Nano))
}
func canonicalRecord(r rawRecord) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	e := enc.Encode(r)
	return b.Bytes(), e
}

// ExportLegacy requires a fence and an absent destination. It includes mutations to
// pre-cutover messages and keeps opaque Runtime fields. Receipts are separate chains
// and must be carried alongside this export without rewriting them.
func (s *Store) ExportLegacy(ctx context.Context, dest string) (err error) {
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var fence string
	if e = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='fence'`).Scan(&fence); e != nil {
		return errors.New("rollback export requires a fenced store")
	}
	if e = os.Mkdir(dest, 0700); e != nil {
		return e
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(dest)
		}
	}()
	for _, d := range []string{"posts", "messages", "instances"} {
		if e = os.Mkdir(filepath.Join(dest, d), 0700); e != nil {
			return e
		}
	}
	write := func(dir, id string, r rawRecord) error {
		if !legacyName.MatchString(id) {
			return fmt.Errorf("unsafe record name %q", id)
		}
		b, e := canonicalRecord(r)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(filepath.Join(dest, dir, id+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	}
	seats, e := tx.QueryContext(ctx, `SELECT seat,record FROM seats ORDER BY seat`)
	if e != nil {
		return e
	}
	for seats.Next() {
		var id, raw string
		if e = seats.Scan(&id, &raw); e != nil {
			seats.Close()
			return e
		}
		r, e := record(raw)
		if e != nil {
			seats.Close()
			return e
		}
		if e = write("instances", id, r); e != nil {
			seats.Close()
			return e
		}
	}
	e = seats.Err()
	seats.Close()
	if e != nil {
		return e
	}
	posts, e := tx.QueryContext(ctx, `SELECT seq,id,sender,coalesce(conversation_id,''),coalesce(parent_id,''),created_text,body,coalesce(recipients,''),record_json FROM messages ORDER BY seq`)
	if e != nil {
		return e
	}
	defer posts.Close()
	for posts.Next() {
		var seq int64
		var id, sender, conv, parent, created, recips, raw string
		var body []byte
		if e = posts.Scan(&seq, &id, &sender, &conv, &parent, &created, &body, &recips, &raw); e != nil {
			return e
		}
		p, e := record(raw)
		if e != nil {
			return e
		}
		p.put("body", string(body))
		p.defaultValue("schema_version", 1)
		p.defaultValue("message_id", id)
		p.defaultValue("from_instance", sender)
		p.defaultValue("created_at", created)
		p.defaultValue("conversation_id", ptr(conv))
		p.defaultValue("in_reply_to", ptr(parent))
		p.defaultValue("sha256", PayloadSHA256(body))
		p.defaultValue("sender_identity", "claimed")
		for _, k := range []string{"project_id", "importer", "interim_id", "source_path", "answer_operation_id"} {
			p.defaultValue(k, nil)
		}
		if parent != "" {
			p.put("answer_operation_id", "op-"+parent+"-"+sender)
		}
		copies, e := tx.QueryContext(ctx, `SELECT seat,record_json,coalesce(delivered_at,0),coalesce(noticed_at,0),coalesce(answered_at,0),coalesce(answer_id,''),coalesce(nullif(created_text,''),?) FROM deliveries WHERE seq=? ORDER BY seat`, created, seq)
		if e != nil {
			return e
		}
		recipients := []string{}
		for copies.Next() {
			var seat, meta, aid, dc string
			var delivered, noticed, answered int64
			if e = copies.Scan(&seat, &meta, &delivered, &noticed, &answered, &aid, &dc); e != nil {
				copies.Close()
				return e
			}
			recipients = append(recipients, seat)
			d, e := record(meta)
			if e != nil {
				copies.Close()
				return e
			}
			d.put("body", string(body))
			d.defaultValue("schema_version", 1)
			d.defaultValue("message_id", id+"."+seat)
			d.defaultValue("post_id", id)
			d.defaultValue("from_instance", sender)
			d.defaultValue("to_instance", seat)
			d.defaultValue("created_at", dc)
			d.defaultValue("conversation_id", ptr(conv))
			d.defaultValue("reply_to", nil)
			d.defaultValue("state", "queued")
			d.defaultValue("kind", "message")
			d.defaultValue("requires_ack", true)
			d.defaultValue("artifact_refs", []string{})
			d.defaultValue("delivery_attempts", 0)
			for _, k := range legacyNullFields {
				d.defaultValue(k, nil)
			}
			state := "delivered"
			if noticed != 0 {
				state = "acknowledged"
			}
			if answered != 0 {
				state = "answered"
			}
			d.put("log_state", state)
			d.stamp("log_delivered_at", delivered)
			d.stamp("log_acked_at", noticed)
			d.stamp("log_answered_at", answered)
			if aid != "" {
				d.put("log_answer_message_id", aid)
			}
			if e = write("messages", id+"."+seat, d); e != nil {
				copies.Close()
				return e
			}
		}
		e = copies.Err()
		copies.Close()
		if e != nil {
			return e
		}
		if recips != "" {
			recipients = strings.Split(recips, ",")
		}
		p.defaultValue("recipients", recipients)
		if e = write("posts", id, p); e != nil {
			return e
		}
	}
	if e = posts.Err(); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(dest, "FENCE"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write([]byte(fence))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	for _, d := range []string{"posts", "messages", "instances", ""} {
		f, e := os.Open(filepath.Join(dest, d))
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return e
		}
	}
	done = true
	return nil
}

var legacyNullFields = []string{"acked_at", "business_result_delivery_attempt", "business_result_dispatch_fence", "business_result_history", "business_result_incarnation", "business_result_receipt_json", "business_result_recipient_instance", "business_result_recorded_at", "consumption_receipt_delivery_attempt", "consumption_receipt_incarnation", "consumption_receipt_message_id", "consumption_receipt_recipient_instance", "consumption_receipt_recorded_at", "delivered_at", "delivery_attempt_lease_expires_at", "delivery_attempt_owner", "delivery_attempt_status", "delivery_incarnation", "last_error", "log_ack_generation", "log_ack_overdue_at", "log_ack_overdue_generation", "log_acked_at", "log_answer_generation", "log_answer_message_id", "log_answer_operation_id", "log_answer_overdue_at", "log_answer_overdue_generation", "log_answer_sha256", "log_answered_at", "log_carried_at", "log_carried_from", "log_carried_to", "log_delivered_at", "log_restored_at", "log_restored_generation", "reply_to"}
