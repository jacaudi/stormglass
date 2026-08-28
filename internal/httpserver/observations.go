package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jacaudi/stormglass/internal/sqlite"
	"github.com/jacaudi/stormglass/internal/tempestudp"
)

// pressureTrendWindow is how far back HistoryPoints is queried to derive
// pressureTrend for the current observation. 3 hours is long enough to
// smooth past a single noisy sample yet short enough to reflect a
// short-term trend rather than a full daily cycle -- a judgment call with no
// authoritative source; see the task report for the rationale.
const pressureTrendWindow = 3 * time.Hour

// deviceStatusMaxAge bounds how old a device_status row may be before its
// readings are served as null.
//
// obs_st and device_status arrive independently, so without this the card
// would show "Last Report 12s ago" beside a signal reading that could be
// hours old, with no way for the client to tell. device_status is documented
// as roughly once a minute (tempestudp/report.go), so 15 minutes is fifteen
// consecutive misses -- a radio that is down, not one that is jittery. A
// stale reading presented as live is the same class of defect as a zero
// presented as a reading, and this is the one place the server can prevent
// it.
const deviceStatusMaxAge = 15 * time.Minute

// pressureTrendThresholdMB is the minimum |delta| (millibars, over
// pressureTrendWindow) required to call the trend "rising"/"falling" rather
// than "steady". Below this, sensor noise could otherwise flip the label
// sample-to-sample.
const pressureTrendThresholdMB = 0.5

// windChillMaxTempC and windChillMinWindKmh are the NWS/Environment Canada
// wind chill formula's validity bounds: below the temperature and above the
// wind speed. Outside this regime the formula does not apply and windChill
// returns the air temperature unchanged.
const (
	windChillMaxTempC   = 10.0
	windChillMinWindKmh = 4.8
)

// heatIndexMinTempC is the NWS heat-index formula's validity floor (80°F).
// Below it, feelsLike falls through to the wind-chill/plain-temperature
// branches instead.
const heatIndexMinTempC = 26.7

// applyDeviceStatus fills the sensor-health fields from the latest
// device_status row for THIS observation's serial (#196).
//
// Every failure mode leaves both fields nil, which is the honest answer:
//   - no row yet (fresh store, or the first device_status has not arrived)
//   - the row is older than deviceStatusMaxAge
//   - the query failed
//
// A query error degrades with a WARN rather than failing the request, exactly
// as the pressure-trend history above does. A radio reading must never 500 the
// dashboard.
func applyDeviceStatus(ctx context.Context, reader ObservationReader, obs sqlite.Observation, now int64, resp *currentObservation) {
	ds, err := reader.LatestDeviceStatus(ctx, obs.SerialNumber)
	if err != nil {
		if !errors.Is(err, sqlite.ErrDeviceStatusNotFound) {
			slog.WarnContext(ctx, "httpserver: latest device status", "error", err, "serial", obs.SerialNumber)
		}
		return
	}
	if now-ds.Timestamp > int64(deviceStatusMaxAge.Seconds()) {
		return // stale: better to say nothing than to present it as live
	}
	if ds.Rssi != nil {
		v := float64(*ds.Rssi)
		resp.SignalDbm = &v
	}
	resp.FirmwareVersion = ds.Firmware
}

// ObservationReader is the read-side dependency registerObservations needs,
// defined at the consumer site per go-standards §12: production wires
// *sqlite.Writer (which satisfies it), tests use fakeObservationReader.
type ObservationReader interface {
	// LatestObservationAny returns the newest stormglass_observations row across
	// all serials, or sqlite.ErrObservationNotFound if the table is empty.
	LatestObservationAny(ctx context.Context) (sqlite.Observation, error)
	// HistoryPoints returns the allowlisted field's samples in [from, to];
	// an unknown field is rejected with a non-nil error before any query runs.
	HistoryPoints(ctx context.Context, field string, from, to int64) ([]sqlite.Point, error)
	// SummarizeObservations returns the windowed min/max/total aggregates
	// over [from, to] backing GET /api/observations/summary.
	SummarizeObservations(ctx context.Context, from, to int64) (sqlite.Summary, error)
	// LatestDeviceStatus returns the newest device_status row for serial, or
	// sqlite.ErrDeviceStatusNotFound when that serial has none. SCOPED by
	// serial because device_status is not Tempest-only -- see the sqlite
	// implementation's doc comment.
	LatestDeviceStatus(ctx context.Context, serial string) (sqlite.DeviceStatus, error)
	// TemperatureExtremes returns the max and min air temperature in
	// [from, to] with the timestamp at which each occurred, backing
	// GET /api/almanac's four record columns. Every field is invalid when
	// the window holds no row with a non-NULL temp_air.
	TemperatureExtremes(ctx context.Context, from, to int64) (sqlite.TempExtremes, error)
}

