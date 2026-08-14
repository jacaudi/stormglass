// Package httpserver builds the HTTP server that serves the embedded React
// UI (from tempestwx-utilities/web) alongside the JSON API added by later
// tasks (observations from SQLite, WeatherFlow proxy — see Deps).
package httpserver

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"

	"tempestwx-utilities/internal/config"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second

	// apiPrefix is reserved for the JSON API handlers Tasks 1.4/1.5 register.
	// Requests under this prefix never fall through to the SPA fallback, even
	// before those handlers exist, so the namespace boundary holds from day one.
	apiPrefix = "/api/"

	indexPage = "index.html"
)

// cspPolicy is the self-only Content-Security-Policy for the UI: every
// resource (scripts, styles, images, connections) must come from the same
// origin the page was served from. No external host is ever added here —
// the OSM basemap is served same-origin as a static asset (Task 2.6).
const cspPolicy = "default-src 'self'; img-src 'self' data: blob:; worker-src 'self' blob:; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'self'"

// Deps carries the dependencies New's handlers need. It is the seam future
// tasks extend: Task 1.4 adds a SQLite reader field + a registerObservations
// call in New; Task 1.5 adds a WeatherFlow client field + its own register
// call. Adding a field and a register call is additive — it does not require
// touching the handlers already registered here.
type Deps struct {
	// StaticFS serves the embedded UI build (web.DistFS() in production; an
	// in-memory fstest.MapFS in tests, so tests never need a real UI build).
	StaticFS fs.FS

	// Observations backs GET /api/observations/current and .../history
	// (Task 1.4). Production passes a *sqlite.Writer (which satisfies
	// ObservationReader); tests pass a fake.
	Observations ObservationReader

	// Station is the operator-supplied station identity backing
	// GET /api/station, and the source of the coordinates and timezone
	// GET /api/almanac computes from. It is plain configuration, not a
	// backing service: the store holds no coordinates and no UDP message
	// carries any. main fills it from config.LoadStation.
	Station config.StationConfig

	// WeatherFlow backs GET /api/forecast and /api/almanac
	// (Task 1.5). Production passes a *tempestapi.Client constructed from
	// the server-held TOKEN (which satisfies WeatherFlowProxy); tests pass a
	// fake or a *tempestapi.Client pointed at an httptest.Server.
	WeatherFlow WeatherFlowProxy

	// Radar backs GET /api/radar/{site} (Task 2.5). Left nil disables the
	// route entirely (main.go sets it only when ENABLE_RADAR=true) -- see
	// registerRadar's doc comment for why this differs from Observations/
	// WeatherFlow's "register unconditionally, 503 on nil" pattern.
	// Production wires *radar.Proxy (which satisfies RadarProxy); tests pass
	// a fake.
	Radar RadarProxy

	// Forecast and Almanac gate GET /api/forecast and GET /api/almanac, and
	// the matching entries in GET /api/capabilities. main.go sets them from
	// ENABLE_FORECAST / ENABLE_ALMANAC; both default to false, so a server
	// built from a zero Deps serves neither route (issue #145).
	Forecast bool
	Almanac  bool

	// TracerProvider is the otelhttp middleware's span source (Task 1.6).
	// Nil means "use the OTel global TracerProvider" -- otelhttp.WithTracerProvider
	// already treats a nil provider as a no-op, falling back to
	// otel.GetTracerProvider() internally, so New does not need its own
	// nil-check. Production leaves this nil (main wires OTel globally via
	// internal/otel.Setup, Task 6.4); tests inject a
	// tracetest.SpanRecorder-backed provider for hermetic span assertions.
	TracerProvider trace.TracerProvider
}

// New builds the HTTP server: the embedded UI with SPA fallback, security
// headers, /healthz, explicit timeouts, and a mux ready for later tasks to
// register /api/* handlers onto. The caller owns starting and stopping it
// (Serve/ListenAndServe, then Shutdown(ctx) for graceful shutdown).
func New(deps Deps) *http.Server {
	mux := http.NewServeMux()
	registerHealthz(mux)
	registerCapabilities(mux, newCapabilities(deps))
	registerObservations(mux, deps)
	registerStation(mux, deps)
	registerAlmanac(mux, deps)
	registerProxy(mux, deps)
	registerRadar(mux, deps)
	registerStatic(mux, deps)

	// The "http.server" operation argument no longer names the span: since
	// otelhttp v0.69.0 the default span name formatter discards it and uses the
	// HTTP semantic convention "{method} {route}" instead, so spans arrive as
	// "GET /healthz". The argument is still required by NewHandler.
	handler := otelhttp.NewHandler(securityHeaders(mux), "http.server", otelhttp.WithTracerProvider(deps.TracerProvider))

	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// securityHeaders wraps next with the response headers required for the UI:
// a self-only CSP, MIME-sniffing protection, and no referrer leakage.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", cspPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// isRegularFile reports whether name Opens successfully in fsys AND is not a
// directory. Open("assets") succeeds for a directory just as it does for a
// file, so a directory path (e.g. GET /assets/) would otherwise fall into
// the "serve as an immutable static asset" branch and let
// http.ServeFileFS auto-generate a directory listing (SGE review M2) -- a
// directory must instead be treated like a missing asset and fall through
// to the SPA/404 branch below.
func isRegularFile(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() {
		_ = f.Close()
	}()
	info, err := f.Stat()
	return err == nil && !info.IsDir()
}

// registerHealthz registers the liveness endpoint used by container
// orchestration to confirm the process is up.
func registerHealthz(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			slog.ErrorContext(r.Context(), "httpserver: healthz write response", "error", err)
		}
	})
}

// registerStatic registers the catch-all handler that serves the embedded
// UI, with SPA fallback for client-side routes and a reserved /api/
// namespace for future JSON API handlers.
//
// Rules:
//   - /api/* never falls back to the SPA index (reserved for Tasks 1.4/1.5).
//   - A path that resolves to a real static file is served with an immutable
//     cache-control header (content-hashed assets never change in place).
//   - A missing path with a file extension (a static asset request) 404s —
//     it must not silently serve index.html for a stale/renamed asset URL.
//   - A missing path without an extension (a client-side route) serves
//     index.html for the SPA router to handle, uncached.
func registerStatic(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, apiPrefix) {
			http.NotFound(w, r)
			return
		}

		assetPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if assetPath == "" || assetPath == "." {
			assetPath = indexPage
		}

		if assetPath != indexPage && fs.ValidPath(assetPath) {
			if isRegularFile(deps.StaticFS, assetPath) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				http.ServeFileFS(w, r, deps.StaticFS, assetPath)
				return
			}
			if path.Ext(assetPath) != "" {
				http.NotFound(w, r)
				return
			}
		}

		// SPA fallback: explicitly no-cache, so a redeploy with new hashed
		// asset names can't be defeated by a heuristically-cached index
		// (SGE review M3).
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, deps.StaticFS, indexPage)
	})
}
