package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jacaudi/stormglass/internal/config"
)

func testDepsWithStation(station config.StationConfig) Deps {
	return Deps{
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
		Station:  station,
	}
}

// getStationJSON issues GET /api/station and returns the decoded body as a
// generic map, so a test can assert a key is ABSENT rather than zero -- which
// a typed struct could not distinguish.
func getStationJSON(t *testing.T, deps Deps) map[string]any {
	t.Helper()
	srv := New(deps)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/station", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

func strPtr(s string) *string   { return &s }
func fltPtr(f float64) *float64 { return &f }

func TestAPI_Station_EmitsConfiguredFields(t *testing.T) {
	body := getStationJSON(t, testDepsWithStation(config.StationConfig{
		Name:      strPtr("Backyard"),
		Latitude:  fltPtr(40.1234),
		Longitude: fltPtr(-75.9876),
		Elevation: fltPtr(118.3),
		RadarSite: strPtr("TLX"),
	}))

	want := map[string]any{
		"name": "Backyard", "latitude": 40.1234, "longitude": -75.9876,
		"elevation": 118.3, "radarSite": "TLX",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, body[k], body[k], v)
		}
	}
}

// This is the test the design calls load-bearing: unconfigured coordinates
// must be ABSENT, not 0. The UI's hasCoordinates accepts 0 as finite, so a
// zero-valued wire field would put every default deployment on Null Island.
func TestAPI_Station_OmitsUnsetFields(t *testing.T) {
	body := getStationJSON(t, testDepsWithStation(config.StationConfig{}))

	for _, k := range []string{"name", "latitude", "longitude", "elevation", "radarSite"} {
		if v, present := body[k]; present {
			t.Errorf("%s must be ABSENT when unconfigured, got %v", k, v)
		}
	}
	if len(body) != 0 {
		t.Errorf("body = %v, want {}", body)
	}
}

// Zero is a legitimate configured value -- the equator, the prime meridian
// and sea level -- and must be emitted, not treated as unset.
func TestAPI_Station_EmitsLegitimateZeros(t *testing.T) {
	body := getStationJSON(t, testDepsWithStation(config.StationConfig{
		Latitude: fltPtr(0), Longitude: fltPtr(0), Elevation: fltPtr(0),
	}))

	for _, k := range []string{"latitude", "longitude", "elevation"} {
		v, present := body[k]
		if !present {
			t.Errorf("%s must be emitted when configured to 0", k)
			continue
		}
		if v != float64(0) {
			t.Errorf("%s = %v, want 0", k, v)
		}
	}
}

// Coordinates go out as a pair or not at all: a half-set pair is a
// malformed configuration, not a partial one. LoadStation rejects it, but
// Deps can be built by anyone, so the WIRE contract enforces it too.
func TestAPI_Station_CoordinatesAreAPairOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.StationConfig
	}{
		{"latitude_only", config.StationConfig{Latitude: fltPtr(40.1)}},
		{"longitude_only", config.StationConfig{Longitude: fltPtr(-75.9)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := getStationJSON(t, testDepsWithStation(tc.cfg))
			if _, present := body["latitude"]; present {
				t.Error("latitude must be absent when longitude is unset")
			}
			if _, present := body["longitude"]; present {
				t.Error("longitude must be absent when latitude is unset")
			}
		})
	}
}

// STATION_TIMEZONE is server-side only: its consumers are the almanac's
// calendar arithmetic and its date labels, and the UI has its own locale.
func TestAPI_Station_NeverEmitsTimezone(t *testing.T) {
	// time.UTC deliberately, not a LoadLocation call: this test asserts the
	// field never reaches the wire, and using a real zone would create a
	// dependency on Task 5's mustLoad helper for no added coverage.
	body := getStationJSON(t, testDepsWithStation(config.StationConfig{
		Name: strPtr("Backyard"), Location: time.UTC,
	}))
	if _, present := body["timezone"]; present {
		t.Error("timezone must never appear on the wire")
	}
}

// TestStationResponseFrom_IsTheOnlyMapping pins that the handler has no
// second source: everything it emits comes from the StationConfig it was
// handed. A future edit that reads an environment variable inside the
// handler would break this.
func TestStationResponseFrom_IsTheOnlyMapping(t *testing.T) {
	cfg := config.StationConfig{Name: strPtr("Backyard"), Elevation: fltPtr(118.3)}
	got := stationResponseFrom(cfg)

	if got.Name != cfg.Name || got.Elevation != cfg.Elevation {
		t.Fatal("stationResponseFrom must pass the configured pointers through unchanged")
	}
	if got.Latitude != nil || got.Longitude != nil || got.RadarSite != nil {
		t.Fatal("unset fields must stay nil")
	}
}
