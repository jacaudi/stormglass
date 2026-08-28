package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/google/uuid"
)

// newTestDB opens a fresh, migrated SQLite DB in a t.TempDir() for use by
// writer tests. FlushInterval is set to an hour so the ticker never fires
// during a test; tests force flushes explicitly via Writer.Flush.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(t.Context(), dbPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return db
}

// newTestWriter constructs a Writer backed by newTestDB, with a large
// batch size and a dormant ticker so tests control flushing explicitly.
func newTestWriter(t *testing.T) *Writer {
	t.Helper()
	db := newTestDB(t)
	w := NewWriter(t.Context(), db, Config{BatchSize: 100, FlushInterval: time.Hour})
	t.Cleanup(func() {
		if err := w.Close(t.Context()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return w
}

// TestWriter_InsertsObservation proves WriteReport on an observation report
// produces a row with exact column values (field-by-field) and a UUIDv7 PK,
// and separately that fields absent from a short obs slice are stored as SQL
// NULL rather than a zero value (the task brief's explicit NULL-handling
// requirement).
func TestWriter_InsertsObservation(t *testing.T) {
	t.Run("full_fields", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		report := &tempestudp.TempestObservationReport{
			SerialNumber: "ST-00001",
			Obs: [][]float64{
				{1700000000, 1.5, 2.0, 2.5, 180, 3, 1013.25, 20.5, 55.0, 50000, 3, 500, 0.5, 1, 2.1, 4, 3.6, 5},
			},
		}

		if err := w.WriteReport(ctx, report); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
		if err := w.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		var (
			id                                         string
			serial                                     string
			ts                                         int64
			windLull, windAvg, windGust, windDirection float64
			windSampleInterval                         float64
			pressure, tempAir, tempWetbulb, humidity   float64
			illuminance, uvIndex, irradiance, rainRate float64
			precipType                                 int64
			lightningDistance                          float64
			lightningStrikeCount                       float64
			battery                                    float64
			reportInterval                             float64
		)
		row := w.db.QueryRowContext(ctx, `SELECT
			id, serial_number, timestamp,
			wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
			pressure, temp_air, temp_wetbulb, humidity,
			illuminance, uv_index, irradiance, rain_rate, precip_type,
			lightning_distance, lightning_strike_count,
			battery, report_interval
			FROM stormglass_observations`)
		if err := row.Scan(
			&id, &serial, &ts,
			&windLull, &windAvg, &windGust, &windDirection, &windSampleInterval,
			&pressure, &tempAir, &tempWetbulb, &humidity,
			&illuminance, &uvIndex, &irradiance, &rainRate, &precipType,
			&lightningDistance, &lightningStrikeCount,
			&battery, &reportInterval,
		); err != nil {
			t.Fatalf("scan observation row: %v", err)
		}

		parsedID, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("id %q is not a valid UUID: %v", id, err)
		}
		if parsedID.Version() != 7 {
			t.Errorf("id version = %d, want 7 (UUIDv7)", parsedID.Version())
		}

		if serial != "ST-00001" {
			t.Errorf("serial = %q, want ST-00001", serial)
		}
		if ts != 1700000000 {
			t.Errorf("timestamp = %d, want 1700000000", ts)
		}
		if windLull != 1.5 {
			t.Errorf("windLull = %v, want 1.5", windLull)
		}
		if windAvg != 2.0 {
			t.Errorf("windAvg = %v, want 2.0", windAvg)
		}
		if windGust != 2.5 {
			t.Errorf("windGust = %v, want 2.5", windGust)
		}
		if windDirection != 180.0 {
			t.Errorf("windDirection = %v, want 180.0", windDirection)
		}
		if windSampleInterval != 3 {
			t.Errorf("windSampleInterval = %v, want 3", windSampleInterval)
		}
		if pressure != 1013.25 {
			t.Errorf("pressure = %v, want 1013.25", pressure)
		}
		if tempAir != 20.5 {
			t.Errorf("tempAir = %v, want 20.5", tempAir)
		}
		if humidity != 55.0 {
			t.Errorf("humidity = %v, want 55.0", humidity)
		}
		if illuminance != 50000.0 {
			t.Errorf("illuminance = %v, want 50000.0", illuminance)
		}
		if uvIndex != 3.0 {
			t.Errorf("uvIndex = %v, want 3.0", uvIndex)
		}
		if irradiance != 500.0 {
			t.Errorf("irradiance = %v, want 500.0", irradiance)
		}
		if rainRate != 0.5 {
			t.Errorf("rainRate = %v, want 0.5", rainRate)
		}
		if precipType != 1 {
			t.Errorf("precipType = %v, want 1", precipType)
		}
		if lightningDistance != 2.1 {
			t.Errorf("lightningDistance = %v, want 2.1", lightningDistance)
		}
		if lightningStrikeCount != 4 {
			t.Errorf("lightningStrikeCount = %v, want 4", lightningStrikeCount)
		}
		if battery != 3.6 {
			t.Errorf("battery = %v, want 3.6", battery)
		}
		if reportInterval != 5 {
			t.Errorf("reportInterval = %v, want 5", reportInterval)
		}

		// Wet bulb is computed, not a literal input; just assert it's a
		// plausible convergent value (non-zero, less than air temp).
		if tempWetbulb <= 0 || tempWetbulb >= tempAir {
			t.Errorf("tempWetbulb = %v, want a plausible convergent value below tempAir=%v", tempWetbulb, tempAir)
		}
	})

	t.Run("nullable_fields_absent", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		// Minimum valid length (13, indices 0-12): fields 13-17 (precip_type,
		// lightning distance/count, battery, report_interval) are absent ->
		// must read back as SQL NULL, not zero.
		report := &tempestudp.TempestObservationReport{
			SerialNumber: "ST-SHORT",
			Obs: [][]float64{
				{1700000001, 1, 1, 1, 1, 1, 1013, 20, 50, 100, 1, 10, 0.5},
			},
		}

		if err := w.WriteReport(ctx, report); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
		if err := w.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		var (
			windSampleInterval                   float64
			precipType                           sql.NullInt64
			lightningStrikeCount, reportInterval sql.NullFloat64
			lightningDistance, battery           sql.NullFloat64
		)
		row := w.db.QueryRowContext(ctx, `SELECT
			wind_sample_interval, precip_type, lightning_distance, lightning_strike_count,
			battery, report_interval
			FROM stormglass_observations WHERE serial_number = ?`, "ST-SHORT")
		if err := row.Scan(
			&windSampleInterval, &precipType, &lightningDistance, &lightningStrikeCount,
			&battery, &reportInterval,
		); err != nil {
			t.Fatalf("scan observation row: %v", err)
		}

		// wind_sample_interval (index 5) IS present at the minimum valid
		// length of 13 -> must NOT be NULL.
		if windSampleInterval != 1 {
			t.Errorf("wind_sample_interval = %v, want 1 (present at len=13)", windSampleInterval)
		}
		if precipType.Valid {
			t.Errorf("precip_type should be NULL, got %v", precipType.Int64)
		}
		if lightningDistance.Valid {
			t.Errorf("lightning_distance should be NULL, got %v", lightningDistance.Float64)
		}
		if lightningStrikeCount.Valid {
			t.Errorf("lightning_strike_count should be NULL, got %v", lightningStrikeCount.Float64)
		}
		if battery.Valid {
			t.Errorf("battery should be NULL, got %v", battery.Float64)
		}
		if reportInterval.Valid {
			t.Errorf("report_interval should be NULL, got %v", reportInterval.Float64)
		}
	})
}

