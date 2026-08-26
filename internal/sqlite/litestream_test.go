package sqlite

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempestudp"
)

// selectAllObservationsSQL mirrors selectLatestObservationSQL's column list
// (writer.go) exactly, without the ORDER BY ... DESC LIMIT 1: this test
// needs every row, not just the newest.
const selectAllObservationsSQL = `
	SELECT id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	FROM stormglass_observations
	ORDER BY timestamp
`

// queryAllObservations returns every stormglass_observations row in db, ordered
// by timestamp, as Observation values — writer.go's own exported read type —
// rather than a second, parallel row type: the column layout and NULL
// handling are the same knowledge LatestObservation already encodes, so this
// reuses that shape (see LatestObservation's Scan block, which this mirrors
// for N rows instead of 1) rather than duplicating it.
func queryAllObservations(t *testing.T, db *sql.DB) []Observation {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), selectAllObservationsSQL)
	if err != nil {
		t.Fatalf("query stormglass_observations: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var got []Observation
	for rows.Next() {
		var (
			obs                                                      Observation
			precipType                                               sql.NullInt64
			windSampleInterval, lightningStrikeCount, reportInterval sql.NullFloat64
			tempWetbulb, lightningDistance, battery                  sql.NullFloat64
		)
		if err := rows.Scan(
			&obs.ID, &obs.SerialNumber, &obs.Timestamp,
			&obs.WindLull, &obs.WindAvg, &obs.WindGust, &obs.WindDirection, &windSampleInterval,
			&obs.Pressure, &obs.TempAir, &tempWetbulb, &obs.Humidity,
			&obs.Illuminance, &obs.UVIndex, &obs.Irradiance, &obs.RainRate, &precipType,
			&lightningDistance, &lightningStrikeCount,
			&battery, &reportInterval,
		); err != nil {
			t.Fatalf("scan observation row: %v", err)
		}
		if windSampleInterval.Valid {
			obs.WindSampleInterval = &windSampleInterval.Float64
		}
		if tempWetbulb.Valid {
			obs.TempWetbulb = &tempWetbulb.Float64
		}
		if precipType.Valid {
			obs.PrecipType = &precipType.Int64
		}
		if lightningDistance.Valid {
			obs.LightningDistance = &lightningDistance.Float64
		}
		if lightningStrikeCount.Valid {
			obs.LightningStrikeCount = &lightningStrikeCount.Float64
		}
		if battery.Valid {
			obs.Battery = &battery.Float64
		}
		if reportInterval.Valid {
			obs.ReportInterval = &reportInterval.Float64
		}
		got = append(got, obs)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate observations: %v", err)
	}
	return got
}

// TestLitestreamRestore_FileReplica proves the SQLite PRAGMA/WAL/restore
// contract (design §10) with a real Litestream replicate+restore round-trip
// against a file replica: N observation rows are written through the WS3
// writer, replicated with a forced one-shot snapshot, and restored to a
// fresh path — the restored rows must match the originals field-for-field.
//
// This test is on the DEFAULT build (not `//go:build integration`) so the
// restore contract is proven on every `go test` run, not only when an
// integration-gated suite happens to execute — design §10 names
// "misconfigured PRAGMAs silently lose Litestream data" as the top risk, and
// a routinely-skipped test would leave it unproven. It skips cleanly when the
// litestream binary is absent (CI installs it).
func TestLitestreamRestore_FileReplica(t *testing.T) {
	if _, err := exec.LookPath("litestream"); err != nil {
		t.Skip("litestream not installed")
	}

	ctx := t.Context()

	dbPath := filepath.Join(t.TempDir(), "stormglass.db")
	db, err := Open(ctx, dbPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	w := NewWriter(ctx, db, Config{BatchSize: 100, FlushInterval: time.Hour})

	const n = 5
	for i := range n {
		ts := 1700000000 + int64(i)*60
		obs := []float64{
			float64(ts),
			1.0 + float64(i),       // wind_lull
			2.0 + float64(i),       // wind_avg
			3.0 + float64(i),       // wind_gust
			90 + float64(i)*10,     // wind_direction
			3,                      // wind_sample_interval
			1000 + float64(i),      // pressure (mb)
			15 + float64(i),        // temp_air (C)
			40 + float64(i),        // humidity (%)
			10000 + float64(i)*100, // illuminance
			float64(i % 5),         // uv_index
			400 + float64(i)*10,    // irradiance
			0.1 * float64(i),       // rain_rate
			1,                      // precip_type
			2.0 + float64(i)*0.1,   // lightning_distance
			4,                      // lightning_strike_count
			3.5 + float64(i)*0.1,   // battery
			5,                      // report_interval
		}
		if i == n-1 {
			// One row deliberately has a short obs slice (only indices 0..12,
			// through rain_rate) so the columns that require a longer slice —
			// precip_type (needs len>=14), lightning_distance/lightning_strike_count
			// (len>=16), battery (len>=17), report_interval (len>=18) — round-trip
			// as genuine SQL NULL, rather than every restored row being all-non-NULL.
			// Otherwise this test's "field-for-field" comparison never exercises the
			// nil-pointer branch of Observation's nullable fields. (wind_sample_interval
			// and temp_wetbulb stay non-NULL here: index 5 is present, and wet-bulb is
			// computed from the present temp_air/humidity/pressure.)
			obs = obs[:13]
		}
		report := &tempestudp.TempestObservationReport{
			SerialNumber: "ST-LITESTREAM",
			Obs:          [][]float64{obs},
		}
		if err := w.WriteReport(ctx, report); err != nil {
			t.Fatalf("WriteReport %d: %v", i, err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	want := queryAllObservations(t, db)
	if len(want) != n {
		t.Fatalf("wrote %d rows, want %d", len(want), n)
	}
	// Guard the intent behind the short obs[:13] row above: confirm it
	// actually produced SQL NULLs, so this test's field-for-field comparison
	// genuinely exercises the nil-pointer branch of Observation's nullable
	// fields rather than silently degrading to an all-non-NULL fixture on a
	// future edit.
	if last := want[n-1]; last.PrecipType != nil || last.Battery != nil {
		t.Fatalf("row %d expected nil PrecipType/Battery from the short obs slice, got %+v", n-1, last)
	}

	// Quiesce the DB before handing it to Litestream: Close drains and
	// flushes the writer's goroutine, then closing db itself ensures no
	// connection is held open while litestream reads the file directly.
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	replicaDir := filepath.Join(t.TempDir(), "replica")

	// litestream 0.5.x: replicate a single DB via command-line args (no
	// config file needed). -once performs a single sync then exits;
	// -force-snapshot (requires -once) forces a full snapshot so restore
	// below has something deterministic to restore from without waiting on
	// a background daemon's sync interval.
	replicaURL := "file://" + replicaDir
	replicateOut, err := exec.CommandContext(ctx, "litestream", "replicate",
		"-once", "-force-snapshot",
		dbPath, replicaURL,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("litestream replicate: %v\n%s", err, replicateOut)
	}

	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	restoreOut, err := exec.CommandContext(ctx, "litestream", "restore",
		"-o", restoredPath,
		replicaURL,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("litestream restore: %v\n%s", err, restoreOut)
	}

	restoredDB, err := Open(ctx, restoredPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open restored db: %v", err)
	}
	t.Cleanup(func() {
		if err := restoredDB.Close(); err != nil {
			t.Errorf("close restored db: %v", err)
		}
	})

	got := queryAllObservations(t, restoredDB)
	if len(got) != n {
		t.Fatalf("restored %d rows, want %d", len(got), n)
	}
	for i := range want {
		// reflect.DeepEqual, not ==: Observation carries pointer fields for
		// its nullable columns, and the original/restored rows were scanned
		// from two independent connections, so equal values never share the
		// same pointer — DeepEqual compares what they point to.
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("row %d mismatch:\n got  = %+v\n want = %+v", i, got[i], want[i])
		}
	}
}
