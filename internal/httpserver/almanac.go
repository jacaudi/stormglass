package httpserver

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/jacaudi/stormglass/internal/astro"
	"github.com/jacaudi/stormglass/internal/config"
	"github.com/jacaudi/stormglass/internal/sqlite"
)

// tempRecord is one record column: an extreme and a human label for when it
// occurred. Every field is nullable, and a value and its label are always
// both null or both set -- an empty window (a freshly provisioned appliance
// has no year of history) must render an em-dash, not NaN.
type tempRecord struct {
	High     *float64 `json:"high"`
	HighDate *string  `json:"highDate"`
	Low      *float64 `json:"low"`
	LowDate  *string  `json:"lowDate"`
}

// stationAlmanac is the wire shape for GET /api/almanac -- Contract C's
// StationAlmanac, pinned to web/src/types/weather.ts field-for-field.
//
// sunrise/sunset are PREFORMATTED station-local clock times ("5:47 AM"),
// not epochs, and daylightMinutes carries the derived quantity separately.
// The reason is the same one that makes highDate a server-rendered string:
// the browser's timezone is the VIEWER's, not the station's, and
// STATION_TIMEZONE is deliberately not on the wire. Shipping the epochs as
// well would leave a timezone-naive value for a future edit to render
// wrongly.
type stationAlmanac struct {
	Today            tempRecord `json:"today"`
	Week             tempRecord `json:"week"`
	Month            tempRecord `json:"month"`
	Year             tempRecord `json:"year"`
	Sunrise          *string    `json:"sunrise"`
	Sunset           *string    `json:"sunset"`
	DaylightMinutes  *int64     `json:"daylightMinutes"`
	MoonPhase        float64    `json:"moonPhase"`
	MoonPhaseName    string     `json:"moonPhaseName"`
	MoonIllumination float64    `json:"moonIllumination"`
}

// registerAlmanac registers the computed almanac endpoint when it is
// enabled. Leaving the route unregistered -- rather than registering it and
// refusing inside the handler -- mirrors registerRadar and makes a disabled
// feature incapable of running eight table scans.
func registerAlmanac(mux *http.ServeMux, deps Deps) {
	if !deps.Almanac {
		return
	}
	mux.HandleFunc("GET /api/almanac", func(w http.ResponseWriter, r *http.Request) {
		handleAlmanac(w, r, deps.Observations, deps.Station)
	})
}

func handleAlmanac(w http.ResponseWriter, r *http.Request, reader ObservationReader, station config.StationConfig) {
	if reader == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "observation store not configured")
		return
	}
	loc := station.Location
	if loc == nil {
		loc = time.UTC
	}

	now := time.Now()
	today, week, month, year := almanacWindows(now, loc)

	// The four calls are eight statements, and idx_obs_time leads on
	// timestamp so it cannot serve a temperature extreme -- the year window
	// (~525k rows at a 1/minute cadence) is a scan. Bounded with the same
	// constant and for the same reason handleSummary bounds its single
	// aggregate; dropping that convention for an endpoint that scans eight
	// times as much would be a regression.
	ctx, cancel := context.WithTimeout(r.Context(), summaryQueryTimeout)
	defer cancel()

	resp := stationAlmanac{}
	for _, slot := range []struct {
		window almanacWindow
		out    *tempRecord
	}{
		{today, &resp.Today}, {week, &resp.Week}, {month, &resp.Month}, {year, &resp.Year},
	} {
		te, err := reader.TemperatureExtremes(ctx, slot.window.From, slot.window.To)
		if err != nil {
			slog.ErrorContext(ctx, "httpserver: temperature extremes", "from", slot.window.From, "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to load station almanac")
			return
		}
		*slot.out = toTempRecord(te, now, loc)
	}

	if station.Latitude != nil && station.Longitude != nil {
		// The station's own local day, not the UTC day.
		rise, set := astro.SunriseSunset(*station.Latitude, *station.Longitude, now.In(loc))
		resp.Sunrise = almanacClock(rise, loc)
		resp.Sunset = almanacClock(set, loc)
		if rise != nil && set != nil {
			// Either bound may independently be nil near the polar boundary,
			// so this is deliberately gated on BOTH.
			minutes := int64(set.Sub(*rise).Round(time.Minute) / time.Minute)
			resp.DaylightMinutes = &minutes
		}
	}

	resp.MoonPhase, resp.MoonPhaseName, resp.MoonIllumination = astro.MoonPhase(now)

	writeJSON(w, http.StatusOK, resp)
}

// toTempRecord maps one window's store result to its wire record. The value
// and its label come from the same sql.Null* pair, so they can never
// disagree.
func toTempRecord(te sqlite.TempExtremes, now time.Time, loc *time.Location) tempRecord {
	return tempRecord{
		High:     f64(te.Max),
		HighDate: almanacDateLabel(te.MaxAt, now, loc),
		Low:      f64(te.Min),
		LowDate:  almanacDateLabel(te.MinAt, now, loc),
	}
}

// almanacDateLabel renders an extreme's timestamp as the card's label:
// "Today" when it falls on the station's current local date, else "Jan 2"
// (e.g. "Feb 15"). Formatted server-side because only the server knows
// STATION_TIMEZONE -- a Colorado station viewed from Tokyo would otherwise
// label the July 4th high "Jul 5" and break the "Today" test outright.
//
// The today column's labels are "Today" in every ordinary case, and that is
// accepted rather than special-cased: the alternative is a second code path
// for one column. The exception is a midnight-DST zone on its transition
// date, where almanacWindows' local midnight resolves an hour early into the
// previous local day (see its doc comment); an extreme falling in that hour
// is correctly labelled with the previous date while sitting in the today
// column. Rare, correct for what the label says, and not worth a special
// case.
func almanacDateLabel(at sql.NullInt64, now time.Time, loc *time.Location) *string {
	if !at.Valid {
		return nil
	}
	when := time.Unix(at.Int64, 0).In(loc)
	ny, nm, nd := now.In(loc).Date()
	wy, wm, wd := when.Date()

	label := when.Format("Jan 2")
	if ny == wy && nm == wm && nd == wd {
		label = "Today"
	}
	return &label
}

// almanacClock renders an absolute instant as a station-local 12-hour clock
// time ("5:47 AM"), or nil for an event that does not occur.
func almanacClock(at *time.Time, loc *time.Location) *string {
	if at == nil {
		return nil
	}
	s := at.In(loc).Format("3:04 PM")
	return &s
}