// TestWriter_PreservesFractionalMeasurements is the UDP-path counterpart to
// internal/sqlite's TestInsertObservationsPreservesFractionalMeasurements.
// obs_st indices: 0 epoch, 5 wind sample interval, 13 precip type, 15
// lightning strike count, 17 report interval. The three measurements must
// survive fractionally; precip_type must still truncate.
func TestWriter_PreservesFractionalMeasurements(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-FRAC",
		Obs: [][]float64{
			//  0           1    2    3    4    5     6        7     8     9      10  11   12   13   14   15   16   17
			{1700000100, 1.5, 2.0, 2.5, 180, 1.5, 1013.25, 20.5, 55.0, 50000, 3, 500, 0.5, 1.9, 2.1, 2.5, 3.6, 5.9},
		},
	}

	if err := w.WriteReport(ctx, report); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var (
		windSampleInterval, lightningStrikeCount, reportInterval float64
		precipType                                               int64
	)
	err := w.db.QueryRowContext(ctx, `
		SELECT wind_sample_interval, precip_type, lightning_strike_count, report_interval
		FROM stormglass_observations WHERE serial_number = ?`, "ST-FRAC",
	).Scan(&windSampleInterval, &precipType, &lightningStrikeCount, &reportInterval)
	if err != nil {
		t.Fatalf("scan observation row: %v", err)
	}

	if windSampleInterval != 1.5 {
		t.Errorf("wind_sample_interval = %v, want 1.5", windSampleInterval)
	}
	if lightningStrikeCount != 2.5 {
		t.Errorf("lightning_strike_count = %v, want 2.5", lightningStrikeCount)
	}
	if reportInterval != 5.9 {
		t.Errorf("report_interval = %v, want 5.9", reportInterval)
	}
	if precipType != 1 {
		t.Errorf("precip_type = %d, want 1 (categorical enum: truncated by design)", precipType)
	}
}

