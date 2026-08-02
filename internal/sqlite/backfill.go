package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"tempestwx-utilities/internal/tempestudp"
	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
)

// This file holds the backfill tool's read/write path. Everything here is a
// package-level function taking *sql.DB, never a method on Writer:
// Writer.run is documented as "the only goroutine that ever touches db"
// (writer.go:161,236), so an insert method on that type would breach the
// single-writer invariant and be callable from the daemon. Nothing here
// starts a goroutine.
//
// IMPORTANT: sqlite.Open sets db.SetMaxOpenConns(1) (db.go:74). Every query
// below fully materializes its result slice and closes its *sql.Rows before
// returning. A streaming iterator that yielded rows while the caller inserted
// would deadlock on the single connection with no error and no timeout. Do
// not refactor these into iterators.

// findObservationGapsSQL locates interior holes in each station's series.
//
// PARTITION BY serial_number is NOT optional. The series is identified by
// (serial_number, timestamp) — the same uniqueness contract idempotency
// relies on. Without partitioning, two stations phase-offset by ~30s produce
// a merged sequence in which no consecutive interval ever exceeds minGap, so
// a multi-hour outage on one station becomes undetectable and the tool
// reports "no gaps" and exits 0. The same failure hides a hardware swap
// (a new serial) behind an apparently continuous sequence.
const findObservationGapsSQL = `
	SELECT serial_number, prev, timestamp FROM (
	  SELECT serial_number,
	         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
	         timestamp
	  FROM tempest_observations
	  WHERE timestamp BETWEEN ? AND ?
	) WHERE prev IS NOT NULL AND timestamp - prev > ?
	ORDER BY serial_number, prev
`

// FindObservationGaps returns the interior gaps in [from, to] wider than
// minGap, one per (serial, hole).
//
// LAG yields NULL for the first row of each partition, so this finds interior
// gaps ONLY. Head and tail gaps — and the empty-table case — are assembled by
// the caller from SeriesBounds; see internal/backfill.
func FindObservationGaps(ctx context.Context, db *sql.DB, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	rows, err := db.QueryContext(ctx, findObservationGapsSQL, from.Unix(), to.Unix(), int64(minGap.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("query observation gaps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var gaps []weather.Gap
	for rows.Next() {
		var serial string
		var prev, next int64
		if err := rows.Scan(&serial, &prev, &next); err != nil {
			return nil, fmt.Errorf("scan observation gap: %w", err)
		}
		gaps = append(gaps, weather.Gap{
			SerialNumber: serial,
			From:         time.Unix(prev, 0).UTC(),
			To:           time.Unix(next, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation gaps: %w", err)
	}
	return gaps, nil
}

const seriesBoundsSQL = `
	SELECT serial_number, MIN(timestamp), MAX(timestamp)
	FROM tempest_observations
	WHERE timestamp BETWEEN ? AND ?
	GROUP BY serial_number
	ORDER BY serial_number
`

// SeriesBounds returns the first and last observation timestamp held for each
// serial within [from, to]. An empty result means the store holds nothing in
// that window — the first-run case, where the whole range is one gap.
func SeriesBounds(ctx context.Context, db *sql.DB, from, to time.Time) ([]weather.Bounds, error) {
	rows, err := db.QueryContext(ctx, seriesBoundsSQL, from.Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("query series bounds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []weather.Bounds
	for rows.Next() {
		var serial string
		var first, last int64
		if err := rows.Scan(&serial, &first, &last); err != nil {
			return nil, fmt.Errorf("scan series bounds: %w", err)
		}
		out = append(out, weather.Bounds{
			SerialNumber: serial,
			First:        time.Unix(first, 0).UTC(),
			Last:         time.Unix(last, 0).UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series bounds: %w", err)
	}
	return out, nil
}

// DistinctSerials returns every serial the table has ever held.
//
// UNWINDOWED, deliberately. This is the pre-flight check's input, and it must
// NOT be replaced by SeriesBounds' key set: SeriesBounds is windowed, so a
// station that was simply quiet during the queried window would look absent
// from the store entirely and trip a false serial mismatch — breaking
// `backfill --from X --to Y`, which is the tool's main repair path.
func DistinctSerials(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT serial_number FROM tempest_observations ORDER BY serial_number`)
	if err != nil {
		return nil, fmt.Errorf("query distinct serials: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, fmt.Errorf("scan distinct serial: %w", err)
		}
		out = append(out, serial)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct serials: %w", err)
	}
	return out, nil
}

// InsertObservations writes obs idempotently and reports how many rows were
// actually new.
//
// The count is what makes the permanent-hole tradeoff visible: if the station
// was genuinely offline, the API has no data either, and inserted stays 0
// across runs. ON CONFLICT (serial_number, timestamp) DO NOTHING is backed by
// a real UNIQUE constraint (migrations/0002_init.sql:22), and per-row
// RowsAffected returns 0 for a skipped conflict and 1 for an insert.
//
// The count is returned only after a successful Commit — execBatch rolls the
// whole transaction back on any row error.
//
// The caller bounds len(obs) to keep the transaction short; see the design's
// "Concurrency with a running daemon".
//
// Binding is direct from weather.Observation rather than through the private
// observationRow used by the UDP path: that type's leading fields are
// non-pointer float64, so routing through it would coerce a JSON null to 0.0.
func InsertObservations(ctx context.Context, db *sql.DB, obs []weather.Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}

	inserted := 0
	err := execBatch(ctx, db, insertObservationSQL, obs, func(stmt *sql.Stmt, o weather.Observation) error {
		res, err := stmt.ExecContext(ctx,
			uuid.Must(uuid.NewV7()).String(), o.SerialNumber, o.Timestamp.Unix(),
			o.WindLull, o.WindAvg, o.WindGust, o.WindDirection, o.WindSampleInterval,
			o.Pressure, o.TempAir, wetBulb(o), o.Humidity,
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, asPrecipType(o.PrecipType),
			o.LightningDistance, o.LightningStrikeCount,
			o.Battery, o.ReportInterval)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		inserted += int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// asPrecipType converts a possibly-nil *float64 into a possibly-nil *int64
// for precip_type, which is INTEGER in both stores. Unlike the three
// measurement columns, precip_type is a categorical enum (0 none, 1 rain,
// 2 hail, 3 rain + hail), so a fractional value is corrupt input and
// int64(...) truncation -- matching the UDP path at writer.go's
// handleObservationReport -- is the intended coercion, not data loss. Nil
// maps to SQL NULL.
func asPrecipType(p *float64) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}

// wetBulb derives temp_wetbulb, which the REST API does not return.
//
// It uses the same tempestudp.WetBulbTemperatureC + math.IsNaN guard the UDP
// ingest path uses (writer.go:410,432-434), single-sourced, so backfilled and
// live rows are indistinguishable. Change the formula and both paths change
// together: shared knowledge, not shared shape.
//
// Any missing input yields NULL rather than a value computed from a zero —
// the same reason the decode preserves nulls in the first place.
func wetBulb(o weather.Observation) *float64 {
	if o.TempAir == nil || o.Humidity == nil || o.Pressure == nil {
		return nil
	}
	v := tempestudp.WetBulbTemperatureC(*o.TempAir, *o.Humidity, *o.Pressure)
	if math.IsNaN(v) {
		return nil
	}
	return &v
}
