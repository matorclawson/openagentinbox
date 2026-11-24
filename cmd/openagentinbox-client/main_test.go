package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunVerifyUsesSelectedSeatAndChecksMailbox(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAGENTINBOX_PROFILE", filepath.Join(home, "other-profile.json"))

	const name = "fixture-seat"
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	token := strings.Repeat("f", 43)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/agent/whoami" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("unexpected verification request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"Name": name, "mailbox_id": id})
	}))
	defer server.Close()

	path := filepath.Join(home, ".config", "openagentinbox", "seats", name+".json")
	if err := installProfile(path, profile{Origin: server.URL, Name: name, Token: token}); err != nil {
		t.Fatal(err)
	}
	verifyArgs := []string{"--seat", name, "verify", server.URL, name}
	if err := run(t.Context(), append(verifyArgs, id)); err != nil {
		t.Fatalf("verify selected seat: %v", err)
	}
	if err := run(t.Context(), append(verifyArgs, strings.Repeat("b", 32))); err == nil || !strings.Contains(err.Error(), "identity differs") {
		t.Fatalf("verify accepted a different mailbox: %v", err)
	}

	if err := run(t.Context(), []string{"--seat", name, "verify", "https://example.invalid", name}); err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("verify accepted a different workspace: %v", err)
	}
	if err := run(t.Context(), []string{"--seat", name, "join", server.URL, "other-seat"}); err == nil || !strings.Contains(err.Error(), "join name differs") {
		t.Fatalf("join accepted a different seat name: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("verification requests = %d, want 2", got)
	}
}