// TestWriter_OnConflictIdempotent proves writing the same (serial_number,
// timestamp) observation twice yields exactly one row (ON CONFLICT DO
// NOTHING on the UNIQUE(serial_number, timestamp) constraint).
func TestWriter_OnConflictIdempotent(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-DUP",
		Obs: [][]float64{
			{1700000002, 1, 1, 1, 1, 1, 1013, 20, 50, 100, 1, 10, 0},
		},
	}

	if err := w.WriteReport(ctx, report); err != nil {
		t.Fatalf("first WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("first Flush: %v", err)
	}
	if err := w.WriteReport(ctx, report); err != nil {
		t.Fatalf("second WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("second Flush: %v", err)
	}

	var count int
	if err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stormglass_observations WHERE serial_number = ?`, "ST-DUP",
	).Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after duplicate write, got %d", count)
	}
}

// TestWriter_RoutesReportTypes proves each of the five report types lands in
// its corresponding table.
func TestWriter_RoutesReportTypes(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	obs := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-ROUTE",
		Obs:          [][]float64{{1700000010, 1, 1, 1, 1, 1, 1013, 20, 50, 100, 1, 10, 0}},
	}
	rapidWind := &tempestudp.RapidWindReport{
		SerialNumber: "ST-ROUTE",
		Ob:           []float64{1700000011, 5.5, 90},
	}
	hub := &tempestudp.HubStatusReport{
		SerialNumber: "ST-ROUTE",
		Timestamp:    1700000012,
		Uptime:       12345,
		Rssi:         -60,
		RadioStats:   []float64{17, 2, 0},
	}
	rainStart := &tempestudp.RainStartReport{
		SerialNumber: "ST-ROUTE",
		Evt:          []float64{1700000013},
	}
	lightning := &tempestudp.LightningStrikeReport{
		SerialNumber: "ST-ROUTE",
		Evt:          []float64{1700000014, 3.2, 100},
	}
	device := &tempestudp.DeviceStatusReport{
		SerialNumber: "ST-ROUTE",
		Timestamp:    1700000015,
		Rssi:         dsIntPtr(-70),
	}

	for _, report := range []tempestudp.Report{obs, rapidWind, hub, rainStart, lightning, device} {
		if err := w.WriteReport(ctx, report); err != nil {
			t.Fatalf("WriteReport(%T): %v", report, err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	assertCount := func(table, where string, want int) {
		t.Helper()
		var got int
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE serial_number = 'ST-ROUTE'`
		if where != "" {
			q += " AND " + where
		}
		if err := w.db.QueryRowContext(ctx, q).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s count = %d, want %d", table, got, want)
		}
	}

	assertCount("stormglass_observations", "", 1)
	assertCount("stormglass_rapid_wind", "", 1)
	assertCount("stormglass_hub_status", "", 1)
	assertCount("stormglass_device_status", "", 1)
	assertCount("stormglass_events", "event_type = 'rain_start'", 1)
	assertCount("stormglass_events", "event_type = 'lightning_strike'", 1)
}

