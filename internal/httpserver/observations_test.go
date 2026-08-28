package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jacaudi/stormglass/internal/sqlite"
)

// fakeObservationReader is a hand-written test double for ObservationReader
// (per go-standards §8.4, prefer interface-based fakes over mock-generation
// libraries). It never touches a real database.
type fakeObservationReader struct {
	obs    sqlite.Observation
	obsErr error

	// history maps a field name to the points HistoryPoints returns for it.
	// A field absent from the map yields an empty (non-nil) slice, mirroring
	// the real writer's "no matches" behavior.
	history map[string][]sqlite.Point
	// historyErr, when non-nil, is returned by HistoryPoints unconditionally
	// -- simulating the real writer's allowlist rejecting an unknown field
	// before running any query.
	historyErr error

	// deviceStatus and deviceStatusErr back LatestDeviceStatus (#196).
	// The zero value means "no row", so a test that says nothing about
	// device status gets ErrDeviceStatusNotFound and null wire fields --
	// which is the correct default for a store that has never seen one.
	deviceStatus    sqlite.DeviceStatus
	deviceStatusErr error
	// deviceStatusSerial records the serial LatestDeviceStatus was called
	// with, so a test can assert the read is SCOPED to the observation's
	// serial rather than unscoped.
	deviceStatusSerial string

	// summary and summaryErr back SummarizeObservations for
	// TestHandleSummary_*.
	summary    sqlite.Summary
	summaryErr error

	// extremes is consumed in CALL ORDER: handleAlmanac queries today, week,
	// month, year in that fixed order, so element 0 answers today and so on.
	// A call past the end yields an all-invalid TempExtremes -- the real
	// writer's empty-window behaviour.
	//
	// Deliberately NOT a map keyed on the window's From timestamp. Those keys
	// COLLIDE: today.From == week.From on every Sunday (that is what
	// week-to-date means) and today.From == month.From on the 1st of every
	// month. Duplicate runtime keys in a Go map literal are legal and
	// silently last-wins, so a map would quietly collapse two windows onto
	// one answer and the test would fail on roughly one day in six.
	extremes    []sqlite.TempExtremes
	extremesN   int
	extremesErr error

	// extremesCalls records the (from, to) window of every TemperatureExtremes
	// call, in call order -- so a test can pin that handleAlmanac queries four
	// DISTINCT windows (today, week, month, year) rather than the same window
	// four times. Recorded unconditionally, including on the error path, so a
	// test can inspect how many calls happened before a failure.
	extremesCalls []almanacWindow
}

func (f *fakeObservationReader) LatestObservationAny(context.Context) (sqlite.Observation, error) {
	return f.obs, f.obsErr
}

func (f *fakeObservationReader) HistoryPoints(_ context.Context, field string, _, _ int64) ([]sqlite.Point, error) {
	if f.historyErr != nil {
		return nil, f.historyErr
	}
	return f.history[field], nil
}

func (f *fakeObservationReader) SummarizeObservations(context.Context, int64, int64) (sqlite.Summary, error) {
	return f.summary, f.summaryErr
}

func (f *fakeObservationReader) TemperatureExtremes(_ context.Context, from, to int64) (sqlite.TempExtremes, error) {
	f.extremesCalls = append(f.extremesCalls, almanacWindow{From: from, To: to})
	if f.extremesErr != nil {
		return sqlite.TempExtremes{}, f.extremesErr
	}
	if f.extremesN >= len(f.extremes) {
		return sqlite.TempExtremes{}, nil
	}
	te := f.extremes[f.extremesN]
	f.extremesN++
	return te, nil
}

func testDepsWithObservations(reader ObservationReader) Deps {
	return Deps{
		StaticFS:     fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
		Observations: reader,
	}
}