// currentObservation is the wire shape for GET /api/observations/current.
// Field names and units are Contract C (design §11), pinned to
// web/src/types/weather.ts's CurrentObservation interface -- every field
// there must have a same-named counterpart here.
type currentObservation struct {
	Timestamp                  int64   `json:"timestamp"`
	WindLull                   float64 `json:"windLull"`
	WindAvg                    float64 `json:"windAvg"`
	WindGust                   float64 `json:"windGust"`
	WindDirection              float64 `json:"windDirection"`
	WindSampleInterval         float64 `json:"windSampleInterval"`
	StationPressure            float64 `json:"stationPressure"`
	AirTemperature             float64 `json:"airTemperature"`
	RelativeHumidity           float64 `json:"relativeHumidity"`
	Illuminance                float64 `json:"illuminance"`
	UVIndex                    float64 `json:"uvIndex"`
	SolarRadiation             float64 `json:"solarRadiation"`
	RainAccumulated            float64 `json:"rainAccumulated"`
	PrecipitationType          int64   `json:"precipitationType"`
	LightningStrikeAvgDistance float64 `json:"lightningStrikeAvgDistance"`
	LightningStrikeCount       float64 `json:"lightningStrikeCount"`
	Battery                    float64 `json:"battery"`
	ReportInterval             float64 `json:"reportInterval"`
	LocalDayRainAccumulation   float64 `json:"localDayRainAccumulation"`
	FeelsLike                  float64 `json:"feelsLike"`
	DewPoint                   float64 `json:"dewPoint"`
	WetBulbTemperature         float64 `json:"wetBulbTemperature"`
	HeatIndex                  float64 `json:"heatIndex"`
	WindChill                  float64 `json:"windChill"`
	PressureTrend              string  `json:"pressureTrend"`

	// SignalDbm and FirmwareVersion are the sensor's own health (#196),
	// sourced from the latest device_status row for this observation's serial
	// -- a DIFFERENT report type on a different cadence, so they are "latest
	// known device health" rather than part of this observation. They live
	// here because the UI derives its whole StationStatus from this one
	// response.
	//
	// These are the FIRST pointer fields on this type, and they are a
	// deliberate exception to deref's "absent SQL column -> report as zero"
	// rule below: 0 dBm is a valid reading, so zero-filling would present
	// absent data as a reading. Null is the honest answer when there is no
	// device_status row, when it is stale (deviceStatusMaxAge), or when the
	// query fails.
	//
	// SignalDbm is deliberately NOT named signalStrength: the previous UI
	// contract was 0-4 bars, and the units changed. A new name makes that
	// break visible at the type level rather than silently redefining a
	// field.
	SignalDbm       *float64 `json:"signalDbm"`
	FirmwareVersion *string  `json:"firmwareVersion"`
}

// historyResponse is the wire shape for GET /api/observations/history:
// Contract C's {"points":[{"t":..,"v":..}]}. sqlite.Point already carries the
// t/v json tags (single-sourced there), so this just wraps it.
type historyResponse struct {
	Points []sqlite.Point `json:"points"`
}

// summaryMinMax is a min/max pair shared by every summaryResponse field that
// has both. A nil pointer means the underlying SQL aggregate returned NULL
// (no rows had that column set in-window), not zero.
type summaryMinMax struct {
	Max *float64 `json:"max"`
	Min *float64 `json:"min"`
}

