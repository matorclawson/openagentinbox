package console

import (
	"context"
	"database/sql"
	"time"

	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
)

type sendFields struct {
	To, Subject, Body, Key, Intent, ReplyTo, FileName string
	FileData                                          []byte
}

// enqueue writes the transport obligation inside AcceptMail. wake is a lossy
// after-commit hint; delivery remains recoverable if the hint never runs.
type deliveryRoute struct {
	replyAddress string
	enqueue      func(*sql.Tx, deliverystore.Message, string, []byte) error
	wake         func()
}

type deliveryAdapter interface {
	matches(address string) bool
	resolve(context.Context, string, *sendFields) (*deliveryRoute, error)
	detail(context.Context, object, string, string, string, string, bool) error
}

func (s *server) deliveryAdapters() []deliveryAdapter {
	return []deliveryAdapter{letterAdapter{s}}
}

func invalidAttachment() error {
	return failure{400, "External attachment needs a safe filename and 1 to 98304 bytes"}
}

func (s *server) resolveDelivery(ctx context.Context, sender string, q *sendFields) (*deliveryRoute, error) {
	for _, adapter := range s.deliveryAdapters() {
		if adapter.matches(q.To) {
			route, err := adapter.resolve(ctx, sender, q)
			if err != nil || route != nil {
				return route, err
			}
			break // The adapter resolved a hosted address to a local mailbox.
		}
	}
	if q.FileName != "" || q.FileData != nil {
		return nil, invalidAttachment()
	}
	return nil, nil
}

// acceptInbound commits an already authenticated transport's mailbox projection
// and its own durable record before acknowledging the transport.
func (s *server) acceptInbound(ctx context.Context, key string, m deliverystore.Message, record func(*sql.Tx) error) (string, error) {
	id, err := s.store.AcceptMail(ctx, key, m, time.Now(), record)
	if err == nil {
		s.live.signal()
	}
	return id, err
}
