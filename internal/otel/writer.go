// Package otel bridges Tempest observations into OpenTelemetry. Its Writer
// implements sink.MetricsWriter by recording each Tempest weather field onto
// a pre-registered OTel instrument. The instrument names are Contract B:
// chosen so the Collector's OTLP→Prometheus translation reproduces the exact
// existing stormglass_* metric names that WS4's PromQL depends on. See the
// instrument-name constants below for the full table.
package otel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/jacaudi/stormglass/internal/metrics"
	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterName is the instrumentation scope name for all instruments this
// writer registers.
const meterName = "stormglass"

// Contract B instrument names — the law WS4's PromQL depends on. A wrong
// dot/underscore/suffix here breaks WS4, so these are defined once and
// referenced both at instrument-creation time and (indirectly, via the
// pointer-identity switch in WriteMetrics) at the old-Prometheus-descriptor
// mapping site.
const (
	instTemperatureC         = "stormglass.temperature.c"
	instDewPointC            = "stormglass.dewpoint.c"
	instHeatIndexC           = "stormglass.heat_index.c"
	instWetBulbC             = "stormglass.wetbulb.c"
	instHumidityPercent      = "stormglass.humidity.percent"
	instPressureMb           = "stormglass.pressure.mb"
	instWindMetersPerSecond  = "stormglass.wind.meters_per_second"
	instWindDirectionDegrees = "stormglass.wind.direction.degrees"
	instUVIndex              = "stormglass.uv.index"
	instIrradianceWM2        = "stormglass.irradiance.w_m2"
	instIlluminanceLux       = "stormglass.illuminance.lux"
	instRainRateMmMin        = "stormglass.rain_rate.mm_min"
	instRainfallMm           = "stormglass.rainfall.mm"
	instLightningDistanceKm  = "stormglass.lightning.distance.km"
	instLightningStrikeCount = "stormglass.lightning.strike_count"
	instBatteryVolts         = "stormglass.battery.volts"
	instRssiDbm              = "stormglass.rssi.dbm"
	instUptimeSeconds        = "stormglass.uptime.seconds"
	instReboots              = "stormglass.reboots"
	instBusErrors            = "stormglass.bus_errors"
)

// Writer implements sink.MetricsWriter, recording each Tempest report field
// onto its Contract B instrument. Instruments are created once at
// construction time and reused for the writer's lifetime.
type Writer struct {
	temperatureC         metric.Float64Gauge
	dewPointC            metric.Float64Gauge
	heatIndexC           metric.Float64Gauge
	wetBulbC             metric.Float64Gauge
	humidityPercent      metric.Float64Gauge
	pressureMb           metric.Float64Gauge
	windMetersPerSecond  metric.Float64Gauge
	windDirectionDegrees metric.Float64Gauge
	uvIndex              metric.Float64Gauge
	irradianceWM2        metric.Float64Gauge
	illuminanceLux       metric.Float64Gauge
	rainRateMmMin        metric.Float64Gauge
	lightningDistanceKm  metric.Float64Gauge
	batteryVolts         metric.Float64Gauge
	rssiDbm              metric.Float64Gauge
	uptimeSeconds        metric.Float64Gauge

	rainfallMm           metric.Float64Counter
	lightningStrikeCount metric.Float64Counter

	// reboots and busErrors are ObservableCounters, not synchronous
	// Counters: RadioStats[1]/[2] are device-lifetime ABSOLUTE cumulative
	// counts (the full lifetime value is reported on every hub_status
	// broadcast, ~1/min) rather than per-interval deltas, so Counter.Add
	// would inflate without bound. handleHubStatusReport records the
	// latest absolute value per serial into rebootsBySerial /
	// busErrorsBySerial (guarded by mu); the callbacks registered below
	// observe that latest value once per collection cycle. mu also guards
	// concurrent access from the SDK's collection goroutine (which invokes
	// the callbacks) racing with WriteReport calls from the UDP-listener
	// goroutine.
	mu                sync.Mutex
	rebootsBySerial   map[string]int64
	busErrorsBySerial map[string]int64
	reboots           metric.Int64ObservableCounter
	busErrors         metric.Int64ObservableCounter
}

