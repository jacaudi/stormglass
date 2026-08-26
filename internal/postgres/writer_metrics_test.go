package postgres

import (
	"math"
	"testing"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"
)

// TestPostgresWriter_WriteMetrics_MapsEachFieldToItsColumn feeds WriteMetrics
// the exact []prometheus.Metric shape the API-export path produces
// (tempestudp.TempestObservationReport.Metrics()) with a distinct value per
// field, and asserts each value lands in its corresponding observationRow
// column. Values are distinct across fields specifically so a transposition
// in observationFieldMappers (writer.go) — one descriptor's value landing in
// a different field's column — shows up as a mismatch on both the field that
// lost its value and the field that wrongly received someone else's.
//
// No live database is needed: WriteMetrics's dispatch (grouping metrics by
// serial+timestamp, then applyObservationField) is fully separable from the
// eventual Postgres write — sendObservationBatch only enqueues onto
// w.obsBatch, a buffered channel with no consumer required here.
//
// stormglass_pressure_pa and stormglass_report_interval_s
// (observationFieldMappers, writer.go) do not match the real descriptor
// names stormglass_pressure_mb / stormglass_report_interval_minutes
// (internal/tempest/metrics.go) — a pre-existing defect (present in HEAD
// before the lint-debt refactor this test package covers) that silently
// drops both fields on every WriteMetrics call. pressure and reportInterval
// are deliberately excluded from this test's "must land correctly"
// assertions for that reason; see
// TestPostgresWriter_WriteMetrics_PressureAndReportIntervalNotDispatched_KnownBug
// below, which locks in and flags the current (broken) behavior instead of
// silently passing over it.
func TestPostgresWriter_WriteMetrics_MapsEachFieldToItsColumn(t *testing.T) {
	w := &PostgresWriter{
		obsBatch: make(chan observationRow, 10),
		done:     make(chan struct{}),
	}

	const serial = "ST-METRICS"
	const (
		windLull      = 1.1
		windAvg       = 2.2
		windGust      = 3.3
		windDirection = 144.4
		pressure      = 1013.25 // known-broken dispatch; used only for wetbulb convergence, not asserted below
		tempAir       = 20.5
		humidity      = 75.0
		illuminance   = 50000.0
		uvIndex       = 6.6
		irradiance    = 500.5
		rainRate      = 0.55
		lightningDist = 8.25
		lightningCnt  = 12.0
		battery       = 3.85
	)

	report := tempestudp.TempestObservationReport{
		SerialNumber: serial,
		Obs: [][]float64{
			{
				1700000000,    // 0: timestamp
				windLull,      // 1: wind lull
				windAvg,       // 2: wind avg
				windGust,      // 3: wind gust
				windDirection, // 4: wind direction
				5,             // 5: wind sample interval (no Prometheus descriptor; not exercised by WriteMetrics)
				pressure,      // 6: pressure (mb)
				tempAir,       // 7: temp air
				humidity,      // 8: humidity
				illuminance,   // 9: illuminance
				uvIndex,       // 10: uv
				irradiance,    // 11: irradiance
				rainRate,      // 12: rain rate
				0,             // 13: precip type (no Prometheus descriptor; not exercised by WriteMetrics)
				lightningDist, // 14: lightning distance
				lightningCnt,  // 15: lightning strike count
				battery,       // 16: battery
				15,            // 17: report interval (minutes) -- known-broken dispatch
			},
		},
	}

	if err := w.WriteMetrics(t.Context(), report.Metrics()); err != nil {
		t.Fatalf("WriteMetrics returned unexpected error: %v", err)
	}

	var row observationRow
	select {
	case row = <-w.obsBatch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observation row on w.obsBatch")
	}

	if row.serialNumber != serial {
		t.Errorf("serialNumber = %q, want %q", row.serialNumber, serial)
	}

	// wantWetbulb is derived the same way the production wetbulb metric is
	// (report.go's Metrics()), so this asserts dispatch — landing the
	// "wetbulb" kind of the shared Temperature descriptor in tempWetbulb,
	// not "air" — rather than re-deriving the wetbulb formula itself.
	wantWetbulb := tempestudp.WetBulbTemperatureC(tempAir, humidity, pressure)

	// deref returns NaN for a nil pointer so a wrongly-unset field produces a
	// visible mismatch against a real expected value instead of silently
	// comparing two zero-ish placeholders.
	deref := func(p *float64) float64 {
		if p == nil {
			return math.NaN()
		}
		return *p
	}

	cases := []struct {
		field string
		got   float64
		want  float64
	}{
		{"windLull", row.windLull, windLull},
		{"windAvg", row.windAvg, windAvg},
		{"windGust", row.windGust, windGust},
		{"windDirection", row.windDirection, windDirection},
		{"tempAir", row.tempAir, tempAir},
		{"tempWetbulb", deref(row.tempWetbulb), wantWetbulb},
		{"humidity", row.humidity, humidity},
		{"illuminance", row.illuminance, illuminance},
		{"uvIndex", row.uvIndex, uvIndex},
		{"irradiance", row.irradiance, irradiance},
		{"rainRate", row.rainRate, rainRate},
		{"lightningDistance", deref(row.lightningDistance), lightningDist},
		{"lightningStrikeCount", deref(row.lightningStrikeCount), lightningCnt},
		{"battery", deref(row.battery), battery},
	}

	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, tc.got, tc.want)
		}
	}
}

// TestPostgresWriter_WriteMetrics_PressureAndReportIntervalNotDispatched_KnownBug
// locks in a pre-existing defect discovered while writing the test above:
// observationFieldMappers (writer.go) matches metric descriptors by
// substring, and its entries for pressure ("stormglass_pressure_pa") and
// report interval ("stormglass_report_interval_s") do not match the real
// descriptor names — stormglass_pressure_mb and
// stormglass_report_interval_minutes (internal/tempest/metrics.go) — so
// WriteMetrics silently never populates observationRow.pressure or
// .reportInterval from Prometheus metrics. Confirmed present in HEAD before
// the uncommitted lint-debt refactor (git show HEAD:internal/postgres/writer.go),
// so this is not a regression introduced by that refactor.
//
// This test exists to flag the defect and prevent it from being
// silently re-broken or silently forgotten, not to endorse it as correct
// behavior. If it starts failing, the dispatch bug has been fixed — delete
// this test (and add pressure/reportInterval to the case table in
// TestPostgresWriter_WriteMetrics_MapsEachFieldToItsColumn above instead).
func TestPostgresWriter_WriteMetrics_PressureAndReportIntervalNotDispatched_KnownBug(t *testing.T) {
	w := &PostgresWriter{
		obsBatch: make(chan observationRow, 10),
		done:     make(chan struct{}),
	}

	report := tempestudp.TempestObservationReport{
		SerialNumber: "ST-KNOWNBUG",
		Obs: [][]float64{
			{1700000000, 1, 1, 1, 1, 1, 1013.25, 20, 75, 1, 1, 1, 1, 0, 1, 1, 1, 42},
		},
	}

	if err := w.WriteMetrics(t.Context(), report.Metrics()); err != nil {
		t.Fatalf("WriteMetrics returned unexpected error: %v", err)
	}

	select {
	case row := <-w.obsBatch:
		if row.pressure != 0 {
			t.Errorf("pressure = %v, want 0 — the dispatch mapper substring mismatch appears fixed; update this test", row.pressure)
		}
		if row.reportInterval != nil {
			t.Errorf("reportInterval = %v, want nil — the dispatch mapper substring mismatch appears fixed; update this test", *row.reportInterval)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observation row on w.obsBatch")
	}
}