// summaryWindow echoes the requested window back to the caller, so the UI
// doesn't need to independently recompute from/to from days.
type summaryWindow struct {
	Days int   `json:"days"`
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// summaryResponse is the wire shape for GET /api/observations/summary:
// Contract C (design §11), pinned to web/src/types/weather.ts's
// RecordsSummary interface (Task 4) -- every field there must have a
// same-named counterpart here. Backend emits SI units unchanged.
type summaryResponse struct {
	Window         summaryWindow `json:"window"`
	Count          int64         `json:"count"`
	CoveredFrom    *int64        `json:"coveredFrom"`
	CoveredTo      *int64        `json:"coveredTo"`
	Temperature    summaryMinMax `json:"temperature"`
	Humidity       summaryMinMax `json:"humidity"`
	Pressure       summaryMinMax `json:"pressure"`
	WindMax        *float64      `json:"windMax"`
	GustMax        *float64      `json:"gustMax"`
	RainTotal      *float64      `json:"rainTotal"`
	LightningTotal *float64      `json:"lightningTotal"`
}

// summaryQueryTimeout bounds handleSummary's SummarizeObservations call so a
// slow full-table aggregate scan can't hold the request open indefinitely.
const summaryQueryTimeout = 5 * time.Second

// allowedSummaryDays is the exact set of windows the UI's Records card
// offers; unlike /history's from/to (which default rather than reject),
// there's no sensible fallback for a window the UI never asks for, so
// anything else 400s rather than silently coercing to a nearby value.
var allowedSummaryDays = map[int]bool{7: true, 30: true, 180: true, 365: true}

// f64 maps a nullable SQL float to its wire form: nil means the aggregate's
// underlying column was NULL for every row in-window, not zero.
func f64(n sql.NullFloat64) *float64 {
	if n.Valid {
		v := n.Float64
		return &v
	}
	return nil
}

// i64 is f64's sql.NullInt64 counterpart.
func i64(n sql.NullInt64) *int64 {
	if n.Valid {
		v := n.Int64
		return &v
	}
	return nil
}

// registerObservations registers the Contract C JSON API handlers reading
// SQLite. Additive per the Deps seam server.go documents: a new field
// (Observations) plus this one register call, siblings (registerHealthz,
// registerStatic) untouched.
func registerObservations(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /api/observations/current", func(w http.ResponseWriter, r *http.Request) {
		handleCurrentObservation(w, r, deps.Observations)
	})
	mux.HandleFunc("GET /api/observations/history", func(w http.ResponseWriter, r *http.Request) {
		handleHistory(w, r, deps.Observations)
	})
	mux.HandleFunc("GET /api/observations/summary", func(w http.ResponseWriter, r *http.Request) {
		handleSummary(w, r, deps.Observations)
	})
}

// handleCurrentObservation serves the newest observation (across all
// serials -- see sqlite.Writer.LatestObservationAny's doc comment for why a
// single-station appliance has no serial to scope by) as Contract C's
// currentObservation, including the server-computed derived fields.
func handleCurrentObservation(w http.ResponseWriter, r *http.Request, reader ObservationReader) {
	if reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "observation store not configured")
		return
	}

	ctx := r.Context()

	obs, err := reader.LatestObservationAny(ctx)
	if err != nil {
		if errors.Is(err, sqlite.ErrObservationNotFound) {
			writeJSONError(w, http.StatusNotFound, "no observation available")
			return
		}
		slog.ErrorContext(ctx, "httpserver: latest observation", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to load current observation")
		return
	}

	now := time.Now().Unix()
	trendPoints, err := reader.HistoryPoints(ctx, "pressure", now-int64(pressureTrendWindow.Seconds()), now)
	if err != nil {
		// Pressure trend is a nice-to-have derived field, not the reason this
		// request exists -- degrade to "steady" rather than failing the whole
		// response over a history-query error.
		slog.WarnContext(ctx, "httpserver: pressure history for trend", "error", err)
		trendPoints = nil
	}

	resp := toCurrentObservation(obs, pressureTrendFromHistory(trendPoints))
	applyDeviceStatus(ctx, reader, obs, now, &resp)

	writeJSON(w, http.StatusOK, resp)
}

// handleHistory serves HistoryPoints for the requested field, wrapped in
// Contract C's {"points": [...]} shape. field is passed straight through to
// HistoryPoints, whose allowlist rejects an unknown field -- that rejection
// is mapped to 400 here, not re-validated.
func handleHistory(w http.ResponseWriter, r *http.Request, reader ObservationReader) {
	if reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "observation store not configured")
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	from := parseEpochOrDefault(q.Get("from"), 0)
	to := parseEpochOrDefault(q.Get("to"), time.Now().Unix())

	points, err := reader.HistoryPoints(ctx, q.Get("field"), from, to)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid field")
		return
	}

	writeJSON(w, http.StatusOK, historyResponse{Points: points})
}