// NewWriter pre-registers every Contract B instrument on a Meter obtained
// from mp, returning an error if any instrument fails to register (e.g. a
// malformed name).
func NewWriter(mp metric.MeterProvider) (*Writer, error) {
	meter := mp.Meter(meterName)

	var errs []error
	newGauge := func(name string) metric.Float64Gauge {
		g, err := meter.Float64Gauge(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("create gauge %s: %w", name, err))
		}
		return g
	}
	newCounter := func(name string) metric.Float64Counter {
		c, err := meter.Float64Counter(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("create counter %s: %w", name, err))
		}
		return c
	}

	w := &Writer{
		temperatureC:         newGauge(instTemperatureC),
		dewPointC:            newGauge(instDewPointC),
		heatIndexC:           newGauge(instHeatIndexC),
		wetBulbC:             newGauge(instWetBulbC),
		humidityPercent:      newGauge(instHumidityPercent),
		pressureMb:           newGauge(instPressureMb),
		windMetersPerSecond:  newGauge(instWindMetersPerSecond),
		windDirectionDegrees: newGauge(instWindDirectionDegrees),
		uvIndex:              newGauge(instUVIndex),
		irradianceWM2:        newGauge(instIrradianceWM2),
		illuminanceLux:       newGauge(instIlluminanceLux),
		rainRateMmMin:        newGauge(instRainRateMmMin),
		lightningDistanceKm:  newGauge(instLightningDistanceKm),
		batteryVolts:         newGauge(instBatteryVolts),
		rssiDbm:              newGauge(instRssiDbm),
		uptimeSeconds:        newGauge(instUptimeSeconds),

		rainfallMm:           newCounter(instRainfallMm),
		lightningStrikeCount: newCounter(instLightningStrikeCount),

		rebootsBySerial:   make(map[string]int64),
		busErrorsBySerial: make(map[string]int64),
	}

	var err error
	w.reboots, err = meter.Int64ObservableCounter(instReboots,
		metric.WithInt64Callback(w.observeReboots))
	if err != nil {
		errs = append(errs, fmt.Errorf("create observable counter %s: %w", instReboots, err))
	}
	w.busErrors, err = meter.Int64ObservableCounter(instBusErrors,
		metric.WithInt64Callback(w.observeBusErrors))
	if err != nil {
		errs = append(errs, fmt.Errorf("create observable counter %s: %w", instBusErrors, err))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return w, nil
}

// observeReboots and observeBusErrors are the ObservableCounter callbacks
// registered in NewWriter: once per collection cycle, they report the
// latest absolute value recorded per serial (see the mu/rebootsBySerial doc
// comment on Writer).
func (w *Writer) observeReboots(_ context.Context, obs metric.Int64Observer) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for serial, v := range w.rebootsBySerial {
		obs.Observe(v, metric.WithAttributes(serialAttrs(serial)...))
	}
	return nil
}

func (w *Writer) observeBusErrors(_ context.Context, obs metric.Int64Observer) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for serial, v := range w.busErrorsBySerial {
		obs.Observe(v, metric.WithAttributes(serialAttrs(serial)...))
	}
	return nil
}

// serialAttrs builds the per-data-point attribute set: "serial" (the Tempest
// station serial number, replacing the old reserved "instance" label) plus
// any instrument-specific attributes such as "kind".
func serialAttrs(serial string, extra ...attribute.KeyValue) []attribute.KeyValue {
	return append([]attribute.KeyValue{attribute.String("serial", serial)}, extra...)
}

// WriteReport implements sink.MetricsWriter. It type-switches on the
// concrete report type (matching internal/postgres and internal/sqlite's
// writers) and reads raw obs fields directly — Contract B restructures the
// metric set relative to Report.Metrics()'s Prometheus output, so that
// output cannot be translated 1:1; see WriteMetrics for the API-export path,
// which does translate Report.Metrics() output.
func (w *Writer) WriteReport(ctx context.Context, report tempestudp.Report) error {
	switch r := report.(type) {
	case *tempestudp.TempestObservationReport:
		w.handleObservationReport(ctx, r)
	case *tempestudp.RapidWindReport:
		w.handleRapidWindReport(ctx, r)
	case *tempestudp.HubStatusReport:
		w.handleHubStatusReport(ctx, r)
	case *tempestudp.RainStartReport, *tempestudp.LightningStrikeReport:
		// No instruments — matches report.go, which returns nil Metrics()
		// for evt_precip/evt_strike.
	}
	return nil
}

