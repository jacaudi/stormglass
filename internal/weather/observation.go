// Package weather holds the store-neutral representation of a weather
// observation and of a hole in an observation series.
//
// It deliberately imports nothing else in this repository. Both store
// packages (internal/sqlite, internal/postgres) and the REST client
// (internal/tempestapi) name these types, so any home that imported one of
// them would create a cycle. It is not the UDP wire protocol — that is
// internal/tempestudp — and these types must not be added there.
package weather

import "time"

// Observation is one stormglass_observations row in store-neutral form.
//
// Every measurement is a *float64 because the SQLite and Postgres DDL declare
// every column except id/serial_number/timestamp as nullable, and the Tempest
// REST API may return JSON null for any element of an obs tuple. A nil here
// means SQL NULL; database/sql and pgx both bind it that way directly.
//
// This is deliberately NOT the stores' private observationRow types, whose
// leading fields are non-pointer float64: unmarshalling a JSON null into a
// non-pointer numeric is a silent no-op that yields 0.0, which would write
// "pressure = 0.0 mb" where the API said "unknown". See the design's
// "Nullability — mandatory" section.
type Observation struct {
	// SerialNumber and Timestamp are the series key and are never NULL.
	// An observation whose ob[0] is null cannot be keyed and is dropped
	// at decode time rather than represented here.
	SerialNumber string
	Timestamp    time.Time

	WindLull             *float64 // obs_st[1]
	WindAvg              *float64 // obs_st[2]
	WindGust             *float64 // obs_st[3]
	WindDirection        *float64 // obs_st[4]
	WindSampleInterval   *float64 // obs_st[5]
	Pressure             *float64 // obs_st[6], raw mb (no conversion)
	TempAir              *float64 // obs_st[7]
	Humidity             *float64 // obs_st[8]
	Illuminance          *float64 // obs_st[9]
	UVIndex              *float64 // obs_st[10]
	Irradiance           *float64 // obs_st[11]
	RainRate             *float64 // obs_st[12]
	PrecipType           *float64 // obs_st[13]
	LightningDistance    *float64 // obs_st[14]
	LightningStrikeCount *float64 // obs_st[15]
	Battery              *float64 // obs_st[16]
	ReportInterval       *float64 // obs_st[17]
}

// NOTE: there is deliberately no TempWetbulb field. The API does not return
// wet bulb; each store derives it at its own insert boundary from
// TempAir/Humidity/Pressure using tempestudp.WetBulbTemperatureC. A field
// here would never be read by any code path, and setting it would silently
// do nothing — dead code that looks load-bearing.

// Gap is a CLOSED hole [From, To] in one station's observation series.
//
// Closed, not half-open: every producer emits endpoints that are rows which
// already exist (prev/next from LAG, First/Last from SeriesBounds), so both
// ends get re-fetched and re-offered to the store. ON CONFLICT DO NOTHING
// absorbs the duplicates, so this costs nothing but a slightly inflated
// Returned count — but the doc must not claim a half-open interval it does
// not have.
//
// The series is keyed by (SerialNumber, Timestamp) — the same uniqueness
// contract idempotent inserts rely on — so a Gap is meaningless without its
// serial.
type Gap struct {
	SerialNumber string
	From         time.Time
	To           time.Time
}

// Duration is the width of the gap.
func (g Gap) Duration() time.Duration { return g.To.Sub(g.From) }

// Bounds is the first and last observation timestamp held for one serial
// within a queried window. It is what lets the caller find head and tail
// gaps, which a SQL LAG window function cannot see: LAG yields NULL for the
// first row of each partition, so it finds interior gaps only.
type Bounds struct {
	SerialNumber string
	First        time.Time
	Last         time.Time
}
