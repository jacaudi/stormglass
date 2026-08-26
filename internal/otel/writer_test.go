package otel

import (
	"math"
	"sync"
	"testing"

	"tempestwx-utilities/internal/tempestudp"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// findMetric locates a recorded instrument by its exact Contract B name.
func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// dataPointFor returns the data point recorded with serial=serial, from
// either a Gauge's or a Sum's DataPoints slice.
func dataPointFor(points []metricdata.DataPoint[float64], serial string) (metricdata.DataPoint[float64], bool) {
	for _, dp := range points {
		if v, ok := dp.Attributes.Value("serial"); ok && v.AsString() == serial {
			return dp, true
		}
	}
	return metricdata.DataPoint[float64]{}, false
}

// gaugePointFor returns the Gauge data point recorded with serial=serial, if
// the instrument's aggregation is a Gauge.
func gaugePointFor(t *testing.T, m metricdata.Metrics, serial string) (metricdata.DataPoint[float64], bool) {
	t.Helper()
	g, ok := m.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("%s: data is %T, want metricdata.Gauge[float64]", m.Name, m.Data)
	}
	return dataPointFor(g.DataPoints, serial)
}

// sumPointFor returns the Sum (Counter) data point recorded with
// serial=serial, if the instrument's aggregation is a Sum.
func sumPointFor(t *testing.T, m metricdata.Metrics, serial string) (metricdata.DataPoint[float64], bool) {
	t.Helper()
	s, ok := m.Data.(metricdata.Sum[float64])
	if !ok {
		t.Fatalf("%s: data is %T, want metricdata.Sum[float64]", m.Name, m.Data)
	}
	return dataPointFor(s.DataPoints, serial)
}

// dataPointForInt64 is dataPointFor's int64 counterpart, for
// ObservableCounter-backed instruments (reboots, bus_errors).
func dataPointForInt64(points []metricdata.DataPoint[int64], serial string) (metricdata.DataPoint[int64], bool) {
	for _, dp := range points {
		if v, ok := dp.Attributes.Value("serial"); ok && v.AsString() == serial {
			return dp, true
		}
	}
	return metricdata.DataPoint[int64]{}, false
}

// sumPointForInt64 is sumPointFor's int64 counterpart, for the
// ObservableCounter-backed reboots/bus_errors instruments.
func sumPointForInt64(t *testing.T, m metricdata.Metrics, serial string) (metricdata.DataPoint[int64], bool) {
	t.Helper()
	s, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: data is %T, want metricdata.Sum[int64]", m.Name, m.Data)
	}
	return dataPointForInt64(s.DataPoints, serial)
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 0.001 }

