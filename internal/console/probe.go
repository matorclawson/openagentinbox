package console

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

func (s *server) advanceProbes(ctx context.Context, at int64) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO host_probes(host,marker,issued,started) SELECT id,lower(hex(randomblob(16))),?,? FROM paired_cli c WHERE paired_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM host_probes p WHERE p.host=c.id)`, at, at); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE host_probes SET incident=? WHERE incident=0 AND max(started,confirmed)<=?", at, at-180000)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.live.signal()
	}
	rows, err := s.db.QueryContext(ctx, "UPDATE host_probes SET marker=lower(hex(randomblob(16))),issued=? WHERE host IN (SELECT host FROM host_probes WHERE issued<=? ORDER BY issued LIMIT 128) RETURNING host", at, at-60000)
	if err != nil {
		return err
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		hosts = append(hosts, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range hosts {
		s.store.SignalProbe(id)
	}
	return nil
}
func (s *server) probe(_ http.ResponseWriter, r *http.Request) (any, error) {
	var host string
	if err := s.store.Reader().QueryRowContext(r.Context(), "SELECT id FROM paired_cli WHERE credential_hash=?", hash(bearer(r))).Scan(&host); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, failure{401, "Pair this host first"}
		}
		return nil, err
	}
	current := func() (string, error) {
		var marker string
		err := s.store.Reader().QueryRowContext(r.Context(), "SELECT marker FROM host_probes WHERE host=?", host).Scan(&marker)
		return marker, err
	}
	switch r.PathValue("op") {
	case "watch":
		w, err := s.store.WatchSeat("probe/"+host, false)
		if err != nil {
			return nil, failure{429, "Too many host watchers"}
		}
		defer w.Close()
		marker, err := current()
		if err != nil {
			return nil, err
		}
		if marker == r.URL.Query().Get("after") {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-s.live.draining:
				return nil, failure{503, "Server restarting"}
			case <-w.Changes():
			case <-time.After(50 * time.Second):
			}
			marker, err = current()
		}
		return object{"marker": marker}, err
	case "fetch":
		marker, err := current()
		return object{"marker": marker}, err
	case "confirm":
		if r.Method != "POST" {
			return nil, failure{405, "POST required"}
		}
		var q struct{ Marker string }
		if err := input(r, &q); err != nil {
			return nil, err
		}
		res, err := s.db.ExecContext(r.Context(), "UPDATE host_probes SET confirmed=?,incident=0 WHERE host=? AND marker=? AND confirmed<issued", now(), host, q.Marker)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			marker, err := current()
			if err != nil {
				return nil, err
			}
			if marker != q.Marker {
				return nil, failure{409, "Probe expired"}
			}
		} else {
			s.live.signal()
		}
		return object{"ok": true}, nil
	}
	return nil, failure{404, "Unknown probe operation"}
}
func (s *server) listenerCheck(_ http.ResponseWriter, r *http.Request) (any, error) {
	seat, err := s.actor(r)
	if err != nil {
		return nil, err
	}
	var q struct{ Mode string }
	if err = input(r, &q); err != nil {
		return nil, err
	}
	switch q.Mode {
	case "available", "busy", "focus", "mute", "digest":
	default:
		return nil, failure{400, "Unknown listener mode"}
	}
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO listener_checks VALUES(?,?,?) ON CONFLICT(seat) DO UPDATE SET at=excluded.at,mode=excluded.mode WHERE listener_checks.at<=excluded.at-30000 OR listener_checks.mode!=excluded.mode`, seat, now(), q.Mode)
	return object{"ok": true}, err
}