// TestWriter_EventsNotDroppedUnderBackpressure proves a discrete event
// (lightning/rain-start) is never silently dropped just because the row
// channel is momentarily saturated: enqueueEvent blocks up to
// eventBlockTimeout and succeeds once space frees up, whereas a continuous
// row (enqueue) is dropped immediately once the channel is full (C-MEDIUM).
// Also pins the exact constants the design requires.
func TestWriter_EventsNotDroppedUnderBackpressure(t *testing.T) {
	if rowChanCap != 1000 {
		t.Fatalf("rowChanCap = %d, want 1000", rowChanCap)
	}
	if eventBlockTimeout != 5*time.Second {
		t.Fatalf("eventBlockTimeout = %v, want 5s", eventBlockTimeout)
	}

	// Construct the Writer directly (bypassing NewWriter) so no background
	// goroutine drains w.rows concurrently -- that would make "fill the
	// channel to capacity" a race instead of a deterministic setup.
	w := &Writer{
		rows: make(chan rowEnvelope, rowChanCap),
		done: make(chan struct{}),
	}

	for range rowChanCap {
		w.rows <- rowEnvelope{kind: kindObservation, payload: observationRow{}}
	}

	// A continuous row must be dropped (non-blocking) once the channel is
	// full, not block.
	enqueueDone := make(chan error, 1)
	go func() {
		enqueueDone <- w.enqueue(t.Context(), rowEnvelope{kind: kindObservation, payload: observationRow{}})
	}()
	select {
	case err := <-enqueueDone:
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked instead of dropping when channel full")
	}
	if len(w.rows) != rowChanCap {
		t.Fatalf("channel length changed after dropped continuous row: got %d, want %d", len(w.rows), rowChanCap)
	}

	// A discrete event must block rather than drop, and succeed once a slot
	// frees up (simulating the writer goroutine draining one row).
	freed := make(chan struct{})
	go func() {
		<-w.rows // free one slot, simulating the writer goroutine draining
		close(freed)
	}()

	eventDone := make(chan error, 1)
	go func() {
		eventDone <- w.enqueueEvent(t.Context(), rowEnvelope{kind: kindEvent, payload: eventRow{eventType: "lightning_strike"}})
	}()

	select {
	case <-freed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the simulated drain")
	}

	select {
	case err := <-eventDone:
		if err != nil {
			t.Fatalf("enqueueEvent: %v", err)
		}
	case <-time.After(eventBlockTimeout):
		t.Fatal("enqueueEvent did not unblock once the channel had room")
	}
}

// obsReport builds a minimal valid TempestObservationReport (len(ob) == 13,
// the smallest length handleObservationReport accepts) with the given
// serial, timestamp, and temp_air value -- the shared shape every Task 3.4
// test below needs, just varied by the three fields each test actually
// cares about.
func obsReport(serial string, ts int64, tempAir float64) *tempestudp.TempestObservationReport {
	return &tempestudp.TempestObservationReport{
		SerialNumber: serial,
		Obs: [][]float64{
			{float64(ts), 1, 1, 1, 1, 1, 1013, tempAir, 50, 100, 1, 10, 0},
		},
	}
}

