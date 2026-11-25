package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// serveMCP adapts framing only. The authenticated server owns tool discovery and execution.
func serveMCP(ctx context.Context, p profile, input io.Reader, output io.Writer) error {
	lines := bufio.NewScanner(input)
	lines.Buffer(make([]byte, 4096), 240001)
	encoder := json.NewEncoder(output)
	for lines.Scan() {
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var frame struct{ ID json.RawMessage }
		if err := json.Unmarshal(line, &frame); err != nil {
			return errors.New("invalid MCP frame")
		}
		result, err := forwardMCP(ctx, p, line)
		if err != nil {
			if len(frame.ID) == 0 {
				continue
			}
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "error": map[string]any{"code": -32000, "message": err.Error()}}); err != nil {
				return err
			}
		} else if len(result) > 0 {
			if err := encoder.Encode(result); err != nil {
				return err
			}
		}
	}
	return lines.Err()
}

func forwardMCP(ctx context.Context, p profile, line []byte) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Origin+"/mcp", bytes.NewReader(line))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("inbox connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inbox HTTP %d", resp.StatusCode)
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit || !json.Valid(data) {
		return nil, errors.New("invalid or oversized inbox response")
	}
	return data, nil
}
