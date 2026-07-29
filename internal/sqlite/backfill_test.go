package sqlite

import (
	"database/sql"
	"testing"
	"time"

	"tempestwx-utilities/internal/weather"

	"github.com/google/uuid"
)

// seedObs inserts bare observation rows (serial + timestamp only; every other
// column is nullable) so gap tests can express a series compactly.
func seedObs(t *testing.T, db *sql.DB, serial string, epochs ...int64) {
	t.Helper()
	for _, e := range epochs {
		_, err := db.ExecContext(t.Context(),
			`INSERT INTO tempest_observations (id, serial_number, timestamp) VALUES (?, ?, ?)`,
			uuid.Must(uuid.NewV7()).String(), serial, e)
		if err != nil {
			t.Fatalf("seed %s@%d: %v", serial, e, err)
		}
	}
}

func ts(epoch int64) time.Time { return time.Unix(epoch, 0).UTC() }

// The regression test for the partitioning bug: two serials whose interleaved
// timestamps mask each other. ST-A has a one-hour hole from 1000 to 4600.
// ST-B reports throughout, offset by 30s. Merged and unpartitioned, no
// consecutive interval exceeds minGap and the hole is invisible.
func TestFindObservationGapsPartitionsBySerial(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 4600, 5200)
	for e := int64(1030); e <= 5230; e += 600 {
		seedObs(t, db, "ST-B", e)
	}

	gaps, err := FindObservationGaps(t.Context(), db, ts(0), ts(10000), 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d gaps, want 1: %+v", len(gaps), gaps)
	}
	if gaps[0].SerialNumber != "ST-A" {
		t.Errorf("SerialNumber = %q, want ST-A", gaps[0].SerialNumber)
	}
	if !gaps[0].From.Equal(ts(1000)) || !gaps[0].To.Equal(ts(4600)) {
		t.Errorf("gap = [%v, %v], want [%v, %v]", gaps[0].From, gaps[0].To, ts(1000), ts(4600))
	}
}

func TestFindObservationGapsIgnoresJitterBelowMinGap(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 1060, 1125, 1180) // ~1min apart
	gaps, err := FindObservationGaps(t.Context(), db, ts(0), ts(10000), 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("got %d gaps, want 0: %+v", len(gaps), gaps)
	}
}

func TestSeriesBoundsPerSerial(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 2000, 3000)
	seedObs(t, db, "ST-B", 5000, 6000)

	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	got := map[string]weather.Bounds{}
	for _, b := range bounds {
		got[b.SerialNumber] = b
	}
	if len(got) != 2 {
		t.Fatalf("got %d serials, want 2: %+v", len(got), bounds)
	}
	if !got["ST-A"].First.Equal(ts(1000)) || !got["ST-A"].Last.Equal(ts(3000)) {
		t.Errorf("ST-A bounds = [%v, %v], want [1000, 3000]", got["ST-A"].First, got["ST-A"].Last)
	}
	if !got["ST-B"].First.Equal(ts(5000)) || !got["ST-B"].Last.Equal(ts(6000)) {
		t.Errorf("ST-B bounds = [%v, %v], want [5000, 6000]", got["ST-B"].First, got["ST-B"].Last)
	}
}

// DistinctSerials must ignore the window entirely. This is the regression
// guard for the false-mismatch bug: ST-B's only rows sit outside any window a
// caller is likely to ask about, but it is still very much in the store.
func TestDistinctSerialsIsUnwindowed(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-A", 1000, 2000)
	seedObs(t, db, "ST-B", 900000) // far outside a [0, 10000] window

	serials, err := DistinctSerials(t.Context(), db)
	if err != nil {
		t.Fatalf("DistinctSerials: %v", err)
	}
	if len(serials) != 2 || serials[0] != "ST-A" || serials[1] != "ST-B" {
		t.Errorf("got %v, want [ST-A ST-B] — the query must not be windowed", serials)
	}

	// Contrast: the windowed query legitimately does not see ST-B.
	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	if len(bounds) != 1 {
		t.Errorf("SeriesBounds returned %d serials, want 1 — this is why the two queries cannot be merged", len(bounds))
	}
}

func TestDistinctSerialsEmptyTable(t *testing.T) {
	db := newTestDB(t)
	serials, err := DistinctSerials(t.Context(), db)
	if err != nil {
		t.Fatalf("DistinctSerials on empty table must not error: %v", err)
	}
	if len(serials) != 0 {
		t.Errorf("got %d serials, want 0", len(serials))
	}
}

