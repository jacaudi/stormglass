package httpserver

import (
	"net/http"

	"github.com/jacaudi/stormglass/internal/config"
)

// stationResponse is the wire shape for GET /api/station -- Contract C's
// StationMeta, served entirely from configuration.
//
// Every field is a pointer with `omitempty` so an unset value is ABSENT from
// the JSON rather than emitted as a zero. That is load-bearing, not
// pedantry: the UI's name fallback uses `??`, which does not fire on "", so
// an emitted empty name renders a blank heading; and its hasCoordinates
// guard accepts 0 as a finite number, so emitted zero coordinates would put
// every default deployment at 0.0000°N, 0.0000°E -- reintroducing from the
// other side exactly the failure that guard exists to prevent.
//
// Five fields the previous WeatherFlow passthrough declared are gone:
// station_id, device_id, timezone, firmware_revision and serial_number. Two
// are WeatherFlow-specific concepts with no tokenless source; the other
// three have sources but no consumer. No identified future consumer wants
// any of them -- NWS /points/{lat},{lon} takes no station identifier, key or
// account, and the radar card reads only latitude/longitude -- and re-adding
// one later is additive.
type stationResponse struct {
	Name      *string  `json:"name,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	Elevation *float64 `json:"elevation,omitempty"`
	RadarSite *string  `json:"radarSite,omitempty"`
}

// registerStation registers the station-identity endpoint. It is never
// gated: it backs no optional card of its own, the Header consumes it
// unconditionally, and with no configuration at all it correctly returns an
// empty object.
func registerStation(mux *http.ServeMux, deps Deps) {
	resp := stationResponseFrom(deps.Station)
	mux.HandleFunc("GET /api/station", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, resp)
	})
}

// stationResponseFrom maps the loaded configuration to the wire shape.
//
// The coordinate pairing rule is enforced here as well as in
// config.LoadStation, and that is not a duplicated rule: LoadStation treats
// a half-set pair as an operator ERROR and refuses to start, whereas this is
// the WIRE contract -- Deps can be constructed by any caller, and a lone
// latitude must never reach a client that would pair it with an implicit
// zero longitude.
func stationResponseFrom(cfg config.StationConfig) stationResponse {
	resp := stationResponse{
		Name:      cfg.Name,
		Elevation: cfg.Elevation,
		RadarSite: cfg.RadarSite,
	}
	if cfg.Latitude != nil && cfg.Longitude != nil {
		resp.Latitude, resp.Longitude = cfg.Latitude, cfg.Longitude
	}
	return resp
}
