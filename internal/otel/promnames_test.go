package otel

import (
	"maps"
	"slices"
	"testing"

	"github.com/jacaudi/stormglass/internal/metrics"
	"github.com/jacaudi/stormglass/internal/tempestudp"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// contractBNames is Contract B's right column: the exact stormglass_* Prometheus
// family names the OTel writer's instruments must translate to via the real
// Collector (and, per this test, the real in-process otelprom exporter).
// Both tests below share this literal — it's the same knowledge (the target
// contract), so it lives in one place rather than two copies that could
// drift apart.
var contractBNames = []string{
	"stormglass_temperature_c", "stormglass_dewpoint_c", "stormglass_heat_index_c",
	"stormglass_wetbulb_c", "stormglass_humidity_percent", "stormglass_pressure_mb",
	"stormglass_wind_meters_per_second", "stormglass_wind_direction_degrees",
	"stormglass_uv_index", "stormglass_irradiance_w_m2", "stormglass_illuminance_lux",
	"stormglass_rain_rate_mm_min", "stormglass_rainfall_mm_total",
	"stormglass_lightning_distance_km", "stormglass_lightning_strike_count_total",
	"stormglass_battery_volts", "stormglass_rssi_dbm", "stormglass_uptime_seconds",
	"stormglass_reboots_total", "stormglass_bus_errors_total",
}

// fullSampleReport returns a TempestObservationReport with physically valid
// field values (temp ~20C, RH ~60%, pressure ~1013mb) so the finite-only
// derived gauges (dewpoint, heat_index, wetbulb — see writer.go's
// isNonFinite guards) actually emit a data point, in addition to every other
// obs-derived instrument.
func fullSampleReport() *tempestudp.TempestObservationReport {
	return &tempestudp.TempestObservationReport{
		SerialNumber: "SAMPLE-001",
		Obs: [][]float64{
			{
				0,       // 0 time epoch
				1.5,     // 1 wind lull
				2.5,     // 2 wind avg
				3.5,     // 3 wind gust
				180,     // 4 wind direction
				60,      // 5 wind sample interval
				1013.25, // 6 station pressure mb
				20,      // 7 air temp C
				60,      // 8 humidity %
				1000,    // 9 illuminance lux
				5,       // 10 uv index
				400,     // 11 irradiance w/m2
				0.2,     // 12 rain amount mm (previous minute)
				0,       // 13 precip type
				2.0,     // 14 lightning distance km
				3,       // 15 lightning strike count
				2.75,    // 16 battery volts
				1,       // 17 report interval minutes
			},
		},
	}
}

// fullSampleHubStatusReport returns a HubStatusReport with RadioStats long
// enough (len >= 3) to populate the reboots/bus_errors ObservableCounters,
// plus Uptime/Rssi, so the 4 hub-status-only instruments are populated.
func fullSampleHubStatusReport() *tempestudp.HubStatusReport {
	return &tempestudp.HubStatusReport{
		SerialNumber: "SAMPLE-001",
		Uptime:       12345,
		Rssi:         -60,
		RadioStats:   []float64{1, 4, 2},
	}
}

// TestPrometheusExporterEmitsContractBNames drives the OTel writer through
// the REAL in-process Prometheus exporter (go.opentelemetry.io/otel/exporters/prometheus),
// which performs the exact OTLP->Prometheus normalization the Collector uses
// (dotted->underscore, unit suffixes, _total for monotonic counters).
// Gathering the registry and asserting the emitted family names is a genuine
// translation check — not a comparison of two hand-written maps.
func TestPrometheusExporterEmitsContractBNames(t *testing.T) {
	// The otelprom exporter's dot->underscore sanitization is gated on the
	// prometheus/common package-level model.NameValidationScheme, which
	// defaults to UTF8Validation (the pinned github.com/prometheus/common
	// version supports UTF-8 metric names and skips legacy sanitization by
	// default). A production Collector's prometheusexporter must be run in
	// legacy (underscore) naming mode to reproduce Contract B's stormglass_*
	// names, so this test forces that same mode to genuinely exercise the
	// translation Contract B depends on, restoring the prior value after.
	//nolint:staticcheck // SA1019: model.NameValidationScheme is deprecated but its own doc
	// comment names exactly this use ("a temporary assignment to LegacyValidation... to aid
	// in debugging unforeseen results of the [UTF8Validation] change") — this test needs the
	// legacy scheme to reproduce a production Collector's default translation.
	// Production counterpart: DOC.1 (the OTel Collector config) must enable
	// legacy/underscore naming for its prometheusexporter. This override only
	// proves the instrument names are correct — it does not prove that a
	// default-configured Collector would emit these underscored names on its
	// own; that behavior is DOC.1's responsibility, not this test's.
	prevScheme := model.NameValidationScheme
	model.NameValidationScheme = model.LegacyValidation           //nolint:staticcheck // see above
	t.Cleanup(func() { model.NameValidationScheme = prevScheme }) //nolint:staticcheck // see above

	reg := prom.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))

	w, err := NewWriter(mp)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	// Two reports are required: the observation report emits the 16
	// obs-derived families; the hub-status report emits the remaining 4
	// (uptime, rssi, reboots, bus_errors) — see handleHubStatusReport.
	if err := w.WriteReport(ctx, fullSampleReport()); err != nil {
		t.Fatalf("WriteReport(observation) returned unexpected error: %v", err)
	}
	if err := w.WriteReport(ctx, fullSampleHubStatusReport()); err != nil {
		t.Fatalf("WriteReport(hub status) returned unexpected error: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}

	for _, n := range contractBNames {
		if !got[n] {
			t.Errorf("missing Prometheus metric %q (got %v)", n, slices.Sorted(maps.Keys(got)))
		}
	}
	// Negative guards: the OLD names must NOT appear (the intended breaks).
	for _, old := range []string{"stormglass_wind_ms", "stormglass_uptime_seconds_total", "stormglass_rainfall_total", "stormglass_lightning_strike_count", "stormglass_report_interval_minutes"} {
		if got[old] {
			t.Errorf("stale metric name %q still emitted", old)
		}
	}
}

// oldNames is the CURRENT descriptor set in internal/tempest/metrics.go
// (Contract B's left column) as of this task. *prometheus.Desc has no
// exported name accessor, so this mirrors metrics.go's fqName literals
// directly rather than parsing its unexported internals; the length check
// in TestMetricMigrationList below guards against this list silently going
// stale if a descriptor is added to or removed from metrics.All.
var oldNames = []string{
	"stormglass_uptime_seconds_total", "stormglass_rssi_dbm", "stormglass_reboots_total",
	"stormglass_bus_errors_total", "stormglass_illuminance_lux", "stormglass_uv_index",
	"stormglass_rain_rate_mm_min", "stormglass_wind_ms", "stormglass_wind_direction_degrees",
	"stormglass_battery_volts", "stormglass_report_interval_minutes", "stormglass_irradiance_w_m2",
	"stormglass_rainfall_total", "stormglass_pressure_mb", "stormglass_temperature_c",
	"stormglass_humidity_percent", "stormglass_lightning_distance_km", "stormglass_lightning_strike_count",
}

// TestMetricMigrationList diffs Contract B's right column (contractBNames)
// against the CURRENT descriptor names in internal/tempest/metrics.go
// (oldNames) and asserts the diff is EXACTLY the documented break set:
// 4 renames, 1 drop, 3 adds, plus the (not name-diffable) instance->serial
// label rename, which is logged here for DOC.2 but already asserted
// per-instrument by writer_test.go's "instance" absence checks — no need to
// duplicate that assertion here.
func TestMetricMigrationList(t *testing.T) {
	// Count-only guard: catches an add/remove drift in metrics.go, but not a
	// same-count rename. Acceptable here because metrics.go is the frozen
	// legacy contract being replaced, not a list this test needs to track
	// element-for-element going forward.
	if len(oldNames) != len(metrics.All) {
		t.Fatalf("oldNames has %d entries but metrics.All has %d — a descriptor was added/removed in metrics.go without updating this test's oldNames list", len(oldNames), len(metrics.All))
	}

	renamed := map[string]string{
		"stormglass_wind_ms":                "stormglass_wind_meters_per_second",
		"stormglass_uptime_seconds_total":   "stormglass_uptime_seconds",
		"stormglass_rainfall_total":         "stormglass_rainfall_mm_total",
		"stormglass_lightning_strike_count": "stormglass_lightning_strike_count_total",
	}
	dropped := map[string]bool{
		"stormglass_report_interval_minutes": true,
	}
	added := map[string]bool{
		"stormglass_dewpoint_c":   true,
		"stormglass_heat_index_c": true,
		"stormglass_wetbulb_c":    true,
	}

	oldSet := map[string]bool{}
	for _, n := range oldNames {
		oldSet[n] = true
	}
	newSet := map[string]bool{}
	for _, n := range contractBNames {
		newSet[n] = true
	}

	// Every old name must be explained: unchanged (present in both sets),
	// a documented rename source, or a documented drop.
	for _, old := range oldNames {
		switch {
		case newSet[old]:
			// unchanged
		case renamed[old] != "":
			if !newSet[renamed[old]] {
				t.Errorf("renamed[%q] = %q, but %q is not in Contract B", old, renamed[old], renamed[old])
			}
		case dropped[old]:
			// documented drop
		default:
			t.Errorf("old name %q changed but is not in the documented rename/drop set", old)
		}
	}

	// Every new name must be explained: unchanged, a documented rename
	// target, or a documented addition.
	renameTargets := map[string]bool{}
	for _, to := range renamed {
		renameTargets[to] = true
	}
	for _, n := range contractBNames {
		switch {
		case oldSet[n]:
			// unchanged
		case renameTargets[n]:
			// documented rename target
		case added[n]:
			// documented addition
		default:
			t.Errorf("new name %q is not in the old set and is not a documented rename target or addition", n)
		}
	}

	t.Logf("metric migration (Contract B): renamed=%v dropped=%v added=%v", renamed, slices.Sorted(maps.Keys(dropped)), slices.Sorted(maps.Keys(added)))
	t.Log(`label migration: the "instance" label (every old descriptor's sole/shared variable label) is replaced by "serial" on every Contract B instrument (see writer.go's serialAttrs) — asserted per-instrument in writer_test.go's TestWriter_RecordsInstruments`)
}
