package postgres

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"tempestwx-utilities/internal/weather"
)

func f(v float64) *float64 { return &v }

func TestBackfillWetBulbDerivation(t *testing.T) {
	tests := []struct {
		name    string
		obs     weather.Observation
		wantNil bool
	}{
		{
			name: "all inputs present",
			obs:  weather.Observation{TempAir: f(20.5), Humidity: f(55), Pressure: f(1013)},
		},
		{
			name:    "humidity missing",
			obs:     weather.Observation{TempAir: f(20.5), Pressure: f(1013)},
			wantNil: true,
		},
		{
			name:    "temp missing",
			obs:     weather.Observation{Humidity: f(55), Pressure: f(1013)},
			wantNil: true,
		},
		{
			name:    "pressure missing",
			obs:     weather.Observation{TempAir: f(20.5), Humidity: f(55)},
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wetBulb(tt.obs)
			if tt.wantNil && got != nil {
				t.Errorf("wetBulb = %v, want nil", *got)
			}
			if !tt.wantNil {
				if got == nil {
					t.Fatal("wetBulb = nil, want a value")
				}
				if *got <= 0 || *got >= 20.5 {
					t.Errorf("wetBulb = %v, want a plausible value below dry-bulb 20.5", *got)
				}
			}
		})
	}
}

// TestInsertObservationsEmptyInput pins the len(obs) == 0 early return in
// InsertObservations: it must report (0, nil) without ever touching pool, so
// this needs no live database and must not sit behind the POSTGRES_URL skip
// guard below.
func TestInsertObservationsEmptyInput(t *testing.T) {
	n, err := InsertObservations(t.Context(), nil, nil)
	if err != nil {
		t.Fatalf("InsertObservations(nil pool, nil obs) = _, %v, want nil error", err)
	}
	if n != 0 {
		t.Errorf("InsertObservations(nil pool, nil obs) = %d, want 0", n)
	}
}