// handleSummary serves the windowed observation aggregates (Contract C's
// RecordsSummary) for a days query param drawn from allowedSummaryDays --
// deliberately stricter than handleHistory's from/to (which default on a bad
// value), since there is no sensible fallback for a window the UI never
// offers.
func handleSummary(w http.ResponseWriter, r *http.Request, reader ObservationReader) {
	if reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "observation store not configured")
		return
	}

	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || !allowedSummaryDays[days] {
		writeJSONError(w, http.StatusBadRequest, "days must be one of 7, 30, 180, 365")
		return
	}

	to := time.Now().Unix()
	from := to - int64(days)*86400

	ctx, cancel := context.WithTimeout(r.Context(), summaryQueryTimeout)
	defer cancel()

	s, err := reader.SummarizeObservations(ctx, from, to)
	if err != nil {
		slog.ErrorContext(ctx, "httpserver: summarize observations", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to load records summary")
		return
	}

	writeJSON(w, http.StatusOK, summaryResponse{
		Window:         summaryWindow{Days: days, From: from, To: to},
		Count:          s.Count,
		CoveredFrom:    i64(s.CoveredFrom),
		CoveredTo:      i64(s.CoveredTo),
		Temperature:    summaryMinMax{Max: f64(s.TempMax), Min: f64(s.TempMin)},
		Humidity:       summaryMinMax{Max: f64(s.HumidityMax), Min: f64(s.HumidityMin)},
		Pressure:       summaryMinMax{Max: f64(s.PressureMax), Min: f64(s.PressureMin)},
		WindMax:        f64(s.WindMax),
		GustMax:        f64(s.GustMax),
		RainTotal:      f64(s.RainTotal),
		LightningTotal: f64(s.LightningTotal),
	})
}

// parseEpochOrDefault parses s as a base-10 unix-epoch-seconds integer,
// returning def for an empty or unparseable string. Query-string from/to are
// bind-parameter integers to HistoryPoints (never formatted into SQL text),
// so a malformed value degrading to the default is safe -- not a boundary a
// 400 needs to guard.
func parseEpochOrDefault(s string, def int64) int64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// toCurrentObservation maps a raw sqlite.Observation to Contract C's wire
// shape, filling in the server-computed derived fields. trend is the already
// -computed pressureTrend (kept as a parameter so this stays a pure function
// -- fetching the trend history is handleCurrentObservation's job, not
// this one's).
func toCurrentObservation(obs sqlite.Observation, trend string) currentObservation {
	return currentObservation{
		Timestamp:                  obs.Timestamp,
		WindLull:                   obs.WindLull,
		WindAvg:                    obs.WindAvg,
		WindGust:                   obs.WindGust,
		WindDirection:              obs.WindDirection,
		WindSampleInterval:         deref(obs.WindSampleInterval),
		StationPressure:            obs.Pressure,
		AirTemperature:             obs.TempAir,
		RelativeHumidity:           obs.Humidity,
		Illuminance:                obs.Illuminance,
		UVIndex:                    obs.UVIndex,
		SolarRadiation:             obs.Irradiance,
		RainAccumulated:            obs.RainRate,
		PrecipitationType:          deref(obs.PrecipType),
		LightningStrikeAvgDistance: deref(obs.LightningDistance),
		LightningStrikeCount:       deref(obs.LightningStrikeCount),
		Battery:                    deref(obs.Battery),
		ReportInterval:             deref(obs.ReportInterval),
		// Tempest UDP field 18 (local-day rain accumulation) is not persisted
		// by internal/sqlite's writer -- zero-filled rather than invented; see
		// the task report's DONE_WITH_CONCERNS note.
		LocalDayRainAccumulation: 0,
		FeelsLike:                sanitize(feelsLikeC(obs.TempAir, obs.Humidity, obs.WindAvg)),
		DewPoint:                 sanitize(tempestudp.DewPointC(obs.TempAir, obs.Humidity)),
		WetBulbTemperature:       wetBulbTemperatureC(obs),
		HeatIndex:                sanitize(tempestudp.HeatIndexC(obs.TempAir, obs.Humidity)),
		WindChill:                sanitize(windChillC(obs.TempAir, obs.WindAvg)),
		PressureTrend:            trend,
	}
}

