package main

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// The paired CLI exercises the same watch hub as a seat, without notifying a harness.
func probeHost(ctx context.Context, p profile) {
	last := ""
	for ctx.Err() == nil {
		var watch, fetch struct{ Marker string }
		b, err := request(ctx, p, "probe/watch?after="+url.QueryEscape(last), nil)
		if err == nil {
			err = json.Unmarshal(b, &watch)
		}
		if err == nil && watch.Marker != "" && watch.Marker != last {
			b, err = request(ctx, p, "probe/fetch", nil)
			if err == nil {
				err = json.Unmarshal(b, &fetch)
			}
			if err == nil && fetch.Marker == watch.Marker {
				_, err = request(ctx, p, "probe/confirm", map[string]string{"marker": fetch.Marker})
				if err == nil {
					last = fetch.Marker
				}
			}
		}
		if err != nil {
			pause(ctx, 5*time.Second)
		}
	}
}
