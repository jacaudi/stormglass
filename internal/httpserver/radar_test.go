package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jacaudi/stormglass/internal/radar"
)

// fakeRadarProxy is a hand-written RadarProxy test double (go-standards §8.4:
// prefer fakes over mocks). getFunc lets each test control the returned
// (body, error) without a mock-expectation framework.
type fakeRadarProxy struct {
	getFunc func(ctx context.Context, site, product string) (json.RawMessage, radar.Metadata, error)
}

func (f *fakeRadarProxy) Get(ctx context.Context, site, product string) (json.RawMessage, radar.Metadata, error) {
	return f.getFunc(ctx, site, product)
}

// testDepsWithRadar returns a Deps wired to rp (the radar proxy or fake, nil
// for the disabled case), with the same hermetic in-memory static filesystem
// the other httpserver tests use.
func testDepsWithRadar(rp RadarProxy) Deps {
	return Deps{
		StaticFS: fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
		Radar:    rp,
	}
}

// TestRadarHandler_ServesGeoJSON proves GET /api/radar/{site} returns the
// fake proxy's GeoJSON body unchanged with a 200 and application/json.
func TestRadarHandler_ServesGeoJSON(t *testing.T) {
	const geojson = `{"type":"FeatureCollection","features":[],"metadata":{"site":"TLX","product":"N0B"}}`

	var gotSite, gotProduct string
	fake := &fakeRadarProxy{getFunc: func(_ context.Context, site, product string) (json.RawMessage, radar.Metadata, error) {
		gotSite, gotProduct = site, product
		return json.RawMessage(geojson), radar.Metadata{Site: site, Product: product}, nil
	}}

	srv := New(testDepsWithRadar(fake))

	req := httptest.NewRequest(http.MethodGet, "/api/radar/TLX?product=N0B", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/radar/TLX = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Body.String() != geojson {
		t.Errorf("body = %s, want proxy body passed through unchanged: %s", rec.Body.String(), geojson)
	}
	if gotSite != "TLX" {
		t.Errorf("proxy.Get site = %q, want TLX", gotSite)
	}
	if gotProduct != "N0B" {
		t.Errorf("proxy.Get product = %q, want N0B", gotProduct)
	}
}

// TestRadarHandler_RejectsInvalidSite proves a site code that fails
// radar.IsValidSite's exact-match allowlist is rejected with 400 before the
// proxy is ever called (the SSRF guard) -- "ZZZ" is a well-formed path
// segment absent from the WSR-88D site table, representative of any
// non-allowlisted value an attacker could supply.
func TestRadarHandler_RejectsInvalidSite(t *testing.T) {
	called := false
	fake := &fakeRadarProxy{getFunc: func(context.Context, string, string) (json.RawMessage, radar.Metadata, error) {
		called = true
		return nil, radar.Metadata{}, nil
	}}

	srv := New(testDepsWithRadar(fake))

	req := httptest.NewRequest(http.MethodGet, "/api/radar/ZZZ", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/radar/ZZZ = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("proxy.Get was called for an invalid site -- the SSRF allowlist guard must reject before proxying")
	}
	if !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("body = %s, want a {\"error\":...} envelope", rec.Body.String())
	}
}

// TestRadarHandler_RejectsInvalidProduct proves a product value outside
// Contract A's {N0B, N0Q} allowlist is rejected with 400 before the proxy is
// ever called -- mirroring TestRadarHandler_RejectsInvalidSite's "not called"
// assertion for the site allowlist. "BOGUS" is a well-formed query value
// representative of any non-allowlisted product an attacker could supply.
func TestRadarHandler_RejectsInvalidProduct(t *testing.T) {
	called := false
	fake := &fakeRadarProxy{getFunc: func(context.Context, string, string) (json.RawMessage, radar.Metadata, error) {
		called = true
		return nil, radar.Metadata{}, nil
	}}

	srv := New(testDepsWithRadar(fake))

	req := httptest.NewRequest(http.MethodGet, "/api/radar/TLX?product=BOGUS", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/radar/TLX?product=BOGUS = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("proxy.Get was called for an invalid product -- the product allowlist guard must reject before proxying")
	}
	if want := `{"error":"invalid product"}` + "\n"; rec.Body.String() != want {
		t.Errorf("body = %s, want %s", rec.Body.String(), want)
	}
}

// TestRadarHandler_AcceptsValidProducts proves both Contract A product values
// reach the proxy unchanged, and that an omitted ?product still defaults to
// N0B end-to-end through the handler (TestRadarHandler_ServesGeoJSON only
// exercises an explicit ?product=N0B).
func TestRadarHandler_AcceptsValidProducts(t *testing.T) {
	tests := []struct {
		name        string
		rawQuery    string
		wantProduct string
	}{
		{"explicit N0Q", "?product=N0Q", "N0Q"},
		{"omitted product defaults to N0B", "", "N0B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotProduct string
			fake := &fakeRadarProxy{getFunc: func(_ context.Context, _, product string) (json.RawMessage, radar.Metadata, error) {
				gotProduct = product
				return json.RawMessage(`{"type":"FeatureCollection","features":[]}`), radar.Metadata{}, nil
			}}
			srv := New(testDepsWithRadar(fake))

			req := httptest.NewRequest(http.MethodGet, "/api/radar/TLX"+tt.rawQuery, nil)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
			}
			if gotProduct != tt.wantProduct {
				t.Errorf("proxy.Get product = %q, want %q", gotProduct, tt.wantProduct)
			}
		})
	}
}

// TestRadarHandler_ErrorMapping proves each Contract A sentinel maps to its
// Contract C status + error code.
func TestRadarHandler_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"no recent scan", radar.ErrNoRecentScan, http.StatusServiceUnavailable, `{"error":"no_recent_scan"}` + "\n"},
		{"decode failed", radar.ErrDecodeFailed, http.StatusBadGateway, `{"error":"decode_failed"}` + "\n"},
		{"internal", radar.ErrInternal, http.StatusBadGateway, `{"error":"internal"}` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeRadarProxy{getFunc: func(context.Context, string, string) (json.RawMessage, radar.Metadata, error) {
				return nil, radar.Metadata{}, tt.err
			}}
			srv := New(testDepsWithRadar(fake))

			req := httptest.NewRequest(http.MethodGet, "/api/radar/TLX", nil)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %s, want %s", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// TestRadarHandler_DisabledWhenNotEnabled proves that with Deps.Radar left
// nil (ENABLE_RADAR off in main.go), the route is never registered: the
// request falls through to registerStatic's catch-all, which 404s anything
// under the reserved /api/ prefix.
func TestRadarHandler_DisabledWhenNotEnabled(t *testing.T) {
	srv := New(testDepsWithRadar(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/radar/TLX", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/radar/TLX (radar disabled) = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}