func (w *Writer) handleObservationReport(ctx context.Context, r *tempestudp.TempestObservationReport) {
	for _, ob := range r.Obs {
		if len(ob) < 13 {
			continue
		}
		serial := r.SerialNumber

		w.gauge(ctx, w.windMetersPerSecond, ob[1], serial, attribute.String("kind", "lull"))
		w.gauge(ctx, w.windMetersPerSecond, ob[2], serial, attribute.String("kind", "avg"))
		w.gauge(ctx, w.windMetersPerSecond, ob[3], serial, attribute.String("kind", "gust"))
		w.gauge(ctx, w.windDirectionDegrees, ob[4], serial)
		w.gauge(ctx, w.pressureMb, ob[6], serial)
		w.gauge(ctx, w.temperatureC, ob[7], serial, attribute.String("kind", "air"))

		// WetBulbTemperatureC returns NaN for non-convergent inputs (e.g.
		// physically impossible humidity/pressure from a malformed report);
		// skip emitting the point rather than publishing NaN (mirrors the
		// same guard in tempestudp/report.go's Prometheus metrics path).
		wetBulb := tempestudp.WetBulbTemperatureC(ob[7], ob[8], ob[6])
		if !math.IsNaN(wetBulb) {
			w.gauge(ctx, w.wetBulbC, wetBulb, serial)
		}

		// DewPointC and HeatIndexC can both go non-finite on malformed
		// input (e.g. DewPointC's ln(RH/100) term for RH<=0, or either
		// helper given a NaN/Inf temperature) — skip emitting rather than
		// publishing NaN/Inf, mirroring the wetbulb guard above.
		dewPoint := tempestudp.DewPointC(ob[7], ob[8])
		if !isNonFinite(dewPoint) {
			w.gauge(ctx, w.dewPointC, dewPoint, serial)
		}
		heatIndex := tempestudp.HeatIndexC(ob[7], ob[8])
		if !isNonFinite(heatIndex) {
			w.gauge(ctx, w.heatIndexC, heatIndex, serial)
		}

		w.gauge(ctx, w.humidityPercent, ob[8], serial)
		w.gauge(ctx, w.illuminanceLux, ob[9], serial)
		w.gauge(ctx, w.uvIndex, ob[10], serial)
		w.gauge(ctx, w.irradianceWM2, ob[11], serial)
		w.gauge(ctx, w.rainRateMmMin, ob[12], serial)
		w.counter(ctx, w.rainfallMm, ob[12], serial)

		// Lightning metrics (fields 14 and 15).
		if len(ob) >= 16 {
			w.gauge(ctx, w.lightningDistanceKm, ob[14], serial)
			w.counter(ctx, w.lightningStrikeCount, ob[15], serial)
		}
		if len(ob) >= 17 {
			w.gauge(ctx, w.batteryVolts, ob[16], serial)
		}
		// ob[13] (precip type) and ob[17] (report interval) are not in
		// Contract B — dropped, matching the brief's field mapping.
	}
}

func (w *Writer) handleRapidWindReport(ctx context.Context, r *tempestudp.RapidWindReport) {
	if len(r.Ob) != 3 {
		return
	}
	w.gauge(ctx, w.windMetersPerSecond, r.Ob[1], r.SerialNumber, attribute.String("kind", "rapid"))
	w.gauge(ctx, w.windDirectionDegrees, r.Ob[2], r.SerialNumber)
}

func (w *Writer) handleHubStatusReport(ctx context.Context, r *tempestudp.HubStatusReport) {
	w.gauge(ctx, w.uptimeSeconds, r.Uptime, r.SerialNumber)
	w.gauge(ctx, w.rssiDbm, r.Rssi, r.SerialNumber)

	// radio_stats[1] and [2] (reboots, bus errors) are only present on a
	// well-formed hub_status broadcast; a malformed/short array must not
	// panic (mirrors the same guard in postgres/sqlite's writers). Both are
	// device-lifetime ABSOLUTE cumulative counts, so the latest value is
	// stored (overwriting any prior value for this serial), not added — see
	// the mu/rebootsBySerial doc comment on Writer. int64(...) truncation is
	// lossless here: RadioStats values are integer counts on the wire.
	if len(r.RadioStats) >= 3 {
		w.mu.Lock()
		w.rebootsBySerial[r.SerialNumber] = int64(r.RadioStats[1])
		w.busErrorsBySerial[r.SerialNumber] = int64(r.RadioStats[2])
		w.mu.Unlock()
	}
}

