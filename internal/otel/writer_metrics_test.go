package otel

import (
	"testing"

	"tempestwx-utilities/internal/tempestudp"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestWriter_WriteMetrics_TranslatesOldPrometheusMetrics feeds the
// Prometheus metrics an existing Report.Metrics() call produces (the
// API-export path's input) through WriteMetrics and asserts they land on
// their Contract B instrument, with the old "instance" label becoming the
// "serial" attribute.
func TestWriter_WriteMetrics_TranslatesOldPrometheusMetrics(t *testing.T) {
	ctx := t.Context()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	w, err := NewWriter(mp)
	if err != nil {
		t.Fatalf("NewWriter() returned unexpected error: %v", err)
	}

	const serial = "TEST-002"
	hub := tempestudp.HubStatusReport{
		SerialNumber: serial,
		Uptime:       999,
		Rssi:         -70,
		Timestamp:    1,
		RadioStats:   []float64{1, 7, 5},
	}
	if err := w.WriteMetrics(ctx, hub.Metrics()); err != nil {
		t.Fatalf("WriteMetrics(hub status metrics) returned unexpected error: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("reader.Collect() returned unexpected error: %v", err)
	}

	gaugeCases := []struct {
		instrument string
		want       float64
	}{
		{"stormglass.uptime.seconds", 999},
		{"stormglass.rssi.dbm", -70},
	}
	for _, tc := range gaugeCases {
		m, ok := findMetric(rm, tc.instrument)
		if !ok {
			t.Fatalf("instrument %q not found in collected metrics", tc.instrument)
		}
		dp, ok := gaugePointFor(t, m, serial)
		if !ok {
			t.Fatalf("instrument %q: no data point for serial=%q", tc.instrument, serial)
		}
		if !almostEqual(dp.Value, tc.want) {
			t.Errorf("instrument %q value = %v, want %v", tc.instrument, dp.Value, tc.want)
		}
		if _, hasInstance := dp.Attributes.Value("instance"); hasInstance {
			t.Errorf("instrument %q: unexpected legacy 'instance' attribute present", tc.instrument)
		}
	}

	// stormglass.reboots and stormglass.bus_errors are deliberately NOT asserted
	// here: WriteMetrics no longer translates them (see writer.go's
	// WriteMetrics — the case was removed as dead code, since API-export's
	// client.go type switch only ever produces *TempestObservationReport,
	// never *HubStatusReport, so this path never carries a reboots/bus_errors
	// metric in practice). They are also now ObservableCounters (C1's fix
	// for the cumulative-counter inflation bug), fed only via
	// handleHubStatusReport, which WriteMetrics does not call.
	for _, instrument := range []string{"stormglass.reboots", "stormglass.bus_errors"} {
		if _, ok := findMetric(rm, instrument); ok {
			t.Errorf("instrument %q: unexpectedly found in WriteMetrics output — reboots/bus_errors are no longer translated by this path", instrument)
		}
	}
}