// --- integration: requires a live Postgres ---
//
// Everything above is a pure unit test. The SQL below is the ONLY thing that
// exercises this file's actual queries, and the Postgres dialect differs from
// SQLite's in ways that can fail at runtime and nowhere else:
//
//   - EXTRACT(EPOCH FROM (ts - prev)) yields numeric in PG14+, compared here
//     against a float64 parameter.
//   - MIN/MAX over timestamptz scanned into time.Time.
//   - precip_type is INTEGER in the DDL (schema.go:55) while the bind is a
//     *float64 — pgx truncates silently rather than erroring.
//
// Follow the existing skip idiom in writer_integration_test.go. This must be
// RUN AT LEAST ONCE against a real database before the branch merges; a run
// where it skips is not evidence.
func TestBackfillPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL not set; skipping Postgres integration test")
	}

	ctx := t.Context()
	pool, err := OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := CreateSchema(ctx, pool); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	// Isolate this run from anything already in the table.
	serialA := "ST-ITEST-A"
	serialB := "ST-ITEST-B"
	serialC := "ST-ITEST-C" // isolated: exercises the full derive-and-store wetbulb path only
	serialD := "ST-ITEST-D" // isolated: exercises the full 18-column bind order, one distinct value per field
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM tempest_observations WHERE serial_number IN ($1, $2, $3, $4)`, serialA, serialB, serialC, serialD)
	})
	if _, err := pool.Exec(ctx,
		`DELETE FROM tempest_observations WHERE serial_number IN ($1, $2, $3, $4)`, serialA, serialB, serialC, serialD); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}

	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) // far from real data

	// The same interleaved fixture the SQLite suite uses: ST-A has a one-hour
	// hole; ST-B reports throughout, offset, and would mask it if the query
	// were not partitioned.
	var seed []weather.Observation
	for _, off := range []time.Duration{0, time.Hour, 90 * time.Minute} {
		seed = append(seed, weather.Observation{
			SerialNumber: serialA,
			Timestamp:    base.Add(off),
			TempAir:      f(20),
			// precip_type is the sole INTEGER column among these four
			// (schema.go:55) while the bind is a *float64 — the exact
			// truncation hazard named at the top of this file's comment
			// block; pgx truncates a float into int4 silently, so only a
			// real round-trip catches it. wind_sample_interval,
			// lightning_strike_count, and report_interval are DOUBLE
			// PRECISION (schema.go:43,58,61); seeding them here is a
			// float8 round-trip fidelity check, not an INTEGER hazard
			// check.
			PrecipType:           f(1),
			WindSampleInterval:   f(3),
			LightningStrikeCount: f(0),
			ReportInterval:       f(1),
		})
	}
	for off := 30 * time.Second; off <= 95*time.Minute; off += 10 * time.Minute {
		seed = append(seed, weather.Observation{SerialNumber: serialB, Timestamp: base.Add(off), TempAir: f(21)})
	}
	// serialC has all three wetBulb inputs present, closing the
	// derive-and-store path end to end: nothing else in this fixture proves
	// temp_wetbulb is ever non-NULL.
	seed = append(seed, weather.Observation{
		SerialNumber: serialC,
		Timestamp:    base,
		TempAir:      f(20.5),
		Humidity:     f(55),
		Pressure:     f(1013),
	})
	// serialD carries a DISTINCT, unambiguous value in every one of the 18
	// bound columns (17 direct fields + the derived temp_wetbulb), so a
	// transposition of any two adjacent binds in InsertObservations (e.g.
	// WindAvg <-> WindGust) changes an observable column value and fails the
	// round-trip assertion below by name, instead of leaving every other
	// assertion in this test green. This mirrors the SQLite twin's
	// TestInsertObservationsRoundTripsAllColumns, which the Postgres side
	// lacked until now (only 7 of 18 columns were ever read back).
	seed = append(seed, weather.Observation{
		SerialNumber:         serialD,
		Timestamp:            base,
		WindLull:             f(21.1),
		WindAvg:              f(22.2),
		WindGust:             f(23.3),
		WindDirection:        f(24.4),
		WindSampleInterval:   f(25),
		Pressure:             f(26.6),
		TempAir:              f(27.7),
		Humidity:             f(28.8),
		Illuminance:          f(29.9),
		UVIndex:              f(30.1),
		Irradiance:           f(31.1),
		RainRate:             f(32.2),
		PrecipType:           f(33), // the sole INTEGER column (schema.go:55)
		LightningDistance:    f(34.4),
		LightningStrikeCount: f(35),
		Battery:              f(36.6),
		ReportInterval:       f(37),
	})

	// Partial-conflict coverage (the real repair-run shape): insert a prefix
	// of the seed first, then the full seed, and confirm the second call
	// reports exactly the rows that were genuinely new. prefix spans all of
	// ST-A plus the first two ST-B rows, so the overlap crosses both series,
	// not just one.
	prefix := seed[:5]
	prefixInserted, err := InsertObservations(ctx, pool, prefix)
	if err != nil {
		t.Fatalf("InsertObservations(prefix): %v", err)
	}
	if prefixInserted != len(prefix) {
		t.Errorf("inserted %d prefix rows, want %d", prefixInserted, len(prefix))
	}

	n, err := InsertObservations(ctx, pool, seed)
	if err != nil {
		t.Fatalf("InsertObservations: %v", err)
	}
	wantNew := len(seed) - len(prefix)
	if n != wantNew {
		t.Errorf("inserted %d rows on partial-conflict batch, want %d (len(seed)=%d, prefix already present=%d)",
			n, wantNew, len(seed), len(prefix))
	}

	// Idempotency: the same batch again must insert nothing.
	again, err := InsertObservations(ctx, pool, seed)
	if err != nil {
		t.Fatalf("second InsertObservations: %v", err)
	}
	if again != 0 {
		t.Errorf("re-insert added %d rows, want 0", again)
	}

	from, to := base.Add(-time.Hour), base.Add(3*time.Hour)

	serials, err := DistinctSerials(ctx, pool)
	if err != nil {
		t.Fatalf("DistinctSerials: %v", err)
	}
	if !slices.Contains(serials, serialA) || !slices.Contains(serials, serialB) {
		t.Errorf("DistinctSerials = %v, want it to contain %s and %s", serials, serialA, serialB)
	}

	bounds, err := SeriesBounds(ctx, pool, from, to)
	if err != nil {
		t.Fatalf("SeriesBounds: %v", err)
	}
	var gotA bool
	for _, b := range bounds {
		if b.SerialNumber == serialA {
			gotA = true
			if !b.First.Equal(base) {
				t.Errorf("%s First = %v, want %v", serialA, b.First, base)
			}
		}
	}
	if !gotA {
		t.Errorf("SeriesBounds missing %s: %+v", serialA, bounds)
	}

	// Round-trip precip_type (the sole INTEGER column here — schema.go:55)
	// alongside the three DOUBLE PRECISION columns (wind_sample_interval,
	// lightning_strike_count, report_interval) for float8 fidelity, plus
	// temp_air/pressure/temp_wetbulb — selected by name, never positionally
	// — to pin the full 21-argument bind order in InsertObservations. A
	// transposition of two adjacent binds (e.g. Pressure <-> TempAir) would
	// otherwise pass every other assertion in this test while writing the
	// wrong value into the wrong column.
	var precip, interval, strikes, report *int64
	var tempAir, pressure, tempWetbulb *float64
	if err := pool.QueryRow(ctx,
		`SELECT precip_type, wind_sample_interval, lightning_strike_count, report_interval,
		        temp_air, pressure, temp_wetbulb
		 FROM tempest_observations WHERE serial_number = $1 AND timestamp = $2`,
		serialA, base).Scan(&precip, &interval, &strikes, &report, &tempAir, &pressure, &tempWetbulb); err != nil {
		t.Fatalf("read back observation columns: %v", err)
	}
	for _, c := range []struct {
		name string
		got  *int64
		want int64
	}{
		{"precip_type", precip, 1},
		{"wind_sample_interval", interval, 3},
		{"lightning_strike_count", strikes, 0},
		{"report_interval", report, 1},
	} {
		if c.got == nil {
			t.Errorf("%s is NULL, want %d", c.name, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, *c.got, c.want)
		}
	}
	if tempAir == nil {
		t.Errorf("%s temp_air is NULL, want the seeded value 20", serialA)
	} else if *tempAir != 20 {
		t.Errorf("%s temp_air = %v, want 20 (the seeded value)", serialA, *tempAir)
	}
	if pressure != nil {
		t.Errorf("%s pressure = %v, want NULL (the ST-A fixture sets no pressure)", serialA, *pressure)
	}
	if tempWetbulb != nil {
		t.Errorf("%s temp_wetbulb = %v, want NULL (pressure and humidity are both absent, so wetBulb must return nil)",
			serialA, *tempWetbulb)
	}

	// serialC has all three wetBulb inputs, so this closes the
	// derive-and-store path end to end: nothing else in this fixture proves
	// InsertObservations ever writes a non-NULL temp_wetbulb.
	var wetbulbC *float64
	if err := pool.QueryRow(ctx,
		`SELECT temp_wetbulb FROM tempest_observations WHERE serial_number = $1 AND timestamp = $2`,
		serialC, base).Scan(&wetbulbC); err != nil {
		t.Fatalf("read back %s temp_wetbulb: %v", serialC, err)
	}
	if wetbulbC == nil {
		t.Fatalf("%s temp_wetbulb is NULL, want a derived value (temp/humidity/pressure all present)", serialC)
	}
	if *wetbulbC <= 0 || *wetbulbC >= 20.5 {
		t.Errorf("%s temp_wetbulb = %v, want a plausible value below dry-bulb 20.5", serialC, *wetbulbC)
	}

	// Full 18-column round-trip: serialD carries a distinct value in every
	// bound field, selected here by name — never positionally — to pin the
	// complete bind order in InsertObservations. A transposition of ANY two
	// binds (not just the 7 previously checked) fails this by name.
	var (
		windLull, windAvg, windGust, windDirection *float64
		windSampleInterval                         *int64
		pressureD, tempAirD                        *float64
		tempWetbulbD                               *float64
		humidityD                                  *float64
		illuminance, uvIndex, irradiance, rainRate *float64
		precipTypeD                                *int64
		lightningDistance                          *float64
		lightningStrikeCount                       *int64
		batteryD                                   *float64
		reportIntervalD                            *int64
	)
	if err := pool.QueryRow(ctx,
		`SELECT wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		        pressure, temp_air, temp_wetbulb, humidity,
		        illuminance, uv_index, irradiance, rain_rate, precip_type,
		        lightning_distance, lightning_strike_count, battery, report_interval
		 FROM tempest_observations WHERE serial_number = $1 AND timestamp = $2`,
		serialD, base).Scan(
		&windLull, &windAvg, &windGust, &windDirection, &windSampleInterval,
		&pressureD, &tempAirD, &tempWetbulbD, &humidityD,
		&illuminance, &uvIndex, &irradiance, &rainRate, &precipTypeD,
		&lightningDistance, &lightningStrikeCount, &batteryD, &reportIntervalD,
	); err != nil {
		t.Fatalf("read back %s observation columns: %v", serialD, err)
	}

	floatCases := []struct {
		column string
		got    *float64
		want   float64
	}{
		{"wind_lull", windLull, 21.1},
		{"wind_avg", windAvg, 22.2},
		{"wind_gust", windGust, 23.3},
		{"wind_direction", windDirection, 24.4},
		{"pressure", pressureD, 26.6},
		{"temp_air", tempAirD, 27.7},
		{"humidity", humidityD, 28.8},
		{"illuminance", illuminance, 29.9},
		{"uv_index", uvIndex, 30.1},
		{"irradiance", irradiance, 31.1},
		{"rain_rate", rainRate, 32.2},
		{"lightning_distance", lightningDistance, 34.4},
		{"battery", batteryD, 36.6},
	}
	for _, c := range floatCases {
		if c.got == nil {
			t.Errorf("%s (%s) is NULL, want %v", c.column, serialD, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s (%s) = %v, want %v", c.column, serialD, *c.got, c.want)
		}
	}

	intCases := []struct {
		column string
		got    *int64
		want   int64
	}{
		{"wind_sample_interval", windSampleInterval, 25},
		{"precip_type", precipTypeD, 33},
		{"lightning_strike_count", lightningStrikeCount, 35},
		{"report_interval", reportIntervalD, 37},
	}
	for _, c := range intCases {
		if c.got == nil {
			t.Errorf("%s (%s) is NULL, want %d", c.column, serialD, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s (%s) = %d, want %d", c.column, serialD, *c.got, c.want)
		}
	}

	if tempWetbulbD == nil {
		t.Fatalf("%s temp_wetbulb is NULL, want a derived value (temp/humidity/pressure all present)", serialD)
	}
	if *tempWetbulbD <= 0 || *tempWetbulbD >= 27.7 {
		t.Errorf("%s temp_wetbulb = %v, want a plausible value below dry-bulb 27.7", serialD, *tempWetbulbD)
	}

	gaps, err := FindObservationGaps(ctx, pool, from, to, 30*time.Minute)
	if err != nil {
		t.Fatalf("FindObservationGaps: %v", err)
	}
	var found bool
	for _, g := range gaps {
		if g.SerialNumber == serialA && g.From.Equal(base) && g.To.Equal(base.Add(time.Hour)) {
			found = true
		}
		if g.SerialNumber == serialB {
			t.Errorf("unexpected gap for %s: %+v", serialB, g)
		}
	}
	if !found {
		t.Errorf("did not find the %s hole [%v, %v]; got %+v", serialA, base, base.Add(time.Hour), gaps)
	}
}