// isNonFinite reports whether v is NaN or +/-Inf — the guard condition for
// skipping a gauge record rather than publishing a non-finite value to
// Prometheus.
func isNonFinite(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

// gauge and counter are the small internal helpers that DRY "record a
// gauge/counter with serial(+extra) attributes", per the writer's one
// required design note.
func (w *Writer) gauge(ctx context.Context, g metric.Float64Gauge, value float64, serial string, extra ...attribute.KeyValue) {
	g.Record(ctx, value, metric.WithAttributes(serialAttrs(serial, extra...)...))
}

func (w *Writer) counter(ctx context.Context, c metric.Float64Counter, value float64, serial string, extra ...attribute.KeyValue) {
	c.Add(ctx, value, metric.WithAttributes(serialAttrs(serial, extra...)...))
}

// writeMetricsHandlers maps each old internal/metrics Prometheus descriptor
// pointer to the Contract B instrument WriteMetrics records it onto — the
// exact case-by-case mapping the switch below used to encode directly, now
// as a lookup table so WriteMetrics itself stays a flat dispatch loop. Being
// a package-level var, its handler closures sit outside any function body,
// so they carry no complexity cost for WriteMetrics (verified against the
// project's actual gocyclo tool before adopting this shape).
//
// metrics.Reboots and metrics.BusErrors have no entry: API-export (client.go's
// type switch) only ever produces *TempestObservationReport, never
// *HubStatusReport, so this path never carries a reboots/bus_errors metric to
// translate. Also, reboots/busErrors are now ObservableCounters (see
// handleHubStatusReport), which have no synchronous Add to call from here.
// metrics.ReportInterval (dropped in Contract B) and metrics.RainTotal
// (never emitted by any Report.Metrics() implementation) likewise have no
// entry and are silently skipped, matching the original switch's implicit
// default.
var writeMetricsHandlers = map[*prometheus.Desc]func(w *Writer, ctx context.Context, value float64, serial, kind string){
	metrics.Wind: func(w *Writer, ctx context.Context, value float64, serial, kind string) {
		w.gauge(ctx, w.windMetersPerSecond, value, serial, attribute.String("kind", kind))
	},
	metrics.WindDirection: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.windDirectionDegrees, value, serial)
	},
	metrics.Pressure: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.pressureMb, value, serial)
	},
	metrics.Temperature: func(w *Writer, ctx context.Context, value float64, serial, kind string) {
		switch kind {
		case "air":
			w.gauge(ctx, w.temperatureC, value, serial, attribute.String("kind", "air"))
		case "wetbulb":
			w.gauge(ctx, w.wetBulbC, value, serial)
		}
	},
	metrics.Humidity: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.humidityPercent, value, serial)
	},
	metrics.Illuminance: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.illuminanceLux, value, serial)
	},
	metrics.UV: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.uvIndex, value, serial)
	},
	metrics.Irradiance: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.irradianceWM2, value, serial)
	},
	metrics.RainRate: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.rainRateMmMin, value, serial)
	},
	metrics.LightningDistance: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.lightningDistanceKm, value, serial)
	},
	metrics.LightningStrikeCount: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.counter(ctx, w.lightningStrikeCount, value, serial)
	},
	metrics.Battery: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.batteryVolts, value, serial)
	},
	metrics.Uptime: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.uptimeSeconds, value, serial)
	},
	metrics.Rssi: func(w *Writer, ctx context.Context, value float64, serial, _ string) {
		w.gauge(ctx, w.rssiDbm, value, serial)
	},
}

// WriteMetrics implements sink.MetricsWriter for API-export mode: it
// translates each incoming Prometheus metric (built against the OLD
// internal/metrics descriptors) to its Contract B instrument by matching on
// the exact *prometheus.Desc pointer (metrics.Wind, metrics.Temperature,
// etc. are package-level vars, so m.Desc() is the same pointer that
// Report.Metrics() used to build m) and reading its label/value via
// m.Write(&dto.Metric{}). The "instance" label value becomes the "serial"
// attribute. See writeMetricsHandlers for the full per-descriptor mapping,
// including the metrics with no Contract B counterpart that are skipped.
//
// Known gap: stormglass.dewpoint.c and stormglass.heat_index.c are never
// populated via this path. Deriving them requires the SAME observation's
// temperature AND humidity together, but Report.Metrics() emits temperature
// and humidity as separate, independently-labeled prometheus.Metric values
// with no reliable correlation key across a flat []prometheus.Metric slice
// (especially once merged across multiple stations/timestamps in
// API-export mode). WriteReport is the primary, fully correct path — it has
// both raw fields together from the same ob row.
func (w *Writer) WriteMetrics(ctx context.Context, ms []prometheus.Metric) error {
	for _, m := range ms {
		var d dto.Metric
		if err := m.Write(&d); err != nil {
			return fmt.Errorf("write prometheus metric %s: %w", m.Desc(), err)
		}

		serial := labelValue(&d, "instance")
		kind := labelValue(&d, "kind")
		value := metricValue(&d)

		if handler, ok := writeMetricsHandlers[m.Desc()]; ok {
			handler(w, ctx, value, serial, kind)
		}
	}
	return nil
}

// labelValue returns the value of the named label pair, or "" if absent.
func labelValue(m *dto.Metric, name string) string {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

// metricValue extracts the numeric value from a dto.Metric regardless of
// whether it was built as a Gauge or a Counter.
func metricValue(m *dto.Metric) float64 {
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	return 0
}

// Flush is a no-op: OTel's SDK-side push (via the PeriodicReader configured
// in Setup) owns export timing, not this writer.
func (w *Writer) Flush(ctx context.Context) error { return nil }

// Close is a no-op: provider lifecycle (including final flush) is owned by
// Setup's returned shutdown function (Task 6.1), not the writer.
func (w *Writer) Close(ctx context.Context) error { return nil }
