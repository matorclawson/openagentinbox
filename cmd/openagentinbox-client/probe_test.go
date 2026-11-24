package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeConfirmsMatchingMarkerAndAdvancesWatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	const marker = "fixture+/marker"
	steps := []struct {
		method, path, after, response string
	}{
		{http.MethodGet, "probe/watch", "", `{"marker":"stale"}`},
		{http.MethodGet, "probe/fetch", "", `{"marker":"mismatched"}`},
		{http.MethodGet, "probe/watch", "", `{"marker":"fixture+/marker"}`},
		{http.MethodGet, "probe/fetch", "", `{"marker":"fixture+/marker"}`},
		{http.MethodPost, "probe/confirm", "", `{}`},
		{http.MethodGet, "probe/watch", marker, `{}`},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(requests.Add(1)) - 1
		if i >= len(steps) {
			t.Errorf("unexpected request after confirmed marker: %s %s", r.Method, r.URL.RequestURI())
			cancel()
			return
		}
		want := steps[i]
		if r.Method != want.method || r.URL.Path != "/api/agent/"+want.path || r.URL.Query().Get("after") != want.after {
			t.Errorf("request %d = %s %s, want %s %s after %q", i, r.Method, r.URL.RequestURI(), want.method, want.path, want.after)
			cancel()
			return
		}
		if want.path == "probe/confirm" {
			var body struct{ Marker string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Marker != marker {
				t.Errorf("confirmation marker = %q, decode error = %v", body.Marker, err)
			}
		}
		if i == len(steps)-1 {
			cancel()
		}
		_, _ = w.Write([]byte(want.response))
	}))
	defer server.Close()

	probeHost(ctx, profile{Origin: server.URL})
	if got := requests.Load(); int(got) != len(steps) {
		t.Fatalf("probe requests = %d, want %d", got, len(steps))
	}
}