// TestWriter_BatchFullFlushSurvivesRunCtxCancel proves a batch-full flush is
// not bound to the run ctx, which shutdown cancels before Close is called
// (#154). flushBatches truncates each slice whether or not the insert
// succeeded — the insert helpers return nothing and only log — so a flush
// that fails on a dead ctx destroys those rows before Close's drain can see
// them. This is the SQLite counterpart to
// TestPostgresWriter_BatchFullFlushSurvivesWriterCtxCancel (#111).
//
// Reverting run's batch-full flush to the run ctx makes this fail.
func TestWriter_BatchFullFlushSurvivesRunCtxCancel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "batchfull.db")
	db, err := Open(t.Context(), dbPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	}()

	// SIGTERM has already canceled the context the writer runs under; Close
	// has not been called yet. WriteReport below still carries a live ctx,
	// mirroring a producer that has not noticed shutdown.
	runCtx, cancelRunCtx := context.WithCancel(context.Background())

	const n = 5
	// FlushInterval never fires, so the only flush before Close is the
	// batch-full one the n-th row triggers.
	w := NewWriter(runCtx, db, Config{BatchSize: n, FlushInterval: time.Hour})

	cancelRunCtx()

	for i := range n {
		if err := w.WriteReport(t.Context(), obsReport("ST-BATCHFULL", 1700000200+int64(i), 20)); err != nil {
			t.Fatalf("WriteReport %d: %v", i, err)
		}
	}

	// Flush is a control message on the same channel the rows travel, so FIFO
	// ordering guarantees all n rows — and therefore the batch-full flush they
	// triggered — have already been handled when this returns. By then the
	// local batches are empty, so this Flush itself writes nothing; it is a
	// barrier, not the thing under test.
	if err := w.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var count int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM stormglass_observations WHERE serial_number = ?`,
		"ST-BATCHFULL").Scan(&count); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if count != n {
		t.Fatalf("expected %d rows persisted by the batch-full flush, got %d", n, count)
	}

	if err := w.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWriter_DrainOnClose proves Close(ctx) drains every row still buffered
// in w.rows and flushes it to disk before returning -- not just whatever was
// already appended to a local batch slice. batchSize/flushInterval are set
// so nothing flushes except the drain-on-Close path itself: batchSize is
// larger than the row count and flushInterval's ticker never fires during
// the test.
func TestWriter_DrainOnClose(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "drain.db")
	db, err := Open(t.Context(), dbPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	w := NewWriter(t.Context(), db, Config{BatchSize: 1000, FlushInterval: time.Hour})

	const n = 25
	for i := range n {
		if err := w.WriteReport(t.Context(), obsReport("ST-DRAIN", 1700000100+int64(i), 20)); err != nil {
			t.Fatalf("WriteReport %d: %v", i, err)
		}
	}

	// Close with a fresh ctx (not one derived from the writer's construction
	// ctx) -- proves the drain uses the live ctx passed to Close, per
	// writer.go's shutdownCtx comment.
	if err := w.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Reopen the DB at the same path with a brand-new *sql.DB/connection --
	// this is what proves the rows genuinely reached disk during Close's
	// drain, rather than just an in-process buffer.
	reopened, err := Open(t.Context(), dbPath, Config{BusyTimeout: 5000 * time.Millisecond})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened db: %v", err)
		}
	}()

	var count int
	if err := reopened.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM stormglass_observations WHERE serial_number = ?`, "ST-DRAIN",
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Fatalf("persisted rows after Close = %d, want %d", count, n)
	}
}

// TestReader_LatestObservation proves LatestObservation returns the newest
// row by timestamp (not insertion order), with the raw stored columns
// unmodified -- and a clear sentinel error when the serial has no rows.
func TestReader_LatestObservation(t *testing.T) {
	t.Run("returns_newest", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		// Insertion order deliberately does not match timestamp order.
		for _, ts := range []int64{1700000300, 1700000100, 1700000200} {
			if err := w.WriteReport(ctx, obsReport("ST-LATEST", ts, 20)); err != nil {
				t.Fatalf("WriteReport: %v", err)
			}
		}
		if err := w.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		got, err := w.LatestObservation(ctx, "ST-LATEST")
		if err != nil {
			t.Fatalf("LatestObservation: %v", err)
		}
		if got.Timestamp != 1700000300 {
			t.Errorf("Timestamp = %d, want 1700000300 (the newest)", got.Timestamp)
		}
		if got.SerialNumber != "ST-LATEST" {
			t.Errorf("SerialNumber = %q, want ST-LATEST", got.SerialNumber)
		}
		if got.TempAir != 20 {
			t.Errorf("TempAir = %v, want 20", got.TempAir)
		}
	})

	t.Run("no_rows_returns_sentinel", func(t *testing.T) {
		w := newTestWriter(t)
		_, err := w.LatestObservation(t.Context(), "ST-MISSING")
		if !errors.Is(err, ErrObservationNotFound) {
			t.Fatalf("err = %v, want ErrObservationNotFound", err)
		}
	})
}

