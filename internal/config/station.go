package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	// time/tzdata embeds the IANA database in the binary (~450 KB) so
	// time.LoadLocation works regardless of what the runtime image ships.
	// The pinned cgr.dev/chainguard/static digest does carry tzdata today
	// (tzdata=2026c-r0, 1242 zoneinfo files), but that is a dependency this
	// build does not control -- a Renovate bump of the base digest must not
	// be able to turn a non-UTC STATION_TIMEZONE into a startup failure.
	_ "time/tzdata"
)

// StationConfig is the operator-supplied station identity. The store holds
// no coordinates and no UDP message carries any (the complete station
// metadata on the wire is serial_number, hub_sn and firmware_revision), so
// identity is configuration -- 12-Factor III.
//
// Every serialised field is a pointer because "unset" must be
// distinguishable from a legitimate zero: sea level, the equator and the
// prime meridian are all real values that a value-typed field with
// `omitempty` would silently drop. Location is a pointer for an unrelated,
// purely idiomatic reason -- time.LoadLocation returns one and time.UTC IS
// one -- and is never nil and never serialised.
type StationConfig struct {
	Name      *string
	Latitude  *float64
	Longitude *float64
	Elevation *float64
	RadarSite *string
	Location  *time.Location
}

// Coordinate bounds. Elevation is unbounded in metres (the Dead Sea shore is
// about -430, and a balloon-borne station is not this appliance's problem);
// it is still required to be finite.
const (
	minLatitude  = -90
	maxLatitude  = 90
	minLongitude = -180
	maxLongitude = 180
)

// Elevation has no meaningful range bound in metres, so parseFloatEnv is
// called with the widest possible one; the finiteness check inside it is what
// actually rejects bad input.
const (
	minElevationM = -math.MaxFloat64
	maxElevationM = math.MaxFloat64
)

// LoadStation decodes the station identity from the environment.
//
// No value is ever required: an unset variable yields a nil pointer and no
// error, and no combination of absent values can prevent the process from
// starting. A MALFORMED value -- unparseable, out of range, non-finite, an
// unknown timezone, or a half-set coordinate pair -- is a fatal
// configuration error, matching ParseBoolEnv's stance that a typo is an
// operator error rather than a silently disabled feature.
//
// EVERY malformed value is reported in one aggregated error, per
// go-standards §15.3: with six variables, failing on the first would cost an
// operator six restart cycles.
//
// LoadStation deliberately does not read feature flags. RADAR_SITE is
// decoded here unconditionally; main clears it when ENABLE_RADAR is false,
// so flag interpretation stays in one place and this stays a pure decoder.
func LoadStation() (StationConfig, error) {
	cfg := StationConfig{Location: time.UTC}
	var errs []error

	if v := os.Getenv("STATION_NAME"); v != "" {
		name := v
		cfg.Name = &name
	}
	if v := os.Getenv("RADAR_SITE"); v != "" {
		site := v
		cfg.RadarSite = &site
	}

	// Each parse error is captured in its own variable rather than a shared
	// err: the pairing check below must know whether EITHER coordinate was
	// rejected, and a reused err would only carry the most recent one.
	lat, latErr := parseFloatEnv("STATION_LATITUDE", minLatitude, maxLatitude)
	if latErr != nil {
		errs = append(errs, latErr)
	}
	lon, lonErr := parseFloatEnv("STATION_LONGITUDE", minLongitude, maxLongitude)
	if lonErr != nil {
		errs = append(errs, lonErr)
	}
	// Coordinates are a pair or nothing: a half-set pair is a MALFORMED
	// configuration, not a partial one, so it is an error rather than a
	// silent omission.
	switch {
	case latErr != nil || lonErr != nil:
		// Already reported above. Adding a pairing error too would tell an
		// operator with one typo that they also forgot the other variable.
	case (lat != nil) != (lon != nil):
		errs = append(errs, errors.New("STATION_LATITUDE and STATION_LONGITUDE must be set together"))
	default:
		cfg.Latitude, cfg.Longitude = lat, lon
	}

	elev, elevErr := parseFloatEnv("STATION_ELEVATION", minElevationM, maxElevationM)
	if elevErr != nil {
		errs = append(errs, elevErr)
	} else {
		cfg.Elevation = elev
	}

	if v := os.Getenv("STATION_TIMEZONE"); v != "" {
		loc, err := time.LoadLocation(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid IANA timezone %q for STATION_TIMEZONE: %w", v, err))
		} else {
			cfg.Location = loc
		}
	}

	return cfg, errors.Join(errs...)
}

// parseFloatEnv reads key as a finite float in [lo, hi]. An unset or empty
// variable yields (nil, nil) -- absent is not an error. A value that does
// not parse, is not finite, or falls outside the range is a fatal
// configuration error naming both the variable and the offending value, in
// the shape ParseBoolEnv established.
//
// The bounds are named lo/hi rather than min/max so they do not shadow the
// builtins of those names.
func parseFloatEnv(key string, lo, hi float64) (*float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid float value %q for %s: %w", v, key, err)
	}
	// strconv.ParseFloat accepts "NaN" and "Inf". NaN compares false against
	// BOTH bounds below, so without this it would sail through the range
	// check and reach the almanac as a silently poisoned coordinate.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf("value %q for %s is not a finite number", v, key)
	}
	if f < lo || f > hi {
		return nil, fmt.Errorf("value %v for %s is out of range [%v, %v]", f, key, lo, hi)
	}
	return &f, nil
}