// deref returns *p, or the zero value of T if p is nil. The single "nil
// pointer means absent SQL column -> report as zero" mapping every Contract
// C integer/float field below needs, shared knowledge Contract C's spec
// states once ("nil -> 0") for every one of these optional columns.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// sanitize maps a non-finite float (NaN or ±Inf) to 0, matching this file's
// "absent means zero" convention for every other optional field (see deref).
// encoding/json rejects NaN/Inf outright: an unsanitized non-finite value
// reaching writeJSON's json.Encoder.Encode fails AFTER the 200 status line
// is already written, so the client gets 200 with an empty body instead of
// an error (SGE review M1). dewPoint (math.Log(0) at humidity=0), heatIndex,
// feelsLike, and windChill all route through this; wetBulbTemperatureC
// already guards its own NaN case, so it does not need it.
func sanitize(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// wetBulbTemperatureC returns obs's wet bulb temperature: the writer's own
// computed value when present (TempWetbulb is non-nil), else recomputed here
// from the stored raw fields (stored pressure is mb, which equals hPa, so no
// conversion is needed). WetBulbTemperatureC returns NaN for non-convergent
// inputs; since TempWetbulb being nil already means the writer's own attempt
// didn't converge, there is no second path to fall back to -- 0 is reported
// in that case, matching this handler's nil-pointer-as-absent convention.
func wetBulbTemperatureC(obs sqlite.Observation) float64 {
	if obs.TempWetbulb != nil {
		return *obs.TempWetbulb
	}
	wb := tempestudp.WetBulbTemperatureC(obs.TempAir, obs.Humidity, obs.Pressure)
	if math.IsNaN(wb) {
		return 0
	}
	return wb
}

// windChillC computes the NWS/Environment Canada metric wind chill:
//
//	WC = 13.12 + 0.6215*T - 11.37*V^0.16 + 0.3965*T*V^0.16
//
// (T in °C, V in km/h), valid only for T <= windChillMaxTempC and
// V > windChillMinWindKmh; outside that regime it returns T unchanged, since
// the formula is not defined (and not meaningful) outside it.
func windChillC(tempC, windMS float64) float64 {
	windKmh := windMS * 3.6
	if tempC > windChillMaxTempC || windKmh <= windChillMinWindKmh {
		return tempC
	}
	v := math.Pow(windKmh, 0.16)
	return 13.12 + 0.6215*tempC - 11.37*v + 0.3965*tempC*v
}

// feelsLikeC composites the three "how it actually feels" formulas into one
// Contract C value: heat index above heatIndexMinTempC, wind chill in the
// cold-and-windy regime it's valid for, otherwise the plain air temperature.
func feelsLikeC(tempC, humidityPercent, windMS float64) float64 {
	switch {
	case tempC >= heatIndexMinTempC:
		return tempestudp.HeatIndexC(tempC, humidityPercent)
	case tempC <= windChillMaxTempC && windMS*3.6 > windChillMinWindKmh:
		return windChillC(tempC, windMS)
	default:
		return tempC
	}
}

// pressureTrendFromHistory derives Contract C's pressureTrend enum from an
// ordered (by timestamp ascending, per HistoryPoints) slice of pressure
// samples: fewer than two points can't show a trend, so "steady"; otherwise
// compare the newest sample to the oldest against pressureTrendThresholdMB.
func pressureTrendFromHistory(points []sqlite.Point) string {
	if len(points) < 2 {
		return "steady"
	}
	delta := points[len(points)-1].V - points[0].V
	switch {
	case delta > pressureTrendThresholdMB:
		return "rising"
	case delta < -pressureTrendThresholdMB:
		return "falling"
	default:
		return "steady"
	}
}

// writeJSON encodes v as the JSON response body with status and the
// application/json Content-Type -- the shared "how a JSON API response is
// written" knowledge both observation handlers need (would change together
// if, e.g., the encoding needed a shared envelope added later).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpserver: encode json response", "error", err)
	}
}

// writeJSONError writes a {"error": message} JSON body.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