func TestSeriesBoundsEmptyTable(t *testing.T) {
	db := newTestDB(t)
	bounds, err := SeriesBounds(t.Context(), db, ts(0), ts(10000))
	if err != nil {
		t.Fatalf("SeriesBounds on empty table must not error: %v", err)
	}
	if len(bounds) != 0 {
		t.Errorf("got %d bounds, want 0", len(bounds))
	}
}

func f(v float64) *float64 { return &v }

func TestInsertObservationsIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
		{SerialNumber: "ST-A", Timestamp: ts(1060), TempAir: f(20.6), Humidity: f(56), Pressure: f(1013)},
	}

	n, err := InsertObservations(t.Context(), db, obs)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if n != 2 {
		t.Errorf("first insert = %d, want 2", n)
	}

	n, err = InsertObservations(t.Context(), db, obs)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if n != 0 {
		t.Errorf("second insert = %d, want 0 (ON CONFLICT DO NOTHING)", n)
	}

	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tempest_observations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2", count)
	}
}

func TestInsertObservationsPreservesNull(t *testing.T) {
	db := newTestDB(t)
	// Pressure is nil — it must land as SQL NULL, not 0.0.
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var pressure sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT pressure FROM tempest_observations`).Scan(&pressure); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if pressure.Valid {
		t.Errorf("pressure = %v, want NULL", pressure.Float64)
	}
}

func TestInsertObservationsDerivesWetBulb(t *testing.T) {
	db := newTestDB(t)
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var wb sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT temp_wetbulb FROM tempest_observations`).Scan(&wb); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !wb.Valid {
		t.Fatal("temp_wetbulb is NULL; it must be derived at the store boundary")
	}
	if wb.Float64 <= 0 || wb.Float64 >= 20.5 {
		t.Errorf("temp_wetbulb = %v, want a plausible value below dry-bulb 20.5", wb.Float64)
	}
}