// TestReader_LatestObservationAny proves LatestObservationAny returns the
// newest row across ALL serials (no WHERE serial_number clause) -- the
// single-station appliance's /api/observations/current has no serial to
// scope by, so it needs the newest row overall, not per-serial -- and the
// same sentinel error as LatestObservation when the table is empty.
func TestReader_LatestObservationAny(t *testing.T) {
	t.Run("returns_newest_across_serials", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		// Two different serials; the newest row overall belongs to the
		// second serial, not the first -- proves no serial-scoping happens.
		if err := w.WriteReport(ctx, obsReport("ST-A", 1700000100, 10)); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
		if err := w.WriteReport(ctx, obsReport("ST-B", 1700000200, 20)); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
		if err := w.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		got, err := w.LatestObservationAny(ctx)
		if err != nil {
			t.Fatalf("LatestObservationAny: %v", err)
		}
		if got.SerialNumber != "ST-B" {
			t.Errorf("SerialNumber = %q, want ST-B (the newest row overall)", got.SerialNumber)
		}
		if got.Timestamp != 1700000200 {
			t.Errorf("Timestamp = %d, want 1700000200", got.Timestamp)
		}
	})

	t.Run("no_rows_returns_sentinel", func(t *testing.T) {
		w := newTestWriter(t)
		_, err := w.LatestObservationAny(t.Context())
		if !errors.Is(err, ErrObservationNotFound) {
			t.Fatalf("err = %v, want ErrObservationNotFound", err)
		}
	})
}

// TestReader_HistoryPoints proves HistoryPoints returns in-range points
// ordered by timestamp for an allowlisted field, and rejects an unknown or
// SQL-injection-shaped field before any query executes.
func TestReader_HistoryPoints(t *testing.T) {
	t.Run("returns_in_range_points_ordered", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		// 100 and 400 are outside the [200, 300] query range below.
		for _, sample := range []struct {
			ts      int64
			tempAir float64
		}{
			{1700000100, 15},
			{1700000300, 25},
			{1700000200, 20}, // inserted out of order
			{1700000400, 30},
		} {
			if err := w.WriteReport(ctx, obsReport("ST-HIST", sample.ts, sample.tempAir)); err != nil {
				t.Fatalf("WriteReport: %v", err)
			}
		}
		if err := w.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		got, err := w.HistoryPoints(ctx, "temp_air", 1700000200, 1700000300)
		if err != nil {
			t.Fatalf("HistoryPoints: %v", err)
		}
		want := []Point{{T: 1700000200, V: 20}, {T: 1700000300, V: 25}}
		if len(got) != len(want) {
			t.Fatalf("HistoryPoints returned %d points, want %d: %+v", len(got), len(want), got)
		}
		for i, p := range want {
			if got[i] != p {
				t.Errorf("point %d = %+v, want %+v", i, got[i], p)
			}
		}
	})

	t.Run("no_matches_returns_empty_not_nil_slice", func(t *testing.T) {
		w := newTestWriter(t)

		got, err := w.HistoryPoints(t.Context(), "temp_air", 0, 1)
		if err != nil {
			t.Fatalf("HistoryPoints: %v", err)
		}
		if got == nil {
			t.Fatal("HistoryPoints returned a nil slice for zero matches, want a non-nil empty slice (marshals to [] not null)")
		}
		if len(got) != 0 {
			t.Fatalf("HistoryPoints returned %d points, want 0", len(got))
		}
	})

	t.Run("unknown_field_rejected_before_query", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		if _, err := w.HistoryPoints(ctx, "temp_air; DROP TABLE stormglass_observations", 0, 1<<62); err == nil {
			t.Fatal("HistoryPoints with a malicious field name returned nil error, want rejection")
		}

		// The table must still exist and be queryable -- proves no query
		// ever ran with the unvalidated field string.
		var count int
		if err := w.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stormglass_observations`).Scan(&count); err != nil {
			t.Fatalf("stormglass_observations unusable after rejected field: %v", err)
		}
	})

	// caps_at_max_history_points proves an unbounded [from, to] range can't
	// dump the whole table through the single writer connection (SGE review
	// I1): rows beyond maxHistoryPoints are truncated rather than all
	// returned. Rows are inserted directly via w.db (bypassing the async
	// Writer channel/batching) so inserting maxHistoryPoints+1 rows is fast
	// and can't be silently dropped by the channel's non-blocking enqueue.
	t.Run("caps_at_max_history_points", func(t *testing.T) {
		w := newTestWriter(t)
		ctx := t.Context()

		const total = maxHistoryPoints + 5

		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO stormglass_observations (id, serial_number, timestamp, temp_air) VALUES (?, ?, ?, ?)`)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		for i := range total {
			if _, err := stmt.ExecContext(ctx, uuid.Must(uuid.NewV7()).String(), "ST-CAP", int64(1700000000+i), 20.0); err != nil {
				t.Fatalf("insert row %d: %v", i, err)
			}
		}
		if err := stmt.Close(); err != nil {
			t.Fatalf("close stmt: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}

		got, err := w.HistoryPoints(ctx, "temp_air", 0, 1<<62)
		if err != nil {
			t.Fatalf("HistoryPoints: %v", err)
		}
		if len(got) != maxHistoryPoints {
			t.Fatalf("HistoryPoints returned %d points, want capped at %d (inserted %d)", len(got), maxHistoryPoints, total)
		}
	})
}