// TestAPI_CurrentObservation proves GET /api/observations/current marshals
// every Contract C field (web/src/types/weather.ts CurrentObservation) with
// SI values sourced from the newest sqlite observation, including at least
// one derived (server-computed) field, and 404s with the sentinel error.
func TestAPI_CurrentObservation(t *testing.T) {
	t.Run("returns_contract_c_shape", func(t *testing.T) {
		windSample := 3.0
		precip := int64(1)
		lightningDist := 2.1
		lightningCount := 4.0
		battery := 3.6
		reportInterval := 5.0

		reader := &fakeObservationReader{
			obs: sqlite.Observation{
				SerialNumber:         "ST-00001",
				Timestamp:            1700000000,
				WindLull:             1.5,
				WindAvg:              2.0,
				WindGust:             2.5,
				WindDirection:        180,
				WindSampleInterval:   &windSample,
				Pressure:             1013.25,
				TempAir:              20.5,
				Humidity:             55.0,
				Illuminance:          50000,
				UVIndex:              3.0,
				Irradiance:           500.0,
				RainRate:             0.5,
				PrecipType:           &precip,
				LightningDistance:    &lightningDist,
				LightningStrikeCount: &lightningCount,
				Battery:              &battery,
				ReportInterval:       &reportInterval,
			},
			// Rising pressure trend: last (1014.0) - first (1013.0) = +1.0 > 0.5.
			history: map[string][]sqlite.Point{
				"pressure": {{T: 1699999000, V: 1013.0}, {T: 1700000000, V: 1014.0}},
			},
		}

		srv := New(testDepsWithObservations(reader))
		req := httptest.NewRequest(http.MethodGet, "/api/observations/current", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/observations/current = %d, want 200, body: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v (body: %s)", err, rec.Body.String())
		}

		// Every Contract C key from web/src/types/weather.ts CurrentObservation
		// must be present.
		wantKeys := []string{
			"timestamp", "windLull", "windAvg", "windGust", "windDirection",
			"windSampleInterval", "stationPressure", "airTemperature",
			"relativeHumidity", "illuminance", "uvIndex", "solarRadiation",
			"rainAccumulated", "precipitationType", "lightningStrikeAvgDistance",
			"lightningStrikeCount", "battery", "reportInterval",
			"localDayRainAccumulation", "feelsLike", "dewPoint",
			"wetBulbTemperature", "heatIndex", "windChill", "pressureTrend",
		}
		for _, k := range wantKeys {
			if _, ok := got[k]; !ok {
				t.Errorf("response missing Contract C key %q: %v", k, got)
			}
		}

		// Spot-check raw field mapping.
		if got["airTemperature"] != 20.5 {
			t.Errorf("airTemperature = %v, want 20.5", got["airTemperature"])
		}
		if got["stationPressure"] != 1013.25 {
			t.Errorf("stationPressure = %v, want 1013.25", got["stationPressure"])
		}
		if got["solarRadiation"] != 500.0 {
			t.Errorf("solarRadiation = %v, want 500.0 (irradiance)", got["solarRadiation"])
		}
		if got["rainAccumulated"] != 0.5 {
			t.Errorf("rainAccumulated = %v, want 0.5 (rain_rate)", got["rainAccumulated"])
		}
		// A field never persisted by the writer (Tempest field 18); zero-filled
		// with the gap noted in the task report, not invented.
		if got["localDayRainAccumulation"] != 0.0 {
			t.Errorf("localDayRainAccumulation = %v, want 0 (not stored)", got["localDayRainAccumulation"])
		}

		// Derived field: pressureTrend from the 3h history window.
		if got["pressureTrend"] != "rising" {
			t.Errorf("pressureTrend = %v, want rising", got["pressureTrend"])
		}
		// Derived field: dewPoint must differ from airTemperature (proves it
		// was actually computed, not just echoed).
		if got["dewPoint"] == got["airTemperature"] {
			t.Errorf("dewPoint = %v, expected a computed value distinct from airTemperature", got["dewPoint"])
		}
	})

	// TestAPI_CurrentObservation/fractional_measurements_survive_to_json pins
	// the schema-conformance fix (SGE fix wave, Fix 2): WindSampleInterval,
	// LightningStrikeCount, and ReportInterval are float64 on the wire, not
	// int64. A regression that narrows any of them at the mapping site (the
	// exact bug this branch removed at the SQLite/Postgres boundary) would
	// leave TestAPI_CurrentObservation/returns_contract_c_shape green,
	// because that subtest's fixtures are integral and it only asserts key
	// presence for these fields. precipitationType stays an int on the wire
	// (categorical enum) and is asserted unchanged alongside the three
	// floats.
	t.Run("fractional_measurements_survive_to_json", func(t *testing.T) {
		windSample := 1.5
		precip := int64(1)
		lightningCount := 2.5
		reportInterval := 5.9

		reader := &fakeObservationReader{
			obs: sqlite.Observation{
				SerialNumber:         "ST-00001",
				Timestamp:            1700000000,
				WindSampleInterval:   &windSample,
				PrecipType:           &precip,
				LightningStrikeCount: &lightningCount,
				ReportInterval:       &reportInterval,
			},
		}

		srv := New(testDepsWithObservations(reader))
		req := httptest.NewRequest(http.MethodGet, "/api/observations/current", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/observations/current = %d, want 200, body: %s", rec.Code, rec.Body.String())
		}

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v (body: %s)", err, rec.Body.String())
		}

		if got["windSampleInterval"] != 1.5 {
			t.Errorf("windSampleInterval = %v, want 1.5", got["windSampleInterval"])
		}
		if got["lightningStrikeCount"] != 2.5 {
			t.Errorf("lightningStrikeCount = %v, want 2.5", got["lightningStrikeCount"])
		}
		if got["reportInterval"] != 5.9 {
			t.Errorf("reportInterval = %v, want 5.9", got["reportInterval"])
		}
		if got["precipitationType"] != float64(1) {
			t.Errorf("precipitationType = %v, want 1", got["precipitationType"])
		}
	})

	t.Run("not_found_returns_404_json", func(t *testing.T) {
		reader := &fakeObservationReader{obsErr: sqlite.ErrObservationNotFound}
		srv := New(testDepsWithObservations(reader))

		req := httptest.NewRequest(http.MethodGet, "/api/observations/current", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/observations/current = %d, want 404", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
	})
}