func TestInsertObservationsWetBulbNullWhenInputsMissing(t *testing.T) {
	db := newTestDB(t)
	// No humidity: wet bulb is not derivable and must stay NULL rather than
	// being computed from a zero value.
	obs := []weather.Observation{
		{SerialNumber: "ST-A", Timestamp: ts(1000), TempAir: f(20.5), Pressure: f(1013)},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var wb sql.NullFloat64
	if err := db.QueryRowContext(t.Context(), `SELECT temp_wetbulb FROM tempest_observations`).Scan(&wb); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if wb.Valid {
		t.Errorf("temp_wetbulb = %v, want NULL when humidity is absent", wb.Float64)
	}
}

func TestInsertObservationsEmptyIsNoop(t *testing.T) {
	db := newTestDB(t)
	n, err := InsertObservations(t.Context(), db, nil)
	if err != nil {
		t.Fatalf("empty insert: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// TestInsertObservationsRoundTripsAllColumns pins the 21-argument bind order
// in InsertObservations against insertObservationSQL (writer.go). Every
// measurement field carries a distinct, unambiguous value so a transposition
// of any two bind arguments (e.g. WindAvg <-> WindGust) changes an observable
// column value and fails this test by name, rather than leaving the whole
// suite green.
//
// wind_sample_interval, precip_type, lightning_strike_count, and
// report_interval are declared INTEGER in migrations/0001_init.sql, so this
// test keeps those four fields integral. That is a deliberate accommodation
// of the deferred *float64-into-INTEGER-column finding, not a claim that the
// finding is resolved — SQLite's column affinity converts an integral REAL
// bind to INTEGER storage losslessly, so using integral values here avoids
// dragging that out-of-scope issue into this test.
func TestInsertObservationsRoundTripsAllColumns(t *testing.T) {
	db := newTestDB(t)
	o := weather.Observation{
		SerialNumber:         "ST-A",
		Timestamp:            ts(1000),
		WindLull:             f(1.5),
		WindAvg:              f(3.5),
		WindGust:             f(6.5),
		WindDirection:        f(180),
		WindSampleInterval:   f(3), // INTEGER column: kept integral
		Pressure:             f(1013.25),
		TempAir:              f(22.5),
		Humidity:             f(65),
		Illuminance:          f(15000),
		UVIndex:              f(4.5),
		Irradiance:           f(600),
		RainRate:             f(0.5),
		PrecipType:           f(1), // INTEGER column: kept integral
		LightningDistance:    f(12.5),
		LightningStrikeCount: f(2), // INTEGER column: kept integral
		Battery:              f(2.85),
		ReportInterval:       f(5), // INTEGER column: kept integral
	}

	if _, err := InsertObservations(t.Context(), db, []weather.Observation{o}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		windLull, windAvg, windGust, windDirection, windSampleInterval sql.NullFloat64
		pressure, tempAir, tempWetbulb, humidity                       sql.NullFloat64
		illuminance, uvIndex, irradiance, rainRate, precipType         sql.NullFloat64
		lightningDistance, lightningStrikeCount, battery               sql.NullFloat64
		reportInterval                                                 sql.NullFloat64
	)
	row := db.QueryRowContext(t.Context(), `
		SELECT wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		       pressure, temp_air, temp_wetbulb, humidity,
		       illuminance, uv_index, irradiance, rain_rate, precip_type,
		       lightning_distance, lightning_strike_count,
		       battery, report_interval
		FROM tempest_observations WHERE serial_number = ? AND timestamp = ?`,
		o.SerialNumber, o.Timestamp.Unix())
	if err := row.Scan(
		&windLull, &windAvg, &windGust, &windDirection, &windSampleInterval,
		&pressure, &tempAir, &tempWetbulb, &humidity,
		&illuminance, &uvIndex, &irradiance, &rainRate, &precipType,
		&lightningDistance, &lightningStrikeCount,
		&battery, &reportInterval,
	); err != nil {
		t.Fatalf("scan: %v", err)
	}

	cases := []struct {
		column string
		got    sql.NullFloat64
		want   float64
	}{
		{"wind_lull", windLull, 1.5},
		{"wind_avg", windAvg, 3.5},
		{"wind_gust", windGust, 6.5},
		{"wind_direction", windDirection, 180},
		{"wind_sample_interval", windSampleInterval, 3},
		{"pressure", pressure, 1013.25},
		{"temp_air", tempAir, 22.5},
		{"humidity", humidity, 65},
		{"illuminance", illuminance, 15000},
		{"uv_index", uvIndex, 4.5},
		{"irradiance", irradiance, 600},
		{"rain_rate", rainRate, 0.5},
		{"precip_type", precipType, 1},
		{"lightning_distance", lightningDistance, 12.5},
		{"lightning_strike_count", lightningStrikeCount, 2},
		{"battery", battery, 2.85},
		{"report_interval", reportInterval, 5},
	}
	for _, c := range cases {
		if !c.got.Valid {
			t.Errorf("%s = NULL, want %v", c.column, c.want)
			continue
		}
		if c.got.Float64 != c.want {
			t.Errorf("%s = %v, want %v", c.column, c.got.Float64, c.want)
		}
	}

	// temp_wetbulb is derived, not bound from the struct, so assert
	// plausibility rather than equality with an input.
	if !tempWetbulb.Valid {
		t.Fatal("temp_wetbulb is NULL; it must be derived at the store boundary")
	}
	if tempWetbulb.Float64 <= 0 || tempWetbulb.Float64 > 22.5 {
		t.Errorf("temp_wetbulb = %v, want a plausible value in (0, 22.5] for dry-bulb 22.5", tempWetbulb.Float64)
	}
}

// TestFindObservationGapsRespectsWindow pins the WHERE timestamp BETWEEN ? AND ?
// predicate in findObservationGapsSQL. ST-C's rows sit well outside the
// queried window with a hole wide enough to qualify as a gap if the window
// predicate did not exclude them; ST-D has a genuine in-window hole. If the
// window predicate were removed, ST-C's rows would participate in the query
// (and, if the argument list still matched, would surface an extra gap
// outside the requested range) rather than being excluded entirely.
func TestFindObservationGapsRespectsWindow(t *testing.T) {
	db := newTestDB(t)
	seedObs(t, db, "ST-D", 1000, 5000)   // in-window hole: 4000s > 30min
	seedObs(t, db, "ST-C", 50000, 90000) // out-of-window hole: 40000s > 30min

	gaps, err := FindObservationGaps(t.Context(), db, ts(0), ts(10000), 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d gaps, want 1 (ST-C's out-of-window hole must not appear): %+v", len(gaps), gaps)
	}
	if gaps[0].SerialNumber != "ST-D" {
		t.Errorf("SerialNumber = %q, want ST-D", gaps[0].SerialNumber)
	}
	if !gaps[0].From.Equal(ts(1000)) || !gaps[0].To.Equal(ts(5000)) {
		t.Errorf("gap = [%v, %v], want [%v, %v]", gaps[0].From, gaps[0].To, ts(1000), ts(5000))
	}
}
