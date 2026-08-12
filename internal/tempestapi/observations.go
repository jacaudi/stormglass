package tempestapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"tempestwx-utilities/internal/weather"
)

// observationSet mirrors the Tempest ObservationSet response schema.
//
// Obs is [][]*float64, not [][]float64: unmarshalling a JSON null into a
// non-pointer numeric is a silent no-op that yields 0.0, which would write a
// physically meaningful "pressure = 0.0 mb" where the API said "unknown".
// Backfill operates precisely on marginal windows, where nulls are most
// likely. Do not "simplify" this to [][]float64 and do not reuse
// tempestudp.TempestObservationReport, whose Obs is [][]float64.
//
// Every field is optional. The published OpenAPI schema declares no `required`
// array for ObservationSet, so a response may legitimately omit both `type`
// and `obs` — that is an empty window, not an error.
type observationSet struct {
	statusEnvelope
	Obs [][]*float64 `json:"obs"`
}

// obs_st tuple indices. Indices 0-17 match the UDP layout exactly, so the
// same field semantics apply. The REST array carries 22 elements; 18-21
// (local-day rain accumulation, Nearcast accumulations, precip analysis type)
// map to no column in tempest_observations and are ignored deliberately.
const (
	obsTimestamp = iota
	obsWindLull
	obsWindAvg
	obsWindGust
	obsWindDirection
	obsWindSampleInterval
	obsPressure
	obsTempAir
	obsHumidity
	obsIlluminance
	obsUVIndex
	obsIrradiance
	obsRainRate
	obsPrecipType
	obsLightningDistance
	obsLightningStrikeCount
	obsBattery
	obsReportInterval
)

// obsMinFields is the floor below which a tuple carries no usable core
// measurements. It mirrors the UDP ingest path, which accepts a report at
// len(ob) >= 13 and fills the tail conditionally (sqlite/writer.go:406-457).
//
// The guards here are GRADUATED, not all-or-nothing. A hard
// "len(ob) < 18 -> drop" would silently discard a short tuple's temperature,
// pressure and wind — a real divergence from the ingest path the design
// forbids. Because every weather.Observation measurement is a *float64,
// honoring the graduated rule is free: absent indices simply stay nil.
//
// The floor mirrors the UDP path exactly, but the tail does not: at() fills
// obsLightningDistance (14) and obsLightningStrikeCount (15) independently,
// where sqlite/writer.go's handleObservationReport gates that pair together
// on len(ob) >= 16. A 15-element tuple therefore keeps a lightning distance
// here that the UDP path would discard (it would keep neither). This is a
// deliberate, accepted divergence — not a bug to fix by changing behavior —
// because the graduated rule this file follows is "preserve what is present."
const obsMinFields = 13

// Observations fetches raw historical observations for one device over
// [start, end] and returns them in store-neutral form.
//
// It is deliberately separate from GetObservations, which returns
// []prometheus.Metric for ModeAPIExport and routes through
// tempestudp.ParseReport. ParseReport dispatches on the top-level `type` and
// fails with `unhandled message type: ""` on a status-only envelope, which
// would turn a legitimate empty window into a hard error.
//
// Derived columns are NOT computed here — this returns raw API fields only.
// temp_wetbulb is derived at the store boundary so backfilled and live rows
// stay indistinguishable.
func (c *Client) Observations(ctx context.Context, station Station, start, end time.Time) ([]weather.Observation, error) {
	url := fmt.Sprintf("%s/observations/device/%d?time_start=%d&time_end=%d",
		c.baseURL, station.DeviceID, start.Unix(), end.Unix())

	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}

	var set observationSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decode observation set: %w", err)
	}

	// A non-zero status_code is a real, non-transient API failure. A zero
	// status_code with an absent/null/empty obs is an empty window: zero
	// rows, no error.
	if err := set.err(); err != nil {
		return nil, err
	}

	// at returns ob[i] when the tuple is long enough, nil otherwise. This is
	// the graduated guard: a short tuple keeps its core measurements and gets
	// NULL for the indices it does not carry.
	at := func(ob []*float64, i int) *float64 {
		if i < len(ob) {
			return ob[i]
		}
		return nil
	}

	out := make([]weather.Observation, 0, len(set.Obs))
	dropped := 0
	for _, ob := range set.Obs {
		if len(ob) < obsMinFields || ob[obsTimestamp] == nil {
			// Below the floor, or no timestamp: the row cannot be keyed by
			// (serial_number, timestamp), so writing it would create an
			// un-dedupable row at some arbitrary instant. Drop it — and count
			// it, so an all-malformed window is distinguishable from a
			// permanent hole.
			dropped++
			continue
		}
		out = append(out, weather.Observation{
			SerialNumber:         station.SerialNumber,
			Timestamp:            time.Unix(int64(*ob[obsTimestamp]), 0).UTC(),
			WindLull:             at(ob, obsWindLull),
			WindAvg:              at(ob, obsWindAvg),
			WindGust:             at(ob, obsWindGust),
			WindDirection:        at(ob, obsWindDirection),
			WindSampleInterval:   at(ob, obsWindSampleInterval),
			Pressure:             at(ob, obsPressure),
			TempAir:              at(ob, obsTempAir),
			Humidity:             at(ob, obsHumidity),
			Illuminance:          at(ob, obsIlluminance),
			UVIndex:              at(ob, obsUVIndex),
			Irradiance:           at(ob, obsIrradiance),
			RainRate:             at(ob, obsRainRate),
			PrecipType:           at(ob, obsPrecipType),
			LightningDistance:    at(ob, obsLightningDistance),
			LightningStrikeCount: at(ob, obsLightningStrikeCount),
			Battery:              at(ob, obsBattery),
			ReportInterval:       at(ob, obsReportInterval),
		})
	}

	if dropped > 0 {
		// WARN, not silence: without this, a window whose tuples were all
		// malformed reports zero rows — byte-identical to the permanent-hole
		// signal the reporting design rests on.
		slog.Warn("tempestapi: dropped malformed observation tuples",
			"serial", station.SerialNumber, "dropped", dropped, "total", len(set.Obs),
			"start", start.UTC(), "end", end.UTC())
	}
	return out, nil
}