// TestWriter_RecordsInstruments feeds one TempestObservationReport, one
// RapidWindReport, and one HubStatusReport through the writer and asserts
// the collected metricdata carries the EXACT Contract B instrument names
// with the right kinds (Gauge vs Sum), the recorded values, "serial" (not
// "instance") as the identifying attribute, and "kind" where required.
func TestWriter_RecordsInstruments(t *testing.T) {
	ctx := t.Context()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	w, err := NewWriter(mp)
	if err != nil {
		t.Fatalf("NewWriter() returned unexpected error: %v", err)
	}

	const serial = "TEST-001"
	obs := &tempestudp.TempestObservationReport{
		SerialNumber: serial,
		Obs: [][]float64{
			{
				0,       // 0 time epoch
				1.5,     // 1 wind lull
				2.5,     // 2 wind avg
				3.5,     // 3 wind gust
				180,     // 4 wind direction
				60,      // 5 wind sample interval
				1013.25, // 6 station pressure mb
				25,      // 7 air temp C
				50,      // 8 humidity %
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
	if err := w.WriteReport(ctx, obs); err != nil {
		t.Fatalf("WriteReport(observation) returned unexpected error: %v", err)
	}

	rapid := &tempestudp.RapidWindReport{
		SerialNumber: serial,
		Ob:           []float64{0, 4.2, 90},
	}
	if err := w.WriteReport(ctx, rapid); err != nil {
		t.Fatalf("WriteReport(rapid wind) returned unexpected error: %v", err)
	}

	hub := &tempestudp.HubStatusReport{
		SerialNumber: serial,
		Uptime:       12345,
		Rssi:         -60,
		RadioStats:   []float64{1, 4, 2},
	}
	if err := w.WriteReport(ctx, hub); err != nil {
		t.Fatalf("WriteReport(hub status) returned unexpected error: %v", err)
	}

	// Second observation report, a different serial, engineered so
	// WetBulbTemperatureC never converges AND DewPointC's ln(RH/100) term
	// goes NaN (humidity=-500, physically impossible — see
	// wetbulb_test.go's TestWetBulb_NonConvergentReturnsNaN for the same
	// input) — asserts both NaN gauges are skipped rather than recorded.
	//
	// HeatIndexC(25, -500) is verified (see below) to NOT go NaN for this
	// particular input: at 25°C (77°F, below the 80°F Rothfusz threshold),
	// HeatIndexC returns the air temperature unchanged without ever using
	// the humidity term, per the NWS convention documented on HeatIndexC.
	// A separate malformed report (NaN temperature, below) is used to
	// genuinely exercise heat_index's NaN guard instead.
	const nanSerial = "NAN-TEST"
	nanObs := &tempestudp.TempestObservationReport{
		SerialNumber: nanSerial,
		Obs: [][]float64{
			{0, 1, 2, 3, 180, 60, 900, 25, -500, 100, 1, 50, 0.1},
		},
	}
	if err := w.WriteReport(ctx, nanObs); err != nil {
		t.Fatalf("WriteReport(nan observation) returned unexpected error: %v", err)
	}
	if !math.IsNaN(tempestudp.DewPointC(25, -500)) {
		t.Fatal("test invariant violated: DewPointC(25, -500) is expected to be NaN")
	}
	if math.IsNaN(tempestudp.HeatIndexC(25, -500)) {
		t.Fatal("test invariant violated: HeatIndexC(25, -500) is expected to be non-NaN (below the 80°F threshold, air temp passthrough)")
	}

	// Third observation report, engineered so the air temperature itself is
	// NaN — this propagates into DewPointC, HeatIndexC, and
	// WetBulbTemperatureC alike, genuinely exercising heat_index's NaN
	// guard (which the -500-humidity case above does not reach).
	const nanTempSerial = "NAN-TEMP-TEST"
	nanTempObs := &tempestudp.TempestObservationReport{
		SerialNumber: nanTempSerial,
		Obs: [][]float64{
			{0, 1, 2, 3, 180, 60, 900, math.NaN(), 50, 100, 1, 50, 0.1},
		},
	}
	if err := w.WriteReport(ctx, nanTempObs); err != nil {
		t.Fatalf("WriteReport(nan temperature observation) returned unexpected error: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("reader.Collect() returned unexpected error: %v", err)
	}

	wetBulbWant := tempestudp.WetBulbTemperatureC(25, 50, 1013.25)
	dewPointWant := tempestudp.DewPointC(25, 50)
	heatIndexWant := tempestudp.HeatIndexC(25, 50)

	gaugeCases := []struct {
		instrument string
		want       float64
		kind       string // "" means no kind attribute asserted
	}{
		{"stormglass.temperature.c", 25, "air"},
		{"stormglass.dewpoint.c", dewPointWant, ""},
		{"stormglass.heat_index.c", heatIndexWant, ""},
		{"stormglass.wetbulb.c", wetBulbWant, ""},
		{"stormglass.humidity.percent", 50, ""},
		{"stormglass.pressure.mb", 1013.25, ""},
		// stormglass.wind.direction.degrees carries no "kind" attribute in
		// Contract B, so the observation report's value (180) and the
		// later rapid-wind report's value (90) share the same {serial}
		// attribute set — the Gauge's last recorded value (90, from the
		// rapid-wind report written after the observation report above)
		// wins, which is the correct "current direction" semantics for a
		// Gauge fed by two sources.
		{"stormglass.wind.direction.degrees", 90, ""},
		{"stormglass.uv.index", 5, ""},
		{"stormglass.irradiance.w_m2", 400, ""},
		{"stormglass.illuminance.lux", 1000, ""},
		{"stormglass.rain_rate.mm_min", 0.2, ""},
		{"stormglass.lightning.distance.km", 2.0, ""},
		{"stormglass.battery.volts", 2.75, ""},
		{"stormglass.rssi.dbm", -60, ""},
		{"stormglass.uptime.seconds", 12345, ""},
	}
	for _, tc := range gaugeCases {
		t.Run("gauge/"+tc.instrument, func(t *testing.T) {
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
			if tc.kind != "" {
				v, ok := dp.Attributes.Value("kind")
				if !ok || v.AsString() != tc.kind {
					t.Errorf("instrument %q: kind attribute = %v (ok=%v), want %q", tc.instrument, v, ok, tc.kind)
				}
			}
		})
	}

	// Wind is recorded three times for the observation report (lull, avg,
	// gust) and once more for the rapid-wind report (rapid) — all under the
	// single stormglass.wind.meters_per_second instrument, disambiguated by kind.
	t.Run("gauge/stormglass.wind.meters_per_second", func(t *testing.T) {
		m, ok := findMetric(rm, "stormglass.wind.meters_per_second")
		if !ok {
			t.Fatal("instrument stormglass.wind.meters_per_second not found")
		}
		g, ok := m.Data.(metricdata.Gauge[float64])
		if !ok {
			t.Fatalf("stormglass.wind.meters_per_second: data is %T, want Gauge[float64]", m.Data)
		}
		want := map[string]float64{"lull": 1.5, "avg": 2.5, "gust": 3.5, "rapid": 4.2}
		got := map[string]float64{}
		for _, dp := range g.DataPoints {
			if v, ok := dp.Attributes.Value("serial"); !ok || v.AsString() != serial {
				continue
			}
			kind, ok := dp.Attributes.Value("kind")
			if !ok {
				t.Fatalf("wind data point missing 'kind' attribute: %+v", dp.Attributes)
			}
			got[kind.AsString()] = dp.Value
		}
		for kind, want := range want {
			gotV, ok := got[kind]
			if !ok || !almostEqual(gotV, want) {
				t.Errorf("wind kind=%q value = %v (ok=%v), want %v", kind, gotV, ok, want)
			}
		}
	})

	counterCases := []struct {
		instrument string
		want       float64
	}{
		{"stormglass.rainfall.mm", 0.2},
		{"stormglass.lightning.strike_count", 3},
	}
	for _, tc := range counterCases {
		t.Run("counter/"+tc.instrument, func(t *testing.T) {
			m, ok := findMetric(rm, tc.instrument)
			if !ok {
				t.Fatalf("instrument %q not found in collected metrics", tc.instrument)
			}
			dp, ok := sumPointFor(t, m, serial)
			if !ok {
				t.Fatalf("instrument %q: no data point for serial=%q", tc.instrument, serial)
			}
			if !almostEqual(dp.Value, tc.want) {
				t.Errorf("instrument %q value = %v, want %v", tc.instrument, dp.Value, tc.want)
			}
		})
	}

	// reboots/bus_errors are ObservableCounters reporting the latest
	// device-lifetime absolute value (RadioStats[1]/[2], int64) — see C1's
	// doc comment on Writer — so they're asserted separately from the
	// float64 Sum instruments above.
	observableCounterCases := []struct {
		instrument string
		want       int64
	}{
		{"stormglass.reboots", 4},
		{"stormglass.bus_errors", 2},
	}
	for _, tc := range observableCounterCases {
		t.Run("counter/"+tc.instrument, func(t *testing.T) {
			m, ok := findMetric(rm, tc.instrument)
			if !ok {
				t.Fatalf("instrument %q not found in collected metrics", tc.instrument)
			}
			dp, ok := sumPointForInt64(t, m, serial)
			if !ok {
				t.Fatalf("instrument %q: no data point for serial=%q", tc.instrument, serial)
			}
			if dp.Value != tc.want {
				t.Errorf("instrument %q value = %v, want %v", tc.instrument, dp.Value, tc.want)
			}
		})
	}

	t.Run("wetbulb NaN input is skipped", func(t *testing.T) {
		m, ok := findMetric(rm, "stormglass.wetbulb.c")
		if !ok {
			t.Fatal("instrument stormglass.wetbulb.c not found")
		}
		if _, ok := gaugePointFor(t, m, nanSerial); ok {
			t.Errorf("stormglass.wetbulb.c: expected no data point for serial=%q (NaN wetbulb), but found one", nanSerial)
		}
	})

	t.Run("dewpoint NaN input is skipped", func(t *testing.T) {
		m, ok := findMetric(rm, "stormglass.dewpoint.c")
		if !ok {
			t.Fatal("instrument stormglass.dewpoint.c not found")
		}
		if _, ok := gaugePointFor(t, m, nanSerial); ok {
			t.Errorf("stormglass.dewpoint.c: expected no data point for serial=%q (NaN dewpoint, humidity=-500), but found one", nanSerial)
		}
	})

	t.Run("dewpoint and heat_index NaN-temperature input is skipped", func(t *testing.T) {
		dp, ok := findMetric(rm, "stormglass.dewpoint.c")
		if !ok {
			t.Fatal("instrument stormglass.dewpoint.c not found")
		}
		if _, ok := gaugePointFor(t, dp, nanTempSerial); ok {
			t.Errorf("stormglass.dewpoint.c: expected no data point for serial=%q (NaN temperature), but found one", nanTempSerial)
		}

		hi, ok := findMetric(rm, "stormglass.heat_index.c")
		if !ok {
			t.Fatal("instrument stormglass.heat_index.c not found")
		}
		if _, ok := gaugePointFor(t, hi, nanTempSerial); ok {
			t.Errorf("stormglass.heat_index.c: expected no data point for serial=%q (NaN temperature), but found one", nanTempSerial)
		}
	})
}

// collectObservableSum collects from reader and returns the int64 Sum data
// point for instrument/serial (the reboots/bus_errors ObservableCounters),
// failing the test if either is missing.
func collectObservableSum(t *testing.T, reader *sdkmetric.ManualReader, instrument, serial string) metricdata.DataPoint[int64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("reader.Collect() returned unexpected error: %v", err)
	}
	m, ok := findMetric(rm, instrument)
	if !ok {
		t.Fatalf("instrument %q not found in collected metrics", instrument)
	}
	dp, ok := sumPointForInt64(t, m, serial)
	if !ok {
		t.Fatalf("instrument %q: no data point for serial=%q", instrument, serial)
	}
	return dp
}

// TestWriter_CumulativeCountersDoNotInflate is the RED test for C1:
// reboots/bus_errors (RadioStats[1]/[2]) are device-lifetime ABSOLUTE
// cumulative counts — the full lifetime value is reported on EVERY
// hub_status broadcast (~1/min), not a per-interval delta (unlike rainfall
// and lightning.strike_count, which genuinely are deltas and correctly use
// Counter.Add — see handleObservationReport). Feeding the SAME absolute
// value through WriteReport twice must NOT double the exported total: the
// writer must record the latest absolute value per serial, not add it.
func TestWriter_CumulativeCountersDoNotInflate(t *testing.T) {
	ctx := t.Context()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	w, err := NewWriter(mp)
	if err != nil {
		t.Fatalf("NewWriter() returned unexpected error: %v", err)
	}

	const serial = "REBOOT-TEST"
	hub := &tempestudp.HubStatusReport{
		SerialNumber: serial,
		Uptime:       100,
		Rssi:         -55,
		RadioStats:   []float64{1, 4, 7},
	}

	// Feed the SAME hub_status broadcast twice, as the real device does once
	// a minute between reboots.
	if err := w.WriteReport(ctx, hub); err != nil {
		t.Fatalf("WriteReport(hub, 1st) returned unexpected error: %v", err)
	}
	if err := w.WriteReport(ctx, hub); err != nil {
		t.Fatalf("WriteReport(hub, 2nd) returned unexpected error: %v", err)
	}

	if dp := collectObservableSum(t, reader, "stormglass.reboots", serial); dp.Value != 4 {
		t.Errorf("stormglass.reboots after 2 identical broadcasts = %v, want 4 (not 8 — must not inflate)", dp.Value)
	}
	if dp := collectObservableSum(t, reader, "stormglass.bus_errors", serial); dp.Value != 7 {
		t.Errorf("stormglass.bus_errors after 2 identical broadcasts = %v, want 7 (not 14 — must not inflate)", dp.Value)
	}

	// A genuine change in the device's lifetime count (e.g. it actually
	// rebooted again) must update the exported value to the new absolute
	// value.
	hub2 := &tempestudp.HubStatusReport{
		SerialNumber: serial,
		Uptime:       200,
		Rssi:         -55,
		RadioStats:   []float64{1, 5, 7},
	}
	if err := w.WriteReport(ctx, hub2); err != nil {
		t.Fatalf("WriteReport(hub2) returned unexpected error: %v", err)
	}
	if dp := collectObservableSum(t, reader, "stormglass.reboots", serial); dp.Value != 5 {
		t.Errorf("stormglass.reboots after reboot count changed to 5 = %v, want 5", dp.Value)
	}
}

// TestWriter_CumulativeCounterStoreIsRaceSafe exercises the per-serial
// reboots/bus_errors store under -race: the SDK's ObservableCounter callback
// fires on the collection goroutine (triggered here by reader.Collect)
// CONCURRENTLY with WriteReport updating the store from the UDP-listener
// goroutine. An unsynchronized map would be flagged by -race; a mutex-backed
// store passes cleanly.
func TestWriter_CumulativeCounterStoreIsRaceSafe(t *testing.T) {
	ctx := t.Context()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	w, err := NewWriter(mp)
	if err != nil {
		t.Fatalf("NewWriter() returned unexpected error: %v", err)
	}

	const serial = "RACE-TEST"
	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range 50 {
			hub := &tempestudp.HubStatusReport{
				SerialNumber: serial,
				Uptime:       float64(i),
				Rssi:         -55,
				RadioStats:   []float64{1, float64(i), float64(i)},
			}
			if err := w.WriteReport(ctx, hub); err != nil {
				t.Errorf("WriteReport() returned unexpected error: %v", err)
			}
		}
	})
	wg.Go(func() {
		for range 50 {
			var rm metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &rm); err != nil {
				t.Errorf("reader.Collect() returned unexpected error: %v", err)
			}
		}
	})
	wg.Wait()
}
