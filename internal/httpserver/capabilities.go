package httpserver

import "net/http"

// capabilities is the GET /api/capabilities document: which optional UI
// features this server has enabled. The UI fetches it once and mounts only
// the cards whose capability is true, so a disabled feature leaves nothing in
// the DOM rather than an empty shell (issue #145).
//
// The three JSON key names are an external contract shared by hand with
// web/src/types/weather.ts. capabilities_fixture_test.go pins both sides to a
// single committed fixture; issue #149 tracks generating one side from the
// other across all of Contract C.
type capabilities struct {
	Forecast bool `json:"forecast"`
	Radar    bool `json:"radar"`
	Almanac  bool `json:"almanac"`
}

// newCapabilities derives the document from deps. Radar is derived from
// deps.Radar != nil rather than carried as its own bool: that field is
// already the single source of the radar opt-in (registerRadar reads the same
// one), so a parallel bool could drift from the route it describes.
func newCapabilities(deps Deps) capabilities {
	return capabilities{
		// Constant false: the forecast card's only provider was the
		// WeatherFlow proxy, which is deleted, and a 7-day forecast is the
		// one thing that genuinely cannot come from UDP or the local store.
		// Issue #81 restores this alongside a tokenless NWS provider; the
		// key stays on the wire so the UI contract does not churn.
		Forecast: false,
		Radar:    deps.Radar != nil,
		Almanac:  deps.Almanac,
	}
}

// registerCapabilities registers the capability document. Unlike the feature
// routes it gates, this one is always registered and has no failure mode --
// it marshals a three-field struct with no dependency to consult.
//
// Cache-Control is set before writeJSON because writeJSON calls WriteHeader
// immediately (observations.go:436-438); setting it afterwards would be a
// silent no-op. no-store, not a max-age, so an operator's config change shows
// up on the next page load rather than after a cache expiry.
func registerCapabilities(mux *http.ServeMux, caps capabilities) {
	mux.HandleFunc("GET /api/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, caps)
	})
}
