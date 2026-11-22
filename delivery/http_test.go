package delivery

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

type transport struct{ handler http.Handler }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}
func TestAuthenticatedHTTPDelivery(t *testing.T) {
	i, e := OpenInbox(filepath.Join(t.TempDir(), "private", "db"), &authority{generation: 2})
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	h := Handler(i, func(r *http.Request) bool { return r.Header.Get("Authorization") == "test" })
	request := httptest.NewRequest("POST", "/receive", bytes.NewBufferString(`{"id":"a","message_id":"m","seat":"reader","generation":99}`))
	request.Header.Set("Authorization", "test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 400 {
		t.Fatal("caller supplied generation", w.Code)
	}
	client := HTTPReceiver{URL: "http://runtime/receive", Client: &http.Client{Transport: transport{Handler(i, func(*http.Request) bool { return true })}}}
	r, e := client.Receive(context.Background(), Envelope{"a", "m", "reader"})
	if e != nil || r.Generation != 2 {
		t.Fatal(r, e)
	}
	denied := HTTPReceiver{URL: client.URL, Client: &http.Client{Transport: transport{h}}}
	if _, e = denied.Receive(context.Background(), Envelope{"b", "m", "reader"}); e == nil {
		t.Fatal("unauthenticated delivery")
	}
}
