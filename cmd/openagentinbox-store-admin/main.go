// Offline administrative operations. Run as the service owner, never expose through the public API.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/rodriveracom/openagentinbox/internal/deliverystore"
	"os"
	"strings"
)

func main() {
	db := flag.String("db", "", "SQLite DB")
	fence := flag.String("fence", "", "fence all writes with this reason")
	clear := flag.String("clear-fence", "", "clear only a fence with this EXACT reason")
	seat := flag.String("register-seat", "", "seat to register")
	record := flag.String("record", "", "registry JSON file (required with register-seat)")
	impact := flag.String("rename-impact", "", "inspect old=new recipient overlap and pending deliveries")
	flag.Parse()
	n := 0
	for _, v := range []string{*fence, *clear, *seat, *impact} {
		if v != "" {
			n++
		}
	}
	if *db == "" || n != 1 {
		fmt.Fprintln(os.Stderr, "choose exactly one operation and -db")
		os.Exit(2)
	}
	s, e := deliverystore.Open(*db)
	if e == nil {
		defer s.Close()
		switch {
		case *impact != "":
			old, new, ok := strings.Cut(*impact, "=")
			if !ok || old == "" || new == "" || old == new {
				e = fmt.Errorf("expected distinct old=new seat names")
				break
			}
			var r deliverystore.RenameImpact
			r, e = s.RenameImpact(context.Background(), old, new)
			if e == nil {
				e = json.NewEncoder(os.Stdout).Encode(r)
			}
		case *fence != "":
			e = s.SetFence(*fence)
		case *clear != "":
			e = s.ClearFence(*clear)
		case *seat != "":
			var b []byte
			b, e = os.ReadFile(*record)
			if e == nil && len(b) > 1<<20 {
				e = fmt.Errorf("record exceeds bound")
			}
			if e == nil {
				var r struct {
					Generation int    `json:"message_generation"`
					Type       string `json:"seat_type"`
					Status     string `json:"status"`
				}
				e = json.Unmarshal(b, &r)
				if e == nil {
					e = s.PutSeat(deliverystore.SeatRec{Seat: *seat, Generation: r.Generation, SeatType: r.Type, Status: r.Status, Record: b})
				}
			}
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
