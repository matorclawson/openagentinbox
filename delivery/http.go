package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Handler authenticates a service caller before reading an envelope. The
// receiver independently resolves Runtime authority; request fields cannot
// supply assignment, generation or a receipt. Mount on a protected transport.
func Handler(receiver Receiver, authorize func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorize == nil || !authorize(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		var e Envelope
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&e) != nil {
			http.Error(w, "invalid envelope", 400)
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			http.Error(w, "trailing data", 400)
			return
		}
		if receiver == nil {
			http.Error(w, "receiver unavailable", 503)
			return
		}
		receipt, err := receiver.Receive(r.Context(), e)
		if err != nil {
			status := 503
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			http.Error(w, "delivery unresolved", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(receipt)
	})
}

// HTTPReceiver uses an operator-configured authenticated transport. Request
// cancellation/deadlines belong to the dispatcher. No credential lives here.
type HTTPReceiver struct {
	URL    string
	Client *http.Client
}

func (h HTTPReceiver) Receive(ctx context.Context, e Envelope) (Receipt, error) {
	var out Receipt
	if h.Client == nil {
		return out, errors.New("authenticated client required")
	}
	raw, _ := json.Marshal(e)
	r, err := http.NewRequestWithContext(ctx, "POST", h.URL, bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := h.Client.Do(r)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return out, errors.New("Runtime delivery unresolved")
	}
	dec := json.NewDecoder(io.LimitReader(response.Body, 4097))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&out); err != nil {
		return out, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return out, errors.New("invalid receipt")
	}
	return out, nil
}
