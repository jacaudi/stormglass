package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/jacaudi/stormglass/internal/radar"
)

// testDepsForCapabilities returns Deps with only the static filesystem set,
// so each test can opt individual capabilities on without inheriting any.
func testDepsForCapabilities() Deps {
	return Deps{
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
	}
}

// stubRadarProxy returns a RadarProxy whose Get always succeeds. These tests
// only care whether the route exists, not what it serves -- but fakeRadarProxy
// (radar_test.go:18) dispatches straight to its getFunc field, so a bare
// &fakeRadarProxy{} panics the moment the route is actually requested.
func stubRadarProxy() *fakeRadarProxy {
	return &fakeRadarProxy{
		getFunc: func(_ context.Context, site, product string) (json.RawMessage, radar.Metadata, error) {
			return json.RawMessage(`{"type":"FeatureCollection","features":[]}`),
				radar.Metadata{Site: site, Product: product}, nil
		},
	}
}

// compactJSON normalizes a response body so formatting differences can't mask
// a key-name change -- key names are the contract here.
func compactJSON(b []byte) ([]byte, error) {
	dst := new(bytes.Buffer)
	if err := json.Compact(dst, b); err != nil {
		return nil, err
	}
	return dst.Bytes(), nil
}

func TestCapabilities_Document(t *testing.T) {
	tests := []struct {
		name    string
		almanac bool
		radar   RadarProxy
		want    string
	}{
		{
			name: "all disabled is the default",
			want: `{"forecast":false,"radar":false,"almanac":false}`,
		},
		{
			name:    "almanac only",
			almanac: true,
			want:    `{"forecast":false,"radar":false,"almanac":true}`,
		},
		{
			name:  "radar only, derived from a non-nil Radar dependency",
			radar: stubRadarProxy(),
			want:  `{"forecast":false,"radar":true,"almanac":false}`,
		},
		{
			name:    "radar and almanac enabled",
			almanac: true,
			radar:   stubRadarProxy(),
			want:    `{"forecast":false,"radar":true,"almanac":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDepsForCapabilities()
			deps.Almanac = tt.almanac
			deps.Radar = tt.radar

			req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
			rec := httptest.NewRecorder()
			New(deps).Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("GET /api/capabilities = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}

			// Compare the compacted body so formatting differences don't
			// mask a key-name change -- key names are the contract.
			var compacted []byte
			var err error
			if compacted, err = compactJSON(rec.Body.Bytes()); err != nil {
				t.Fatalf("response body is not valid JSON: %v (body %q)", err, rec.Body.String())
			}
			if string(compacted) != tt.want {
				t.Errorf("body = %s, want %s", compacted, tt.want)
			}
		})
	}
}

// TestCapabilities_RadarMatchesRoute proves the document and the routing read
// the same deps.Radar field: a nil Radar must report "radar":false AND leave
// GET /api/radar/{site} unregistered, and a non-nil one must do both the
// other way. This is the pairing that would force a conversation if someone
// later introduced a separate Radar bool.
func TestCapabilities_RadarMatchesRoute(t *testing.T) {
	tests := []struct {
		name           string
		radar          RadarProxy
		wantCapability bool
		wantRouteCode  int
	}{
		{name: "nil radar", radar: nil, wantCapability: false, wantRouteCode: http.StatusNotFound},
		{name: "wired radar", radar: stubRadarProxy(), wantCapability: true, wantRouteCode: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDepsForCapabilities()
			deps.Radar = tt.radar
			srv := New(deps)

			capsRec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(capsRec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))

			var got capabilities
			if err := json.Unmarshal(capsRec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode capabilities: %v", err)
			}
			if got.Radar != tt.wantCapability {
				t.Errorf("capabilities.radar = %v, want %v", got.Radar, tt.wantCapability)
			}

			routeRec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(routeRec, httptest.NewRequest(http.MethodGet, "/api/radar/TLX", nil))
			if routeRec.Code != tt.wantRouteCode {
				t.Errorf("GET /api/radar/TLX = %d, want %d", routeRec.Code, tt.wantRouteCode)
			}
		})
	}
}

// TestCapabilities_ForecastIsAlwaysFalse pins that there is no way to enable
// the forecast card: its provider was the WeatherFlow proxy, which is gone,
// and #81 restores the capability alongside a tokenless NWS provider.
func TestCapabilities_ForecastIsAlwaysFalse(t *testing.T) {
	caps := newCapabilities(Deps{Almanac: true})
	if caps.Forecast {
		t.Fatal("capabilities.forecast must be false until issue #81 supplies a provider")
	}
}

// TestAPI_Forecast_IsNotRegistered proves the route is gone, not merely
// disabled.
func TestAPI_Forecast_IsNotRegistered(t *testing.T) {
	srv := New(Deps{StaticFS: fstest.MapFS{"index.html": {Data: []byte("x")}}, Almanac: true})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/forecast", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