func dsIntPtr(v int) *int { return &v }

// TestWriter_InsertsDeviceStatus is #196's core writer case: the report the
// writer used to drop now becomes a row, with the served fields exact.
func TestWriter_InsertsDeviceStatus(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	report := &tempestudp.DeviceStatusReport{
		SerialNumber:     "ST-DEV",
		Timestamp:        1700000100,
		Uptime:           63807156,
		Voltage:          2.792,
		FirmwareRevision: dsIntPtr(156),
		Rssi:             dsIntPtr(-82),
		HubRssi:          -78,
		SensorStatus:     0,
	}
	if err := w.WriteReport(ctx, report); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var (
		serial   string
		ts       int64
		uptime   sql.NullInt64
		voltage  sql.NullFloat64
		firmware sql.NullString
		rssi     sql.NullInt64
		hubRssi  sql.NullInt64
	)
	err := w.db.QueryRowContext(ctx,
		`SELECT serial_number, timestamp, uptime, voltage, firmware_revision, rssi, hub_rssi
		   FROM stormglass_device_status WHERE serial_number = 'ST-DEV'`,
	).Scan(&serial, &ts, &uptime, &voltage, &firmware, &rssi, &hubRssi)
	if err != nil {
		t.Fatalf("query device_status: %v", err)
	}

	if ts != 1700000100 {
		t.Errorf("timestamp = %d, want 1700000100", ts)
	}
	// TEXT, not a number: vendor firmware forms are mixed across report types.
	if !firmware.Valid || firmware.String != "156" {
		t.Errorf("firmware_revision = %v, want \"156\"", firmware)
	}
	if !rssi.Valid || rssi.Int64 != -82 {
		t.Errorf("rssi = %v, want -82", rssi)
	}
	if !hubRssi.Valid || hubRssi.Int64 != -78 {
		t.Errorf("hub_rssi = %v, want -78", hubRssi)
	}
	if !voltage.Valid || voltage.Float64 != 2.792 {
		t.Errorf("voltage = %v, want 2.792", voltage)
	}
	if !uptime.Valid || uptime.Int64 != 63807156 {
		t.Errorf("uptime = %v, want 63807156", uptime)
	}
}

