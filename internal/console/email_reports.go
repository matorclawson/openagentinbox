package console

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
)

// The edge can receive a DSN well after it took responsibility for an email.
// A per-message return path constrains a report to the original destination.
var enhancedStatus = regexp.MustCompile(`^[245]\.[0-9]{1,3}\.[0-9]{1,3}$`)

type deliveryReport struct{ recipient, action, status string }

func readDeliveryReport(raw []byte) []deliveryReport {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	kind, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if kind != "multipart/report" || !strings.EqualFold(params["report-type"], "delivery-status") {
		return nil
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	for i := 0; i < 8; i++ {
		part, e := mr.NextPart()
		if e != nil {
			return nil
		}
		kind, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if kind != "message/delivery-status" {
			part.Close()
			continue
		}
		r := textproto.NewReader(bufio.NewReader(io.LimitReader(part, 32<<10)))
		if _, e = r.ReadMIMEHeader(); e != nil {
			return nil
		}
		var reports []deliveryReport
		for j := 0; j < 32; j++ {
			h, e := r.ReadMIMEHeader()
			if e != nil && !errors.Is(e, io.EOF) {
				break
			}
			typ, addr, ok := strings.Cut(h.Get("Final-Recipient"), ";")
			action, status := strings.ToLower(h.Get("Action")), h.Get("Status")
			if ok && strings.EqualFold(typ, "rfc822") && enhancedStatus.MatchString(status) && ((action == "failed" && status[0] == '5') || (action == "delayed" && status[0] == '4') || (action == "delivered" && status[0] == '2')) {
				reports = append(reports, deliveryReport{strings.TrimSpace(addr), action, status})
			}
			if e != nil || len(h) == 0 {
				break
			}
		}
		return reports
	}
	return nil
}
func applyDeliveryReport(tx *sql.Tx, token string, reports []deliveryReport) error {
	for _, r := range reports {
		// Preserve a terminal report if a stale delayed report follows it.
		_, err := tx.Exec(`UPDATE email_outbox SET remote_action=?,remote_status=? WHERE bounce=? AND destination=? COLLATE NOCASE AND remote_action IN ('','delayed')`, r.action, r.status, token, r.recipient)
		if err != nil {
			return err
		}
	}
	return nil
}
