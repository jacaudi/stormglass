package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jacaudi/stormglass/internal/config"
	"github.com/jacaudi/stormglass/internal/sqlite"
)

// errFakeStore simulates a store-layer failure for
// TestAPI_Almanac_StoreErrorIs500. Deliberately distinct from
// observations_test.go's errUnknownFieldForTest, which means something else.
var errFakeStore = errors.New("fake store failure")

// denverStation is a fixed, real station identity: Denver's coordinates and
// timezone, so sunrise/sunset are non-nil and the local date is not UTC.
func denverStation(t *testing.T) config.StationConfig {
	t.Helper()
	lat, lon := 39.74, -104.98
	return config.StationConfig{
		Name: strPtr("Backyard"), Latitude: &lat, Longitude: &lon,
		Location: mustLoad(t, "America/Denver"),
	}
}

func testDepsWithAlmanac(t *testing.T, reader ObservationReader, station config.StationConfig) Deps {
	t.Helper()
	return Deps{
		StaticFS:     fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
		Observations: reader,
		Station:      station,
		Almanac:      true,
	}
}

func getAlmanacJSON(t *testing.T, deps Deps) map[string]any {
	t.Helper()
	srv := New(deps)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/almanac", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

func nf(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
func ni(v int64) sql.NullInt64     { return sql.NullInt64{Int64: v, Valid: true} }

// TestAPI_Almanac_PopulatedWindows proves each of the four windows is queried
// independently and mapped to its own record column.
func TestAPI_Almanac_PopulatedWindows(t *testing.T) {
	station := denverStation(t)
	today, week, month, year := almanacWindows(time.Now(), station.Location)

	// Ordered today, week, month, year -- the order handleAlmanac queries in.
	reader := &fakeObservationReader{extremes: []sqlite.TempExtremes{
		{Max: nf(25), MaxAt: ni(today.From + 3600), Min: nf(10), MinAt: ni(today.From + 60)},
		{Max: nf(28), MaxAt: ni(week.From + 3600), Min: nf(8), MinAt: ni(week.From + 60)},
		{Max: nf(30), MaxAt: ni(month.From + 3600), Min: nf(5), MinAt: ni(month.From + 60)},
		{Max: nf(35), MaxAt: ni(year.From + 3600), Min: nf(-12), MinAt: ni(year.From + 60)},
	}}

	body := getAlmanacJSON(t, testDepsWithAlmanac(t, reader, station))

	for _, tc := range []struct {
		key      string
		wantHigh float64
		wantLow  float64
	}{
		{"today", 25, 10}, {"week", 28, 8}, {"month", 30, 5}, {"year", 35, -12},
	} {
		rec, ok := body[tc.key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want an object", tc.key, body[tc.key])
		}
		if rec["high"] != tc.wantHigh {
			t.Errorf("%s.high = %v, want %v", tc.key, rec["high"], tc.wantHigh)
		}
		if rec["low"] != tc.wantLow {
			t.Errorf("%s.low = %v, want %v", tc.key, rec["low"], tc.wantLow)
		}
		if rec["highDate"] == nil || rec["lowDate"] == nil {
			t.Errorf("%s date labels must be non-null when the values are: %v", tc.key, rec)
		}
	}
}

// TestAPI_Almanac_QueriesFourDistinctWindows proves handleAlmanac queries the
// four windows almanacWindows computes -- today, week, month, year, in that
// order -- rather than passing the same window four times. Every other test
// in this file answers purely by call index (fakeObservationReader.extremes
// is consumed in order), so a mutation to handleAlmanac's slice that queries
// {today, today, today, today} would pass all of them; this is the one test
// that observes the actual (from, to) each call carried.
//
// The From values are asserted positionally against almanacWindows' own
// output, never for pairwise distinctness: they legitimately coincide
// (today.From == week.From on every Sunday, == month.From on the 1st of the
// month, and all three == year.From on 1 January), so an "all differ"
// assertion would itself be flaky on those calendar dates -- precisely the
// map-collision bug shape this branch already fixed once, in a different
// form.
//
// To is deliberately not compared against a test-computed value:
// handleAlmanac reads time.Now() internally, a different instant than any
// `now` this test could capture. Instead this asserts the four recorded To
// values equal ONE ANOTHER, which handleAlmanac guarantees by construction
// (all four windows are derived from a single `now`) -- the real signal here
// is the From positions.
func TestAPI_Almanac_QueriesFourDistinctWindows(t *testing.T) {
	station := denverStation(t)
	wantToday, wantWeek, wantMonth, wantYear := almanacWindows(time.Now(), station.Location)

	reader := &fakeObservationReader{}
	getAlmanacJSON(t, testDepsWithAlmanac(t, reader, station))

	if len(reader.extremesCalls) != 4 {
		t.Fatalf("TemperatureExtremes called %d times, want 4", len(reader.extremesCalls))
	}

	wantFrom := []int64{wantToday.From, wantWeek.From, wantMonth.From, wantYear.From}
	labels := []string{"today", "week", "month", "year"}
	for i, want := range wantFrom {
		if got := reader.extremesCalls[i].From; got != want {
			t.Errorf("call %d (%s) From = %d, want %d", i, labels[i], got, want)
		}
	}

	for i := 1; i < len(reader.extremesCalls); i++ {
		if got, want := reader.extremesCalls[i].To, reader.extremesCalls[0].To; got != want {
			t.Errorf("call %d To = %d, want %d (all four windows share one `now`)", i, got, want)
		}
	}
}

// A freshly provisioned appliance has no history. It must render em-dashes,
// not NaN -- the shape the old passthrough produced.
func TestAPI_Almanac_EmptyStoreYieldsNulls(t *testing.T) {
	station := denverStation(t)
	body := getAlmanacJSON(t, testDepsWithAlmanac(t, &fakeObservationReader{}, station))

	for _, key := range []string{"today", "week", "month", "year"} {
		rec, ok := body[key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want an object", key, body[key])
		}
		for _, field := range []string{"high", "low", "highDate", "lowDate"} {
			v, present := rec[field]
			if !present {
				t.Errorf("%s.%s must be present as null, not absent", key, field)
			}
			if v != nil {
				t.Errorf("%s.%s = %v, want null", key, field, v)
			}
		}
	}
}

// A value and its label are always both present or both null: the store
// query cannot produce a mismatched pair, and the wire shape must not either.
func TestAPI_Almanac_ValueAndLabelAreNulledTogether(t *testing.T) {
	station := denverStation(t)
	today, _, _, _ := almanacWindows(time.Now(), station.Location)

	// Only today has data; the remaining three calls run past the end of the
	// slice and get an all-invalid TempExtremes.
	reader := &fakeObservationReader{extremes: []sqlite.TempExtremes{
		{Max: nf(25), MaxAt: ni(today.From + 3600), Min: nf(10), MinAt: ni(today.From + 60)},
	}}
	body := getAlmanacJSON(t, testDepsWithAlmanac(t, reader, station))

	todayRec := body["today"].(map[string]any)
	if todayRec["high"] == nil || todayRec["highDate"] == nil {
		t.Errorf("today must carry both a value and a label: %v", todayRec)
	}
	weekRec := body["week"].(map[string]any)
	if weekRec["high"] != nil || weekRec["highDate"] != nil {
		t.Errorf("week must null the value and the label together: %v", weekRec)
	}
}

// "Today" is the label when the extreme falls on the STATION's current local
// date -- computed server-side because the browser's timezone is the
// viewer's, not the station's.
func TestAPI_Almanac_DateLabels(t *testing.T) {
	station := denverStation(t)
	now := time.Now().In(station.Location)
	_, _, _, year := almanacWindows(now, station.Location)

	// An extreme 40 days ago is unambiguously not "Today" and stays inside
	// the year window in every month except January -- so pick the year
	// window's own start instead, which is always in the past and never today
	// unless the request happens on 1 January.
	older := year.From + 3600
	// Positions 1 and 2 (week, month) are deliberately empty.
	reader := &fakeObservationReader{extremes: []sqlite.TempExtremes{
		{Max: nf(25), MaxAt: ni(now.Unix()), Min: nf(10), MinAt: ni(now.Unix())},
		{},
		{},
		{Max: nf(35), MaxAt: ni(older), Min: nf(-12), MinAt: ni(older)},
	}}
	body := getAlmanacJSON(t, testDepsWithAlmanac(t, reader, station))

	if got := body["today"].(map[string]any)["highDate"]; got != "Today" {
		t.Errorf("today.highDate = %v, want \"Today\"", got)
	}

	wantLabel := time.Unix(older, 0).In(station.Location).Format("Jan 2")
	oy, om, od := time.Unix(older, 0).In(station.Location).Date()
	ny, nm, nd := now.Date()
	if oy == ny && om == nm && od == nd {
		t.Skip("the year window starts on today's date; the label is legitimately \"Today\"")
	}
	if got := body["year"].(map[string]any)["highDate"]; got != wantLabel {
		t.Errorf("year.highDate = %v, want %q", got, wantLabel)
	}
}

// Sunrise/sunset are PREFORMATTED station-local clock strings, never epochs:
// the client has no timezone to render an epoch against.
func TestAPI_Almanac_SunriseSunsetArePreformattedStrings(t *testing.T) {
	station := denverStation(t)
	body := getAlmanacJSON(t, testDepsWithAlmanac(t, &fakeObservationReader{}, station))

	for _, key := range []string{"sunrise", "sunset"} {
		v, present := body[key]
		if !present {
			t.Fatalf("%s must be present", key)
		}
		if _, isString := v.(string); !isString {
			t.Errorf("%s = %v (%T), want a preformatted string", key, v, v)
		}
	}
	if _, isFloat := body["daylightMinutes"].(float64); !isFloat {
		t.Errorf("daylightMinutes = %v (%T), want a number", body["daylightMinutes"], body["daylightMinutes"])
	}
}

// TestAPI_Almanac_DaylightAgreesWithItsBounds is date-dependent -- the
// handler reads time.Now() and cannot be given a date -- so it asserts an
// INVARIANT that holds whatever today is at Utqiagvik: daylightMinutes is
// non-null exactly when both bounds are. The deterministic coverage of the
// null paths themselves lives in TestAlmanacClock_NilIn_NilOut below and in
// internal/astro's A.3 polar rows (9-12, 15-17).
func TestAPI_Almanac_DaylightAgreesWithItsBounds(t *testing.T) {
	lat, lon := 71.2906, -156.7887 // Utqiagvik
	station := config.StationConfig{Latitude: &lat, Longitude: &lon, Location: time.UTC}

	body := getAlmanacJSON(t, testDepsWithAlmanac(t, &fakeObservationReader{}, station))

	bothPresent := body["sunrise"] != nil && body["sunset"] != nil
	hasDaylight := body["daylightMinutes"] != nil
	if bothPresent != hasDaylight {
		t.Fatalf("sunrise=%v sunset=%v daylightMinutes=%v -- daylightMinutes must be non-null "+
			"exactly when BOTH bounds are", body["sunrise"], body["sunset"], body["daylightMinutes"])
	}
}

// TestAlmanacClock_NilIn_NilOut pins the null path deterministically, which
// the date-dependent handler test above cannot. Above the Arctic Circle a day
// can legitimately have a sunrise and no sunset -- the two events refine at
// different Julian Days -- so each bound must map to null independently.
func TestAlmanacClock_NilIn_NilOut(t *testing.T) {
	denver := mustLoad(t, "America/Denver")

	if got := almanacClock(nil, denver); got != nil {
		t.Errorf("almanacClock(nil) = %v, want nil", *got)
	}

	at := time.Date(2026, time.June, 21, 11, 32, 0, 0, time.UTC)
	got := almanacClock(&at, denver)
	if got == nil {
		t.Fatal("almanacClock returned nil for a real instant")
	}
	// 11:32 UTC is 05:32 MDT -- rendered in the STATION's zone, not UTC.
	if *got != "5:32 AM" {
		t.Errorf("almanacClock = %q, want \"5:32 AM\" (station-local, not UTC)", *got)
	}
}

// The moon fields always compute -- they need no coordinates and no store.
func TestAPI_Almanac_MoonFieldsAlwaysPresent(t *testing.T) {
	station := denverStation(t)
	body := getAlmanacJSON(t, testDepsWithAlmanac(t, &fakeObservationReader{}, station))

	phase, ok := body["moonPhase"].(float64)
	if !ok || phase < 0 || phase >= 1 {
		t.Errorf("moonPhase = %v, want a float in [0, 1)", body["moonPhase"])
	}
	if name, ok := body["moonPhaseName"].(string); !ok || name == "" {
		t.Errorf("moonPhaseName = %v, want a non-empty string", body["moonPhaseName"])
	}
	illum, ok := body["moonIllumination"].(float64)
	if !ok || illum < 0 || illum > 1 {
		t.Errorf("moonIllumination = %v, want a float in [0, 1]", body["moonIllumination"])
	}
}

func TestAPI_Almanac_StoreErrorIs500(t *testing.T) {
	station := denverStation(t)
	deps := testDepsWithAlmanac(t, &fakeObservationReader{extremesErr: errFakeStore}, station)

	srv := New(deps)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/almanac", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// The route is unregistered -- not registered-and-refusing -- when the
// almanac is not enabled, and /api/capabilities agrees.
func TestAPI_Almanac_UnregisteredWhenDisabled(t *testing.T) {
	deps := testDepsWithAlmanac(t, &fakeObservationReader{}, denverStation(t))
	deps.Almanac = false

	srv := New(deps)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/almanac", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 -- a disabled feature's route must not be registered", rec.Code)
	}

	capsRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(capsRec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var caps map[string]any
	if err := json.Unmarshal(capsRec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps["almanac"] != false {
		t.Errorf("capabilities.almanac = %v, want false -- it must agree with the routing", caps["almanac"])
	}
}
