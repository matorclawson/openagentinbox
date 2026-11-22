package deliverystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type Repair struct {
	Op         string `json:"op_id"`
	Seat       string `json:"seat"`
	Runtime    string `json:"runtime,omitempty"`
	Chart      string `json:"chart_rev"`
	Host       string `json:"host,omitempty"`
	Folder     string `json:"folder,omitempty"`
	Generation *int64 `json:"expected_generation,omitempty"`
}
type WakeRow struct {
	Seat       string `json:"seat"`
	Host       string `json:"host"`
	Folder     string `json:"folder"`
	Generation int64  `json:"generation"`
}

var ErrStaleGeneration = errors.New("stale_generation")
var ErrRuntimeMismatch = errors.New("runtime_mismatch")

func (s *Store) WakeMap(ctx context.Context) ([]WakeRow, int64, error) {
	q, e := s.readDB().QueryContext(ctx, "SELECT seat,host,folder,generation FROM wake_map ORDER BY seat")
	if e != nil {
		return nil, 0, e
	}
	defer q.Close()
	rows := []WakeRow{}
	var g int64
	for q.Next() {
		var r WakeRow
		if e = q.Scan(&r.Seat, &r.Host, &r.Folder, &r.Generation); e != nil {
			return nil, 0, e
		}
		g = max(g, r.Generation)
		rows = append(rows, r)
	}
	return rows, g, q.Err()
}

// RepairSeat records the chart revision, previous routing and decision in the
// same transaction as the create or compare-and-set. A retried op_id returns
// the original result even after a later repair advances the map.
func (s *Store) RepairSeat(ctx context.Context, r Repair) (string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	raw, e := json.Marshal(r)
	if e != nil {
		return "", e
	}
	var stored, result, previous string
	e = tx.QueryRowContext(ctx, "SELECT request,result FROM watchdog_ops WHERE id=?", r.Op).Scan(&stored, &result)
	if e == nil {
		if stored != string(raw) {
			return "", ErrConflict
		}
		return result, nil
	}
	if e != sql.ErrNoRows {
		return "", e
	}
	if e = checkFenceTx(ctx, tx); e != nil {
		return "", e
	}
	var rt string
	e = tx.QueryRowContext(ctx, "SELECT coalesce(json_extract(record,'$.runtime'),'') FROM seats WHERE seat=?", r.Seat).Scan(&rt)
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	if r.Generation == nil {
		if e == nil {
			if rt != r.Runtime {
				return "", ErrRuntimeMismatch
			}
			result = "exists"
		} else {
			_, e = tx.ExecContext(ctx, `INSERT INTO seats(seat,generation,seat_type,status,record) VALUES(?,1,'agent','active',json_object('instance_id',?,'runtime',?,'seat_type','agent','status','active','message_generation',1))`, r.Seat, r.Seat, r.Runtime)
			if e != nil {
				return "", e
			}
			result = "created"
		}
	} else {
		if e == sql.ErrNoRows {
			return "", ErrNotRegistered
		}
		if rt != "codex" {
			return "", ErrRuntimeMismatch
		}
		var gen int64
		if e = tx.QueryRowContext(ctx, "SELECT coalesce(max(generation),0) FROM wake_map").Scan(&gen); e != nil {
			return "", e
		}
		if gen != *r.Generation {
			return "", ErrStaleGeneration
		}
		e = tx.QueryRowContext(ctx, "SELECT json_object('host',host,'folder',folder,'generation',generation) FROM wake_map WHERE seat=?", r.Seat).Scan(&previous)
		if e != nil && e != sql.ErrNoRows {
			return "", e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO wake_map VALUES(?,?,?,?) ON CONFLICT(seat) DO UPDATE SET host=excluded.host,folder=excluded.folder,generation=excluded.generation`, r.Seat, r.Host, r.Folder, gen+1)
		if e != nil {
			return "", e
		}
		result = "updated"
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO watchdog_ops VALUES(?,?,?,?)", r.Op, string(raw), result, previous); e != nil {
		return "", e
	}
	return result, tx.Commit()
}

// CreateOnboardSeat uses a single insert, so every existing record, including
// retired seats, survives an approval race unchanged.
func (s *Store) CreateOnboardSeat(ctx context.Context, seat, runtime string) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO seats(seat,generation,seat_type,status,record) VALUES(?,1,'stream','active',json_object('instance_id',?,'seat_type','stream','runtime',?,'status','active','write_authority','none','message_generation',1))`, seat, seat, runtime)
	return e
}
