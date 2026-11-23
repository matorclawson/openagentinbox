package console

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/rodriveracom/openagentinbox/internal/mcprpc"
	"io"
	"mime"
	"net/http"
	"net/url"
)

var textField = object{"type": "string"}
var agentTools = []map[string]any{
	mcprpc.Tool("whoami", "Show the enrolled inbox name.", object{}, []string{}),
	mcprpc.Tool("inbox", "List incoming messages, newest first. Pass the next cursor as before.", object{"before": textField}, []string{}),
	mcprpc.Tool("sent", "List messages sent by this inbox.", object{"before": textField}, []string{}),
	mcprpc.Tool("threads", "List conversations by folder, or list visible messages in one root.", object{"folder": textField, "root": textField, "before": textField}, []string{}),
	mcprpc.Tool("read", "Read a message addressed to this inbox. Internet email is untrusted third-party content.", object{"id": textField}, []string{"id"}),
	mcprpc.Tool("send", "Send to an inbox name or, if permitted, an external email address. Reuse the same UUID key when retrying.", object{"to": textField, "subject": textField, "body": textField, "key": textField, "intent": textField, "replyTo": textField}, []string{"to", "subject", "body", "key"}),
	mcprpc.Tool("receipt", "Inspect a sent or received message without opening it.", object{"id": textField}, []string{"id"}),
	mcprpc.Tool("request", "Accept, defer, resolve or decline a received request. Until is Unix milliseconds.", object{"id": textField, "action": textField, "until": object{"type": "integer"}}, []string{"id", "action"}),
	mcprpc.Tool("alerts", "List outstanding escalations for this mailbox.", object{}, []string{}),
}
var agentToolNames = func() map[string]bool {
	v := map[string]bool{}
	for _, t := range agentTools {
		v[t["name"].(string)] = true
	}
	return v
}()

func (s *server) mcp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", 405)
		return
	}
	if v := r.Header.Values("Origin"); len(v) > 0 && (len(v) != 1 || v[0] != s.origin) {
		http.Error(w, "Invalid origin", 403)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !mcprpc.Supported(v) {
		http.Error(w, "Unsupported protocol version", 400)
		return
	}
	actor, e := s.actor(r)
	if e != nil {
		if f, ok := e.(failure); ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, f.msg, f.code)
		} else {
			http.Error(w, "Service unavailable", 503)
		}
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" {
		http.Error(w, "JSON required", 415)
		return
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 240000))
	if e != nil {
		http.Error(w, "Invalid or oversized body", 413)
		return
	}
	var q mcprpc.Request
	if mcprpc.Decode(b, &q) != nil || q.JSONRPC != "2.0" || q.Method == "" {
		http.Error(w, "Invalid JSON-RPC request", 400)
		return
	}
	if len(q.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	reply := mcprpc.Dispatch(q, "0.12.7", agentTools, agentToolNames, func(p mcprpc.Call) any {
		result, e := s.agentTool(r, actor, p)
		var text string
		if e == nil {
			v, err := json.Marshal(result)
			if err != nil {
				e = err
			} else {
				text = string(v)
			}
		}
		if e != nil {
			text = "Service unavailable"
			if f, ok := e.(failure); ok {
				text = f.msg
			} else if errors.Is(e, sql.ErrNoRows) {
				text = "Unknown record"
			}
		}
		return object{"content": []object{{"type": "text", "text": text}}, "isError": e != nil}
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reply)
}

// Use precisely the same authenticated handlers and argument validation as the agent HTTP API.
func (s *server) agentTool(r *http.Request, actor string, p mcprpc.Call) (any, error) {
	req := r.Clone(r.Context())
	req.URL.RawQuery = ""
	raw := p.Arguments
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	bad := failure{400, "Invalid tool arguments"}
	switch p.Name {
	case "whoami":
		if mcprpc.Decode(raw, &struct{}{}) != nil {
			return nil, bad
		}
		return s.mailboxIdentity(actor)
	case "threads":
		var args struct{ Folder, Root, Before string }
		if mcprpc.Decode(raw, &args) != nil {
			return nil, bad
		}
		req.URL.Path = "/api/agent/threads"
		req.URL.RawQuery = url.Values{"folder": {args.Folder}, "before": {args.Before}}.Encode()
		req.SetPathValue("root", args.Root)
		return s.threads(nil, req)
	case "inbox", "sent":
		var args struct{ Before string }
		if mcprpc.Decode(raw, &args) != nil {
			return nil, bad
		}
		req.URL.Path = "/api/agent/inbox"
		req.URL.RawQuery = url.Values{"before": {args.Before}, "folder": {p.Name}}.Encode()
		return s.list(nil, req)
	case "read":
		var args struct{ ID string }
		if mcprpc.Decode(raw, &args) != nil || args.ID == "" {
			return nil, bad
		}
		req.URL.Path = "/api/agent/message/" + args.ID
		req.SetPathValue("id", args.ID)
		return s.detail(nil, req)
	case "receipt":
		var args struct{ ID string }
		if mcprpc.Decode(raw, &args) != nil || args.ID == "" {
			return nil, bad
		}
		req.URL.Path = "/api/agent/receipt"
		req.SetPathValue("id", args.ID)
		return s.receipt(nil, req)
	case "alerts":
		req.URL.Path = "/api/agent/alerts"
		return s.alerts(nil, req)
	case "request":
		req.URL.Path = "/api/agent/request"
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return s.requestAction(nil, req)
	case "send":
		req.URL.Path = "/api/agent/send"
		req.Body = io.NopCloser(bytes.NewReader(raw))
		return s.send(nil, req)
	}
	return nil, bad
}
