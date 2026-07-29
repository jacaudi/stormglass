package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"tempestwx-utilities/internal/tempestudp"
	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file mirrors internal/sqlite/backfill.go function-for-function. The
// two are deliberately NOT unified: SQLite stores timestamps as epoch INTEGER
// and Postgres as TIMESTAMPTZ, and the two drivers bind parameters
// differently. An abstraction over that difference would be the wrong
// abstraction. The signatures are identical so one consumer-side interface
// satisfies both.

// backfillBatchTimeout bounds one backfill SendBatch. The daemon's hardcoded
// 5s at insertObservations (writer.go:222) was sized for the 1-row live path;
// a backfill batch of up to 200 rows needs its own budget.
const backfillBatchTimeout = 30 * time.Second

// findObservationGapsSQL locates interior holes in each station's series.
//
// PARTITION BY serial_number is NOT optional — see the identical comment in
// internal/sqlite/backfill.go. Without it a multi-hour outage on one of two
// phase-offset stations is undetectable.
const findObservationGapsSQL = `
	SELECT serial_number, prev, ts FROM (
	  SELECT serial_number,
	         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
	         timestamp AS ts
	  FROM tempest_observations
	  WHERE timestamp BETWEEN $1 AND $2
	) q WHERE prev IS NOT NULL AND EXTRACT(EPOCH FROM (ts - prev)) > $3
	ORDER BY serial_number, prev
`

// FindObservationGaps returns the interior gaps in [from, to] wider than
// minGap. LAG yields NULL for the first row of each partition, so this finds
// interior gaps ONLY; head/tail/empty are assembled by the caller from
// SeriesBounds.
func FindObservationGaps(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	rows, err := pool.Query(ctx, findObservationGapsSQL, from, to, minGap.Seconds())
	if err != nil {
		return nil, fmt.Errorf("query observation gaps: %w", err)
	}
	defer rows.Close()

	var gaps []weather.Gap
	for rows.Next() {
		var serial string
		var prev, next time.Time
		if err := rows.Scan(&serial, &prev, &next); err != nil {
			return nil, fmt.Errorf("scan observation gap: %w", err)
		}
		gaps = append(gaps, weather.Gap{
			SerialNumber: serial,
			From:         prev.UTC(),
			To:           next.UTC(),
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
	WHERE timestamp BETWEEN $1 AND $2
	GROUP BY serial_number
	ORDER BY serial_number
`

// SeriesBounds returns the first and last observation timestamp held for each
// serial within [from, to]. An empty result means the store holds nothing in
// that window — the first-run case.
func SeriesBounds(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) ([]weather.Bounds, error) {
	rows, err := pool.Query(ctx, seriesBoundsSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("query series bounds: %w", err)
	}
	defer rows.Close()

	var out []weather.Bounds
	for rows.Next() {
		var serial string
		var first, last time.Time
		if err := rows.Scan(&serial, &first, &last); err != nil {
			return nil, fmt.Errorf("scan series bounds: %w", err)
		}
		out = append(out, weather.Bounds{
			SerialNumber: serial,
			First:        first.UTC(),
			Last:         last.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series bounds: %w", err)
	}
	return out, nil
}

// DistinctSerials returns every serial the table has ever held.
//
// UNWINDOWED, deliberately — see the identical comment in
// internal/sqlite/backfill.go. Merging this into SeriesBounds causes a false
// serial mismatch for any station that was quiet during the queried window.
func DistinctSerials(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT serial_number FROM tempest_observations ORDER BY serial_number`)
	if err != nil {
		return nil, fmt.Errorf("query distinct serials: %w", err)
	}
	defer rows.Close()

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

const backfillInsertSQL = `
	INSERT INTO tempest_observations (
		id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

// InsertObservations writes obs idempotently and reports how many rows were
// actually new. CommandTag.RowsAffected() is 0 for a skipped conflict and 1
// for an insert — the same semantics as SQLite's per-row RowsAffected, and
// the value the daemon path currently discards (writer.go:250).
//
// pgx SendBatch is all-or-nothing per batch, so the count is only meaningful
// once every Exec has succeeded. The caller bounds len(obs).
func InsertObservations(ctx context.Context, pool *pgxpool.Pool, obs []weather.Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(ctx, backfillBatchTimeout)
	defer cancel()

	b := &pgx.Batch{}
	for _, o := range obs {
		b.Queue(backfillInsertSQL,
			uuid.Must(uuid.NewV7()), o.SerialNumber, o.Timestamp,
			o.WindLull, o.WindAvg, o.WindGust, o.WindDirection, o.WindSampleInterval,
			o.Pressure, o.TempAir, wetBulb(o), o.Humidity,
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, o.PrecipType,
			o.LightningDistance, o.LightningStrikeCount,
			o.Battery, o.ReportInterval)
	}

	br := pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	inserted := 0
	for i := range obs {
		tag, err := br.Exec()
		if err != nil {
			return 0, fmt.Errorf("insert observation %d: %w", i, err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// wetBulb derives temp_wetbulb, which the REST API does not return. It uses
// the same tempestudp.WetBulbTemperatureC + math.IsNaN guard the UDP ingest
// path uses, single-sourced, so backfilled and live rows are
// indistinguishable. Any missing input yields NULL rather than a value
// computed from a zero.
//
// This is duplicated (four lines) in internal/sqlite/backfill.go rather than
// shared. The formula itself already lives in exactly one place
// (tempestudp.WetBulbTemperatureC); what's duplicated here is only the nil
// guard, and four lines is cheaper than a new cross-package dependency. Note
// the counter-argument is real: "wet bulb is NULL unless temp, humidity and
// pressure are all present" is shared knowledge under the DRY test, and if it
// ever changes both copies must change together.
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