// TestAPI_History proves GET /api/observations/history returns the sqlite
// Point shape wrapped in {"points": [...]}  for a valid field, and 400s when
// the field is rejected by the writer's allowlist.
func TestAPI_History(t *testing.T) {
	t.Run("valid_field_returns_points", func(t *testing.T) {
		reader := &fakeObservationReader{
			history: map[string][]sqlite.Point{
				"temp_air": {{T: 1700000100, V: 15}, {T: 1700000200, V: 20}},
			},
		}
		srv := New(testDepsWithObservations(reader))

		req := httptest.NewRequest(http.MethodGet, "/api/observations/history?field=temp_air&from=&to=", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/observations/history = %d, want 200, body: %s", rec.Code, rec.Body.String())
		}

		var got struct {
			Points []sqlite.Point `json:"points"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		want := []sqlite.Point{{T: 1700000100, V: 15}, {T: 1700000200, V: 20}}
		if len(got.Points) != len(want) {
			t.Fatalf("points = %+v, want %+v", got.Points, want)
		}
		for i, p := range want {
			if got.Points[i] != p {
				t.Errorf("point %d = %+v, want %+v", i, got.Points[i], p)
			}
		}
	})

	t.Run("invalid_field_returns_400", func(t *testing.T) {
		reader := &fakeObservationReader{historyErr: errUnknownFieldForTest}
		srv := New(testDepsWithObservations(reader))

		req := httptest.NewRequest(http.MethodGet, "/api/observations/history?field=not_a_real_field", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/observations/history?field=not_a_real_field = %d, want 400", rec.Code)
		}
	})
}

// TestAPI_CurrentObservation_NaNSafe proves GET /api/observations/current
// returns a fully-parseable JSON body with finite numbers even when a
// derived field's input goes non-finite: humidity=0 sends
// tempestudp.DewPointC into math.Log(0) == -Inf, which propagates to NaN.
// encoding/json rejects NaN outright, so an unsanitized dewPoint would make
// json.Encoder.Encode fail after the 200 status line was already written,
// leaving the client with 200 + an EMPTY body instead of an error (SGE
// review M1). Only wetBulb was guarded before this fix.
func TestAPI_CurrentObservation_NaNSafe(t *testing.T) {
	reader := &fakeObservationReader{
		obs: sqlite.Observation{
			SerialNumber: "ST-00001",
			Timestamp:    1700000000,
			TempAir:      20.5,
			Humidity:     0, // triggers DewPointC's math.Log(0) -> NaN
			Pressure:     1013.25,
		},
	}

	srv := New(testDepsWithObservations(reader))
	req := httptest.NewRequest(http.MethodGet, "/api/observations/current", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/observations/current = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v (body: %q) -- body must be valid JSON, not empty from a failed NaN encode", err, rec.Body.String())
	}

	for _, field := range []string{"dewPoint", "heatIndex", "feelsLike", "windChill"} {
		v, ok := got[field].(float64)
		if !ok {
			t.Fatalf("response[%q] = %v (%T), want a JSON number", field, got[field], got[field])
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("response[%q] = %v, want a finite number", field, v)
		}
	}
}

// TestAPI_ObservationsNilStore proves both observation handlers 503 (not
// panic) when Deps.Observations is nil -- the postgres-only edge case (Task
// 1.6) where main.go starts the JSON API server without a sqlite writer.
func TestAPI_ObservationsNilStore(t *testing.T) {
	srv := New(testDepsWithObservations(nil))

	t.Run("current", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/observations/current", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /api/observations/current = %d, want 503", rec.Code)
		}
	})

	t.Run("history", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/observations/history?field=temp_air", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /api/observations/history = %d, want 503", rec.Code)
		}
	})
}

// errUnknownFieldForTest simulates the sqlite package's own "unknown history
// field" error without importing sqlite's unexported error construction --
// the handler must map ANY HistoryPoints error to 400, not just a specific
// sentinel, since HistoryPoints has no exported sentinel for this case.
var errUnknownFieldForTest = errors.New("unknown history field")

// TestHandleSummary_OK proves GET /api/observations/summary?days=N maps a
// valid allowlisted window to 200 with the summaryResponse shape (Contract C
// -- web/src/types/weather.ts RecordsSummary, Task 4), including the
// window echo and a spot-check of the NULL-able fields when they are valid.
func TestHandleSummary_OK(t *testing.T) {
	reader := &fakeObservationReader{summary: sqlite.Summary{
		Count:          5,
		CoveredFrom:    sql.NullInt64{Int64: 10, Valid: true},
		CoveredTo:      sql.NullInt64{Int64: 90, Valid: true},
		TempMax:        sql.NullFloat64{Float64: 25, Valid: true},
		TempMin:        sql.NullFloat64{Float64: 5, Valid: true},
		WindMax:        sql.NullFloat64{Float64: 8, Valid: true},
		GustMax:        sql.NullFloat64{Float64: 12, Valid: true},
		RainTotal:      sql.NullFloat64{Float64: 3.5, Valid: true},
		LightningTotal: sql.NullFloat64{Float64: 4, Valid: true},
	}}

	srv := New(testDepsWithObservations(reader))
	req := httptest.NewRequest(http.MethodGet, "/api/observations/summary?days=7", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/observations/summary?days=7 = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var got summaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, rec.Body.String())
	}
	if got.Window.Days != 7 || got.Count != 5 || *got.Temperature.Max != 25 || *got.RainTotal != 3.5 || *got.LightningTotal != 4 {
		t.Fatalf("bad body: %+v", got)
	}
}

// TestHandleSummary_BadDays proves every non-allowlisted days value --
// missing, non-numeric, or a number outside {7,30,180,365} -- 400s. This is
// deliberately stricter than /history's from/to (which default rather than
// reject), because a window that isn't one of the four the UI offers has no
// sensible fallback.
func TestHandleSummary_BadDays(t *testing.T) {
	for _, q := range []string{"", "?days=1", "?days=abc", "?days=8"} {
		req := httptest.NewRequest(http.MethodGet, "/api/observations/summary"+q, nil)
		rec := httptest.NewRecorder()
		New(testDepsWithObservations(&fakeObservationReader{})).Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/observations/summary%s = %d, want 400", q, rec.Code)
		}
	}
}

// TestHandleSummary_ReaderError proves GET /api/observations/summary 500s
// (with the ErrorContext log path exercised) when SummarizeObservations
// returns an error, e.g. a query timeout or a sqlite failure -- the one
// branch of handleSummary's error handling left uncovered.
func TestHandleSummary_ReaderError(t *testing.T) {
	reader := &fakeObservationReader{summaryErr: errors.New("boom")}
	req := httptest.NewRequest(http.MethodGet, "/api/observations/summary?days=7", nil)
	rec := httptest.NewRecorder()
	New(testDepsWithObservations(reader)).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/observations/summary?days=7 = %d, want 500", rec.Code)
	}
}

// TestHandleSummary_NilReader proves the summary handler 503s (not panics)
// when Deps.Observations is nil, matching the other two observation
// handlers' nil-guard pattern (see TestAPI_ObservationsNilStore).
func TestHandleSummary_NilReader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/observations/summary?days=7", nil)
	rec := httptest.NewRecorder()
	New(testDepsWithObservations(nil)).Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/observations/summary?days=7 = %d, want 503", rec.Code)
	}
}

func (f *fakeObservationReader) LatestDeviceStatus(_ context.Context, serial string) (sqlite.DeviceStatus, error) {
	f.deviceStatusSerial = serial
	if f.deviceStatusErr != nil {
		return sqlite.DeviceStatus{}, f.deviceStatusErr
	}
	if f.deviceStatus.SerialNumber == "" {
		return sqlite.DeviceStatus{}, sqlite.ErrDeviceStatusNotFound
	}
	return f.deviceStatus, nil
}

// currentBody issues GET /api/observations/current against reader and decodes
// the response, failing the test on any non-200.
func currentBody(t *testing.T, reader *fakeObservationReader) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	handleCurrentObservation(rec, httptest.NewRequest(http.MethodGet, "/api/observations/current", nil), reader)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func freshObs(serial string) sqlite.Observation {
	return sqlite.Observation{SerialNumber: serial, Timestamp: time.Now().Unix()}
}

// TestCurrentObservation_ServesDeviceStatus is #196's API half: the fields the
// UI hardcoded to null now carry real values.
func TestCurrentObservation_ServesDeviceStatus(t *testing.T) {
	rssi := int64(-61)
	fw := "156"
	reader := &fakeObservationReader{
		obs: freshObs("ST-API"),
		deviceStatus: sqlite.DeviceStatus{
			SerialNumber: "ST-API", Timestamp: time.Now().Unix(), Rssi: &rssi, Firmware: &fw,
		},
	}

	body := currentBody(t, reader)
	if got := body["signalDbm"]; got != float64(-61) {
		t.Errorf("signalDbm = %v, want -61", got)
	}
	if got := body["firmwareVersion"]; got != "156" {
		t.Errorf("firmwareVersion = %v, want \"156\"", got)
	}
	// SCOPED, not unscoped: device_status is not Tempest-only, so an
	// unscoped read could return another device's radio.
	if reader.deviceStatusSerial != "ST-API" {
		t.Errorf("LatestDeviceStatus called with %q, want the observation's serial %q",
			reader.deviceStatusSerial, "ST-API")
	}
}

// TestCurrentObservation_DeviceStatusNullWhenAbsent: no row must serve null,
// not a zero. 0 dBm is a valid reading and cannot double as "unknown".
func TestCurrentObservation_DeviceStatusNullWhenAbsent(t *testing.T) {
	body := currentBody(t, &fakeObservationReader{obs: freshObs("ST-NONE")})

	for _, field := range []string{"signalDbm", "firmwareVersion"} {
		v, present := body[field]
		if !present {
			t.Errorf("%s missing from the response entirely; want an explicit null", field)
		}
		if v != nil {
			t.Errorf("%s = %v, want null", field, v)
		}
	}
}

// TestCurrentObservation_DeviceStatusZeroSurvives is the other half of null
// honesty: a reported 0 dBm is a reading and must not collapse to null.
func TestCurrentObservation_DeviceStatusZeroSurvives(t *testing.T) {
	zero := int64(0)
	body := currentBody(t, &fakeObservationReader{
		obs: freshObs("ST-ZERO"),
		deviceStatus: sqlite.DeviceStatus{
			SerialNumber: "ST-ZERO", Timestamp: time.Now().Unix(), Rssi: &zero,
		},
	})
	if got, ok := body["signalDbm"]; !ok || got != float64(0) {
		t.Errorf("signalDbm = %v (present=%v), want 0 -- 0 dBm is a real reading", got, ok)
	}
}

// TestCurrentObservation_DeviceStatusStaleServesNull: obs_st and device_status
// have different cadences, so a fresh observation can sit beside an ancient
// radio reading. Past deviceStatusMaxAge the server says nothing rather than
// presenting a frozen value as live.
func TestCurrentObservation_DeviceStatusStaleServesNull(t *testing.T) {
	rssi := int64(-61)
	stale := time.Now().Add(-deviceStatusMaxAge - time.Minute).Unix()
	body := currentBody(t, &fakeObservationReader{
		obs: freshObs("ST-STALE"),
		deviceStatus: sqlite.DeviceStatus{
			SerialNumber: "ST-STALE", Timestamp: stale, Rssi: &rssi,
		},
	})
	if got := body["signalDbm"]; got != nil {
		t.Errorf("signalDbm = %v, want null for a device_status older than %v", got, deviceStatusMaxAge)
	}
}

// TestCurrentObservation_DeviceStatusErrorDegrades: a radio reading must never
// 500 the dashboard. Mirrors the pressure-trend degrade in the same handler.
func TestCurrentObservation_DeviceStatusErrorDegrades(t *testing.T) {
	body := currentBody(t, &fakeObservationReader{
		obs:             freshObs("ST-ERR"),
		deviceStatusErr: errors.New("database is locked"),
	})
	if got := body["signalDbm"]; got != nil {
		t.Errorf("signalDbm = %v, want null after a query error", got)
	}
	// The rest of the response must still be intact.
	if _, ok := body["airTemperature"]; !ok {
		t.Error("airTemperature missing -- a device-status error degraded the whole response")
	}
}
