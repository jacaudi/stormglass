package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"tempestwx-utilities/internal/tempestapi"
)

// testDepsWithWeatherFlow returns a Deps wired to wf (the WeatherFlow client
// or fake), with the same hermetic in-memory static filesystem the other
// httpserver tests use. Forecast and Almanac are enabled here because this
// helper exists to exercise the proxy handlers -- a test that wants them off
// sets the field back to false on the returned value.
func testDepsWithWeatherFlow(wf WeatherFlowProxy) Deps {
	return Deps{
		StaticFS:    fstest.MapFS{"index.html": {Data: []byte("<html>fake index</html>")}},
		WeatherFlow: wf,
		Forecast:    true,
		Almanac:     true,
	}
}

// TestProxy_InjectsBearerToken proves GET /api/forecast reaches WeatherFlow
// with the server-held token injected as `Authorization: Bearer`, while the
// browser-facing request itself carries no token at all.
func TestProxy_InjectsBearerToken(t *testing.T) {
	const token = "server-held-token"
	var gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[]}}`))
	}))
	defer upstream.Close()

	client := tempestapi.NewClient(token, tempestapi.WithBaseURL(upstream.URL))
	srv := New(testDepsWithWeatherFlow(client))

	// The browser-facing request carries no Authorization header at all --
	// it doesn't need one, since the token lives only on the server.
	req := httptest.NewRequest(http.MethodGet, "/api/forecast", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/forecast = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("upstream Authorization header = %q, want %q", gotAuth, "Bearer "+token)
	}
}

// TestProxy_NoTokenInResponseOrLogs proves the token never appears in the
// browser-facing response body nor in anything logged while serving it.
func TestProxy_NoTokenInResponseOrLogs(t *testing.T) {
	const token = "leaky-token-xyz"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[]}}`))
	}))
	defer upstream.Close()

	client := tempestapi.NewClient(token, tempestapi.WithBaseURL(upstream.URL))

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	srv := New(testDepsWithWeatherFlow(client))
	req := httptest.NewRequest(http.MethodGet, "/api/forecast", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/forecast = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Errorf("response body leaked the token: %s", rec.Body.String())
	}
	if strings.Contains(logBuf.String(), token) {
		t.Errorf("logs leaked the token: %s", logBuf.String())
	}
}

// TestProxy_ForecastAndAlmanac proves both /api/forecast and /api/almanac
// proxy WeatherFlow's better_forecast endpoint (v2/B3: proxy, not computed)
// and pass its JSON straight through, forwarding the browser's query params.
func TestProxy_ForecastAndAlmanac(t *testing.T) {
	var gotPaths, gotQueries []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[{"day":"Mon"}]}}`))
	}))
	defer upstream.Close()

	client := tempestapi.NewClient("tok", tempestapi.WithBaseURL(upstream.URL))
	srv := New(testDepsWithWeatherFlow(client))

	for _, ep := range []string{"/api/forecast", "/api/almanac"} {
		req := httptest.NewRequest(http.MethodGet, ep+"?station_id=42", nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200, body: %s", ep, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET %s Content-Type = %q, want application/json", ep, ct)
		}
		if !strings.Contains(rec.Body.String(), "Mon") {
			t.Errorf("GET %s body = %s, want upstream JSON passed through", ep, rec.Body.String())
		}
	}

	for _, p := range gotPaths {
		if p != "/better_forecast" {
			t.Errorf("upstream path = %q, want /better_forecast for both forecast and almanac", p)
		}
	}
	for _, q := range gotQueries {
		if q != "station_id=42" {
			t.Errorf("upstream query = %q, want station_id=42 forwarded from the browser request", q)
		}
	}
}

// TestProxy_NilWeatherFlow proves both proxy handlers 503 (not panic) when
// Deps.WeatherFlow is nil -- calling a method on a nil interface value
// panics with no dynamic type to dispatch to, and handleCurrentObservation
// already guards its own nil ObservationReader dependency the same way, but
// registerProxy had no equivalent guard before this fix (SGE review M7).
// /api/station is excluded here: it is served from config, not proxied, and
// correctly returns 200 with no upstream at all.
func TestProxy_NilWeatherFlow(t *testing.T) {
	srv := New(testDepsWithWeatherFlow(nil))

	for _, ep := range []string{"/api/forecast", "/api/almanac"} {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s (nil WeatherFlow) = %d, want 503", ep, rec.Code)
		}
	}
}

// TestProxy_DisabledRoutesAreNotRegistered proves a disabled feature 404s
// rather than proxying: registerProxy skips the route, and the reserved
// /api/ prefix in registerStatic turns the miss into a 404 (server.go:161).
// /api/station is unaffected -- it is not gated and backs no optional card.
func TestProxy_DisabledRoutesAreNotRegistered(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[]}}`))
	}))
	defer upstream.Close()

	deps := testDepsWithWeatherFlow(tempestapi.NewClient("tok", tempestapi.WithBaseURL(upstream.URL)))
	deps.Forecast = false
	deps.Almanac = false
	srv := New(deps)

	for _, ep := range []string{"/api/forecast", "/api/almanac"} {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ep, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s (disabled) = %d, want 404", ep, rec.Code)
		}
	}

	if upstreamCalled {
		t.Error("a disabled route reached WeatherFlow; it must not make an upstream call")
	}

	// /api/station is registered by registerStation, not registerProxy, and
	// is served entirely from config -- it returns 200 regardless of the
	// Forecast/Almanac flags and with no WeatherFlow upstream involved at all.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/station", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/station = %d, want 200 -- station is served from config, not proxied", rec.Code)
	}
}

// TestProxy_EnabledRoutesIndependently proves the two flags are independent:
// enabling only one must not register the other.
func TestProxy_EnabledRoutesIndependently(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[]}}`))
	}))
	defer upstream.Close()

	deps := testDepsWithWeatherFlow(tempestapi.NewClient("tok", tempestapi.WithBaseURL(upstream.URL)))
	deps.Forecast = true
	deps.Almanac = false
	srv := New(deps)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/forecast", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/forecast (enabled) = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/almanac", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/almanac (disabled) = %d, want 404", rec.Code)
	}
}
