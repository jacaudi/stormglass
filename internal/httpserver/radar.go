package httpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jacaudi/stormglass/internal/radar"
)

// defaultRadarProduct is the WSR-88D product requested when the browser
// omits ?product -- N0B (base reflectivity, low-level scan) is the default
// radar.Proxy.Get itself falls back from to N0Q on no_recent_scan (see
// internal/radar/proxy.go's Get doc comment).
const defaultRadarProduct = "N0B"

// RadarProxy is the read-side dependency registerRadar needs, defined at the
// consumer site per go-standards §12: production wires *radar.Proxy (which
// satisfies it), tests use a fake.
type RadarProxy interface {
	// Get returns the GeoJSON FeatureCollection body and its metadata for
	// (site, product); see radar.Proxy.Get for caching/fallback behavior.
	Get(ctx context.Context, site, product string) (json.RawMessage, radar.Metadata, error)
}

// registerRadar registers GET /api/radar/{site} ONLY when deps.Radar is
// non-nil -- radar is an opt-in feature (main.go sets deps.Radar only when
// ENABLE_RADAR=true). Unlike registerObservations/registerProxy (which
// register unconditionally and 503 on a nil dependency), leaving the route
// unregistered here means a request to /api/radar/{site} falls through to
// registerStatic's catch-all, which 404s anything under the reserved /api/
// prefix -- the opt-in gate is the nil-guard itself, with no separate enable
// bool inside this package.
func registerRadar(mux *http.ServeMux, deps Deps) {
	if deps.Radar == nil {
		return
	}
	mux.HandleFunc("GET /api/radar/{site}", func(w http.ResponseWriter, r *http.Request) {
		handleRadar(w, r, deps.Radar)
	})
}

// handleRadar validates {site} against radar.IsValidSite's allowlist (the
// SSRF guard -- must run before any proxy call) and ?product against
// Contract A's {N0B, N0Q} allowlist (same must-run-before-any-proxy-call
// rule -- an unvalidated product would forward to the proxy's S3 prefix
// unchecked, and would break the proxy's literal "N0B" no_recent_scan
// fallback check for any other spelling), then serves the GeoJSON body
// radarProxy.Get returns, mapping its Contract A sentinel errors to
// Contract C's status codes and short error codes.
func handleRadar(w http.ResponseWriter, r *http.Request, radarProxy RadarProxy) {
	site := r.PathValue("site")
	if !radar.IsValidSite(site) {
		writeJSONError(w, http.StatusBadRequest, "invalid site")
		return
	}

	product := cmp.Or(r.URL.Query().Get("product"), defaultRadarProduct)
	switch product {
	case defaultRadarProduct, "N0Q":
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid product")
		return
	}

	ctx := r.Context()

	body, _, err := radarProxy.Get(ctx, site, product)
	if err != nil {
		writeRadarError(w, r, site, product, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// body is the sidecar's own GeoJSON response, not browser-supplied
	// input, so writing it through unescaped is not an XSS/injection risk.
	if _, err := w.Write(body); err != nil { //nolint:gosec // G705: see comment above
		slog.ErrorContext(ctx, "httpserver: write radar proxy response", "error", err)
	}
}

// writeRadarError maps a radar.Proxy error to Contract C's status + short
// error code, logging the underlying error server-side without leaking it to
// the client body. Every branch logs identically -- the same "a radar proxy
// call failed" knowledge -- so the log call is hoisted above the switch
// rather than repeated per case.
func writeRadarError(w http.ResponseWriter, r *http.Request, site, product string, err error) {
	slog.ErrorContext(r.Context(), "httpserver: radar proxy", "site", site, "product", product, "error", err)

	status, code := http.StatusBadGateway, "internal"
	switch {
	case errors.Is(err, radar.ErrNoRecentScan):
		status, code = http.StatusServiceUnavailable, "no_recent_scan"
	case errors.Is(err, radar.ErrDecodeFailed):
		status, code = http.StatusBadGateway, "decode_failed"
	}
	writeJSONError(w, status, code)
}
