package mcprpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// strictJSON decodes exactly one JSON value into v: no duplicate name at any depth, no unknown field, nothing after it.
func Decode(b []byte, v any) error {
	if err := noDuplicateNames(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

func noDuplicateNames(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func() error
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				ks, _ := k.(string)
				if seen[ks] {
					return fmt.Errorf("duplicate name %q", ks)
				}
				seen[ks] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	return walk()
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func Tool(name, desc string, props map[string]any, req []string) map[string]any {
	return map[string]any{"name": name, "description": desc,
		"inputSchema": map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}}
}

type Call struct {
	Meta      map[string]json.RawMessage `json:"_meta,omitempty"`
	Name      string                     `json:"name"`
	Arguments json.RawMessage            `json:"arguments"`
}

func Supported(v string) bool { return v == "2025-03-26" || v == "2025-06-18" || v == "2025-11-25" }
func Dispatch(q Request, version string, tools []map[string]any, names map[string]bool, call func(Call) any) map[string]any {
	resp := map[string]any{"jsonrpc": "2.0", "id": q.ID}
	switch q.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(q.Params, &params)
		v := params.ProtocolVersion
		if !Supported(v) {
			v = "2025-11-25"
		}
		resp["result"] = map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "openagentinbox", "version": version}}
	case "ping":
		resp["result"] = map[string]any{}
	case "tools/list":
		resp["result"] = map[string]any{"tools": tools}
	case "tools/call":
		var p Call
		if Decode(q.Params, &p) != nil || !names[p.Name] {
			resp["error"] = map[string]any{"code": -32602, "message": "unknown tool or bad params"}
			break
		}
		result := call(p)
		if result == nil {
			return nil
		}
		resp["result"] = result
	default:
		resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}
	return resp
}
