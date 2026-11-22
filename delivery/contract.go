// Package delivery defines communication-only ports implemented by Runtime.
package delivery

import "context"

// Envelope is an immutable delivery obligation. ID is stable across retries.
// Payload is deliberately absent: Runtime fetches content through authorised reads.
type Envelope struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	Seat      string `json:"seat"`
}
type Receipt struct {
	ID         string `json:"id"`
	MessageID  string `json:"message_id"`
	Seat       string `json:"seat"`
	Assignment string `json:"assignment"`
	Generation int64  `json:"generation"`
	State      string `json:"state"` // received: durable Runtime inbox, never provider injection
}

// Receiver must durably deduplicate ID, reject changed envelopes, and commit under
// current Runtime assignment authority. A timeout is unknown and retried with ID.
// Receiving does not acknowledge reading or accept the underlying task.
type Receiver interface {
	Receive(context.Context, Envelope) (Receipt, error)
}
