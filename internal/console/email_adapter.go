package console

import (
	"context"
	"strings"
)

type letterAdapter struct{ server *server }

func (a letterAdapter) matches(address string) bool { return strings.Contains(address, "@") }
func (a letterAdapter) resolve(ctx context.Context, src string, q *sendFields) (*deliveryRoute, error) {
	_, domain, _ := strings.Cut(q.To, "@")
	localDomain, err := a.server.hosted(ctx, strings.ToLower(domain))
	if err != nil {
		return nil, err
	}
	if localDomain && q.ReplyTo == "" {
		box, err := a.server.emailBox(ctx, q.To)
		if err == nil {
			q.To = box
		}
		return nil, err
	}
	if (q.FileName != "" || q.FileData != nil) && (!fileNameRE.MatchString(q.FileName) || len(q.FileData) > 96<<10 || len(q.FileData) == 0) {
		return nil, invalidAttachment()
	}
	if q.ReplyTo != "" {
		q.To = strings.TrimPrefix(q.To, "external:")
	}
	if !a.server.maySend(src) {
		return nil, failure{403, "External email sending is disabled for this mailbox"}
	}
	if !validEmail(q.To) || localDomain || strings.ContainsAny(q.Subject, "\r\n\x00") || (q.Intent != "" && q.Intent != "fyi") {
		return nil, failure{400, "Use an external address, single-line subject and FYI intent"}
	}
	return &deliveryRoute{replyAddress: "external:" + q.To, enqueue: a.server.enqueueLetter, wake: a.wake}, nil
}
func (a letterAdapter) wake() {
	select {
	case a.server.email.wake <- struct{}{}:
	default:
	}
}
func (a letterAdapter) detail(ctx context.Context, result object, id, src, dst, actor string, desk bool) error {
	if strings.HasPrefix(src, "external:") {
		result["channel"] = "email"
		result["trust"] = "external"
		result["original_available"] = actor == dst || desk
	}
	if strings.Contains(dst, "@") {
		var status string
		if err := a.server.store.Reader().QueryRowContext(ctx, "SELECT state FROM email_outbox WHERE id=?", id).Scan(&status); err != nil {
			return err
		}
		result["email_status"] = status
	}
	return nil
}
