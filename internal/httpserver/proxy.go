package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
)

// weatherFlowForecastPath is the single WeatherFlow REST path both
// /api/forecast and /api/almanac proxy (v2/B3: almanac is a passthrough
// proxy of better_forecast, not a computed shape -- see design §7/§11).
// Single-sourced here so the two registrations below can't drift apart.
const weatherFlowForecastPath = "/better_forecast"

// WeatherFlowProxy is the read-side dependency registerProxy needs, defined
// at the consumer site per go-standards §12: production wires
// *tempestapi.Client (which satisfies it, once Task 1.6 constructs one from
// the server-held TOKEN), tests use a fake or a *tempestapi.Client pointed
// at an httptest.Server via tempestapi.WithBaseURL.
type WeatherFlowProxy interface {
	// Proxy GETs path (plus query) against WeatherFlow with the server-held
	// Bearer token already injected, returning the upstream body,
	// Content-Type, and status unchanged (passthrough).
	Proxy(ctx context.Context, path string, query url.Values) (body []byte, contentType string, status int, err error)
}

// registerProxy registers the remaining WeatherFlow passthrough proxy
// handlers (/api/forecast and /api/almanac). Additive per the Deps seam
// server.go documents: a new field (WeatherFlow) plus this one register
// call, siblings (registerHealthz, registerObservations, registerStatic)
// untouched. The browser never sends or needs a token for either route -- it
// lives only in deps.WeatherFlow.
func registerProxy(mux *http.ServeMux, deps Deps) {
	// Forecast and almanac are opt-in (issue #145). Leaving the route
	// unregistered -- rather than registering it and refusing inside the
	// handler -- mirrors registerRadar, and makes a disabled feature
	// incapable of making an upstream WeatherFlow call at all.
	if deps.Forecast {
		mux.HandleFunc("GET /api/forecast", func(w http.ResponseWriter, r *http.Request) {
			proxyWeatherFlow(w, r, deps.WeatherFlow, weatherFlowForecastPath)
		})
	}
}

// proxyWeatherFlow calls wf.Proxy for path, forwarding the browser request's
// query params (e.g. station_id) and writing the upstream body straight
// through with the upstream Content-Type and status -- the "how a
// passthrough proxy response is written" knowledge all three handlers above
// share, written once here. It is a passthrough only: the upstream JSON is
// not reshaped into any UI-side contract by this handler (see task report
// for the flagged concern about whether WeatherFlow's envelope already
// matches the UI's expected shape).
func proxyWeatherFlow(w http.ResponseWriter, r *http.Request, wf WeatherFlowProxy, path string) {
	if wf == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "weatherflow client not configured")
		return
	}

	ctx := r.Context()

	body, contentType, status, err := wf.Proxy(ctx, path, r.URL.Query())
	if err != nil {
		slog.ErrorContext(ctx, "httpserver: weatherflow proxy", "path", path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "weatherflow upstream request failed")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	// body is WeatherFlow's own JSON response, not browser-supplied input --
	// gosec's G705 taint check flags any external-response bytes reaching an
	// http.ResponseWriter, but this is the intended passthrough proxy (design
	// §7/§11), and server.go's securityHeaders sets X-Content-Type-Options:
	// nosniff plus a self-only CSP on every response, so the browser cannot
	// execute this body as HTML/script even if contentType were unexpected.
	if _, err := w.Write(body); err != nil { //nolint:gosec // G705: see comment above
		slog.ErrorContext(ctx, "httpserver: write weatherflow proxy response", "error", err)
	}
}