// TestWriter_DeviceStatusAbsentFieldsAreNull is the whole reason the parser
// fields are pointers. An absent rssi/firmware must reach the store as SQL
// NULL, never as 0/"0" -- otherwise the card renders "0 dBm" and firmware "0",
// which is absent data presented as a reading.
func TestWriter_DeviceStatusAbsentFieldsAreNull(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	if err := w.WriteReport(ctx, &tempestudp.DeviceStatusReport{
		SerialNumber: "ST-ABSENT",
		Timestamp:    1700000200,
		Voltage:      2.7,
		// FirmwareRevision and Rssi deliberately nil.
	}); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var firmware sql.NullString
	var rssi sql.NullInt64
	err := w.db.QueryRowContext(ctx,
		`SELECT firmware_revision, rssi FROM stormglass_device_status WHERE serial_number = 'ST-ABSENT'`,
	).Scan(&firmware, &rssi)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if firmware.Valid {
		t.Errorf("firmware_revision = %q, want SQL NULL", firmware.String)
	}
	if rssi.Valid {
		t.Errorf("rssi = %d, want SQL NULL", rssi.Int64)
	}
}

// TestWriter_DeviceStatusZeroIsStored is the other half: 0 dBm is a real
// reading and must NOT be confused with absent.
func TestWriter_DeviceStatusZeroIsStored(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	if err := w.WriteReport(ctx, &tempestudp.DeviceStatusReport{
		SerialNumber:     "ST-ZERO",
		Timestamp:        1700000300,
		FirmwareRevision: dsIntPtr(0),
		Rssi:             dsIntPtr(0),
	}); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var firmware sql.NullString
	var rssi sql.NullInt64
	if err := w.db.QueryRowContext(ctx,
		`SELECT firmware_revision, rssi FROM stormglass_device_status WHERE serial_number = 'ST-ZERO'`,
	).Scan(&firmware, &rssi); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !rssi.Valid || rssi.Int64 != 0 {
		t.Errorf("rssi = %v, want a non-NULL 0", rssi)
	}
	if !firmware.Valid || firmware.String != "0" {
		t.Errorf("firmware_revision = %v, want a non-NULL \"0\"", firmware)
	}
}

// TestWriter_DeviceStatusDropsMalformed: every sibling handler guards its
// report (rapid wind, hub status, rain, lightning). A device_status with no
// timestamp cannot be keyed and is dropped rather than stored at epoch 0.
func TestWriter_DeviceStatusDropsMalformed(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	if err := w.WriteReport(ctx, &tempestudp.DeviceStatusReport{
		SerialNumber: "ST-BAD",
		Timestamp:    0,
		Rssi:         dsIntPtr(-70),
	}); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var n int
	if err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stormglass_device_status WHERE serial_number = 'ST-BAD'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("stored %d malformed rows, want 0", n)
	}
}

// TestWriter_DeviceStatusIdempotent: re-inserting the same
// (serial_number, timestamp) must not duplicate, matching every other table.
func TestWriter_DeviceStatusIdempotent(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	report := &tempestudp.DeviceStatusReport{
		SerialNumber: "ST-DUP", Timestamp: 1700000400, Rssi: dsIntPtr(-70),
	}
	for range 2 {
		if err := w.WriteReport(ctx, report); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var n int
	if err := w.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stormglass_device_status WHERE serial_number = 'ST-DUP'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("row count = %d, want 1", n)
	}
}

// TestWriter_DeviceStatusDrainOnClose guards the repo's worst-history area:
// #111, #154, C-H1 and D-H1 are all shutdown-drain data-loss fixes. A
// device_status queued immediately before Close must still be persisted.
func TestWriter_DeviceStatusDrainOnClose(t *testing.T) {
	db := newTestDB(t)
	w := NewWriter(t.Context(), db, Config{BatchSize: 100, FlushInterval: time.Hour})

	if err := w.WriteReport(t.Context(), &tempestudp.DeviceStatusReport{
		SerialNumber: "ST-DRAIN", Timestamp: 1700000500, Rssi: dsIntPtr(-55),
	}); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM stormglass_device_status WHERE serial_number = 'ST-DRAIN'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("row count after Close = %d, want 1 -- the drain dropped it", n)
	}
}
