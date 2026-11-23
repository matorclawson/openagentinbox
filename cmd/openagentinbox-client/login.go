package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func fileID(value string) string { v := sha256.Sum256([]byte(value)); return hex.EncodeToString(v[:]) }
func hostPath(home, origin string) string {
	return filepath.Join(home, ".config", "openagentinbox", "hosts", fileID(origin), "host.json")
}

func pairingCode(ctx context.Context) (string, error) {
	info, e := os.Stdin.Stat()
	if e != nil {
		return "", e
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		stty := func(arg string) error { c := exec.Command("stty", arg); c.Stdin = os.Stdin; return c.Run() }
		state := exec.Command("stty", "-g")
		state.Stdin = os.Stdin
		before, e := state.Output()
		if e != nil {
			return "", errors.New("cannot read terminal settings; supply the token on stdin")
		}
		if e = stty("-echo"); e != nil {
			return "", e
		}
		defer stty(strings.TrimSpace(string(before)))
		fmt.Fprint(os.Stderr, "Paste the pairing token from Agents and press Enter: ")
		defer fmt.Fprintln(os.Stderr)
	}
	type result struct {
		s string
		e error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := bufio.NewReader(io.LimitReader(os.Stdin, 256)).ReadString('\n')
		ch <- result{strings.TrimSpace(v), e}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.e != nil && !errors.Is(r.e, io.EOF) {
			return "", r.e
		}
		if len(r.s) != 43 {
			return "", errors.New("expected the pairing token from the browser")
		}
		return r.s, nil
	}
}
func login(ctx context.Context, home, origin string) error {
	if !validOrigin(origin) {
		return errors.New("invalid workspace origin")
	}
	code, e := pairingCode(ctx)
	if e != nil {
		return e
	}
	active := hostPath(home, origin)
	pending := filepath.Join(filepath.Dir(active), "pending-"+fileID(code))
	// Save a fresh private key before exchange so an interrupted response can be recovered.
	p, e := ensure(pending, origin, "host")
	if e != nil {
		return e
	}
	label, e := os.Hostname()
	if e != nil {
		return e
	}
	response, e := request(ctx, p, "pair", map[string]string{"code": code, "label": label})
	if e != nil {
		var re remoteError
		if errors.As(e, &re) && re.status == 403 {
			var statusErr error
			response, statusErr = request(ctx, p, "pair/status", nil)
			if statusErr != nil {
				var status remoteError
				if errors.As(statusErr, &status) && status.status == 401 {
					os.Remove(pending)
				}
				return e
			}
		} else {
			return e
		}
	}
	if e = os.Rename(pending, active); e != nil {
		return e
	}
	_, e = os.Stdout.Write(response)
	return e
}
