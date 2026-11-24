package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type onboardingSeat struct {
	ID, Name, Ticket string
	Mailbox          string `json:"mailbox_id"`
}

func connectSeat(ctx context.Context, home string, host profile, s onboardingSeat) string {
	if !safeName.MatchString(s.Name) {
		return "invalid-seat"
	}
	path := filepath.Join(home, ".config/openagentinbox/seats", s.Name+".json")
	var p profile
	var err error
	if s.Ticket != "" && s.Mailbox == "" {
		p, err = ensure(path, host.Origin, s.Name)
		if err != nil {
			return "profile-conflict"
		}
		b, e := request(ctx, p, "enroll", map[string]string{"name": s.Name, "ticket": s.Ticket})
		var result struct{ Status string }
		if e != nil || json.Unmarshal(b, &result) != nil || result.Status != "active" {
			return "enrollment-failed"
		}
	} else {
		p, err = readProfile(path)
		if os.IsNotExist(err) {
			return "profile-missing"
		}
		if err != nil || p.Name != s.Name || p.Origin != host.Origin {
			return "profile-conflict"
		}
	}
	b, err := verify(ctx, p, s.Name, s.Mailbox)
	if err != nil {
		return "identity-failed"
	}
	var identity struct {
		ID string `json:"mailbox_id"`
	}
	if json.Unmarshal(b, &identity) != nil {
		return "identity-failed"
	}
	if _, err = request(ctx, p, "inbox", nil); err != nil {
		return "inbox-unavailable"
	}
	if _, err = request(ctx, p, "onboarding/confirm", map[string]string{"id": s.ID, "mailboxID": identity.ID, "hostCredential": host.Token}); err != nil {
		return "confirmation-failed"
	}
	return "verified"
}
func onboard(ctx context.Context, home, origin string, watch bool) error {
	if !validOrigin(origin) {
		return errors.New("invalid workspace origin")
	}
	h, err := readProfile(hostPath(home, origin))
	if err != nil {
		return err
	}
	if h.Origin != origin {
		return errors.New("paired profile belongs to another workspace")
	}
	if watch {
		child, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); probeHost(child, h) }()
		defer func() { cancel(); <-done }()
	}
	for ctx.Err() == nil {
		b, err := request(ctx, h, "onboarding/work", struct{}{})
		if err != nil {
			var re remoteError
			if !watch || errors.As(err, &re) && (re.status == 401 || re.status == 403) {
				return err
			}
			fmt.Fprintln(os.Stderr, "Workspace unavailable; retrying")
			pause(ctx, time.Minute)
			continue
		}
		var work struct{ Seats []onboardingSeat }
		if err = json.Unmarshal(b, &work); err != nil {
			return err
		}
		failed := 0
		for _, s := range work.Seats {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			status := connectSeat(ctx, home, h, s)
			if status != "verified" {
				failed++
				if _, err = request(ctx, h, "onboarding/problem", map[string]string{"id": s.ID, "reason": status}); err != nil {
					fmt.Fprintln(os.Stderr, "Could not report seat status")
				}
			}
			if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"name": s.Name, "status": status}); err != nil {
				return err
			}
		}
		if !watch {
			if failed > 0 {
				return fmt.Errorf("%d of %d seats need attention", failed, len(work.Seats))
			}
			return nil
		}
		pause(ctx, time.Minute)
	}
	return nil
}
