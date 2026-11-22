package deliverystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type ExportRecipient struct {
	Recipient string  `json:"recipient"`
	MessageID string  `json:"message_id"`
	LogState  string  `json:"log_state"`
	Delivered *string `json:"log_delivered_at"`
	Acked     *string `json:"log_acked_at"`
	Answered  *string `json:"log_answered_at"`
}
type ExportItem struct {
	ID           string            `json:"post_id"`
	From         string            `json:"from"`
	Created      string            `json:"created_at"`
	Subject      *string           `json:"subject"`
	Conversation *string           `json:"conversation_id"`
	Parent       *string           `json:"in_reply_to"`
	Operation    *string           `json:"answer_operation_id"`
	Body         string            `json:"body"`
	Recipients   []ExportRecipient `json:"recipients"`
}

func ptr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func stamp(v int64) *string {
	if v == 0 {
		return nil
	}
	s := time.UnixMilli(v).UTC().Format(time.RFC3339Nano)
	return &s
}

// ExportItems is bounded by rows AND bytes. It exposes all copies only to their sender.
func (s *Store) ExportItems(ctx context.Context, seat, mode, since string, maxBytes int) ([]ExportItem, error) {
	if mode != "" && mode != "outstanding" {
		return nil, errors.New("invalid export mode")
	}
	if mode == "outstanding" && since != "" {
		return nil, errors.New("choose outstanding or since")
	}
	after, id, _ := strings.Cut(since, " ")
	if after != "" {
		t, e := time.Parse(time.RFC3339Nano, after)
		if e != nil {
			return nil, e
		}
		after = t.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	norm := `substr(m.created_text,1,19)||'.'||substr((CASE WHEN substr(m.created_text,20,1)='.' THEN substr(m.created_text,21,length(m.created_text)-21) ELSE '' END)||'000000000',1,9)||'Z'`
	cond := `(m.sender=? OR EXISTS(SELECT 1 FROM deliveries d WHERE d.seq=m.seq AND d.seat=?))`
	args := []any{seat, seat}
	if mode == "outstanding" {
		cond += ` AND m.sender<>? AND EXISTS(SELECT 1 FROM deliveries d WHERE d.seq=m.seq AND d.seat=? AND d.answered_at IS NULL)`
		args = append(args, seat, seat)
	}
	if after != "" {
		cond += ` AND ((` + norm + `)>? OR ((` + norm + `)=? AND m.id>?))`
		args = append(args, after, after, id)
	}
	tx, e := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	q, e := tx.QueryContext(ctx, `SELECT seq FROM messages m WHERE `+cond+` ORDER BY (`+norm+`),m.id LIMIT 201`, args...)
	if e != nil {
		return nil, e
	}
	seqs := []int64{}
	for q.Next() {
		var seq int64
		if e = q.Scan(&seq); e != nil {
			q.Close()
			return nil, e
		}
		seqs = append(seqs, seq)
	}
	e = q.Err()
	q.Close()
	if e != nil {
		return nil, e
	}
	if len(seqs) > 200 {
		return nil, ErrPageRequired
	}
	out := []ExportItem{}
	bytes := 0
	for _, seq := range seqs {
		var r ExportItem
		var subject, conv, parent, meta string
		var body []byte
		e = tx.QueryRowContext(ctx, `SELECT id,sender,created_text,subject,coalesce(conversation_id,''),coalesce(parent_id,''),body,record_json FROM messages WHERE seq=?`, seq).Scan(&r.ID, &r.From, &r.Created, &subject, &conv, &parent, &body, &meta)
		if e != nil {
			return nil, e
		}
		bytes += len(body)
		if bytes > maxBytes/6 {
			return nil, ErrPageRequired
		}
		r.Body = string(body)
		r.Subject = ptr(subject)
		r.Conversation = ptr(conv)
		r.Parent = ptr(parent)
		var metadata map[string]json.RawMessage
		json.Unmarshal([]byte(meta), &metadata)
		json.Unmarshal(metadata["answer_operation_id"], &r.Operation)
		if r.Operation == nil && parent != "" {
			r.Operation = ptr("op-" + parent + "-" + r.From)
		}
		r.Recipients = []ExportRecipient{}
		q, e = tx.QueryContext(ctx, `SELECT seat,coalesce(delivered_at,0),coalesce(noticed_at,0),coalesce(answered_at,0),record_json FROM deliveries WHERE seq=? AND (?=? OR seat=?) ORDER BY seat`, seq, r.From, seat, seat)
		if e != nil {
			return nil, e
		}
		for q.Next() {
			var d ExportRecipient
			var delivered, acked, answered int64
			var dm string
			if e = q.Scan(&d.Recipient, &delivered, &acked, &answered, &dm); e != nil {
				q.Close()
				return nil, e
			}
			d.MessageID = r.ID + "." + d.Recipient
			d.Delivered = ExactStamp(dm, "log_delivered_at", delivered)
			d.Acked = ExactStamp(dm, "log_acked_at", acked)
			d.Answered = ExactStamp(dm, "log_answered_at", answered)
			d.LogState = "delivered"
			if acked != 0 {
				d.LogState = "acknowledged"
			}
			if answered != 0 {
				d.LogState = "answered"
			}
			r.Recipients = append(r.Recipients, d)
		}
		e = q.Err()
		q.Close()
		if e != nil {
			return nil, e
		}
		var ordered []string
		json.Unmarshal(metadata["recipients"], &ordered)
		rank := map[string]int{}
		for i, v := range ordered {
			rank[v] = i + 1
		}
		sort.SliceStable(r.Recipients, func(i, j int) bool {
			a, b := rank[r.Recipients[i].Recipient], rank[r.Recipients[j].Recipient]
			if a == 0 {
				return false
			}
			if b == 0 {
				return true
			}
			return a < b
		})
		out = append(out, r)
	}
	return out, nil
}
