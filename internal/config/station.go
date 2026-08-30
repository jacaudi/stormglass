package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
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

	// TimezoneConfigured records whether a timezone was supplied at all --
	// either STATION_TIMEZONE (set AND parsed) or a non-empty TZ.
	//
	// It records that A zone was supplied, not that it is the STATION's. TZ is
	// routinely injected cluster-wide (a shared envFrom ConfigMap, a mutating
	// webhook) to set the node's zone, and such an injection sets this flag,
	// so a Denver station on a Frankfurt cluster renders its almanac in
	// Frankfurt with no diagnostic. That is the accepted price of making TZ
	// the documented variable.
	//
	// Location cannot carry this: it defaults to time.UTC, and
	// time.LoadLocation("UTC") returns that same pointer, so an operator who
	// deliberately chose UTC is indistinguishable from one who set nothing.
	// The almanac warns only the second (issue #165). Not serialised.
	TimezoneConfigured bool

	// TimezoneNotices are non-fatal, operator-facing messages about how the
	// station zone was resolved -- the STATION_TIMEZONE deprecation and the
	// unresolvable-TZ advisory. main logs them at WARN; LoadStation stays a
	// pure decoder and never logs. nil when there is nothing to say. Not
	// serialised.
	TimezoneNotices []string
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

// The timezone notices are pinned constants rather than inline strings
// because each one has to stay true in every reachable state. The deprecation
// has three variants because the parsed one's every clause is false when the
// value did not parse, and its LAST clause is false -- and actively harmful --
// for "Local", which parses but names no zone TZ can load; the advisory has
// two because a single one would be false whenever STATION_TIMEZONE supplied
// the zone; and the suffix says "is also set" rather than "differs" because no
// string compare can decide whether two spellings name one zone.
// TestTimezoneMessages_ExactText pins all six.
const (
	tzDeprecationMsg = "STATION_TIMEZONE is deprecated and will be removed in a future release. " +
		"It is still honoured and still takes precedence over TZ, so this station's times use %q. " +
		"Set TZ to the same value and remove STATION_TIMEZONE."

	// Used instead of tzDeprecationMsg when the value did not parse. Every
	// clause of tzDeprecationMsg is false in that state, and "set TZ to the
	// same value" is actively harmful -- an invalid zone name is invalid in TZ
	// too.
	tzDeprecationMalformedMsg = "STATION_TIMEZONE is deprecated and will be removed in a future " +
		"release; set TZ instead. The value %q did not parse, so it supplied no zone — the " +
		"startup error below names it."

	// Used instead of tzDeprecationMsg when the value is exactly "Local".
	// time.LoadLocation special-cases that name and returns the process zone,
	// so the value PARSES and does supply the station's times -- it is not
	// malformed, and routing it to the variant above would both lie and change
	// behaviour. Only tzDeprecationMsg's last clause is wrong for it, and
	// wrong in the worst direction: the runtime cannot load "Local" from TZ
	// (initLocal finds no such zone and falls back to UTC), so an operator who
	// followed "set TZ to the same value" would silently move the almanac to
	// UTC while the station kept rendering correctly right up until they did.
	tzDeprecationLocalMsg = "STATION_TIMEZONE is deprecated and will be removed in a future " +
		"release. It is still honoured and still takes precedence over TZ, so this station's " +
		"times use %q. Local is not an IANA zone name and TZ=Local falls back to UTC, so set " +
		"TZ to the station's actual zone (e.g. America/Denver) and remove STATION_TIMEZONE."

	// "is also set", never "differs": whether the two spellings name the same
	// zone is not something a string compare can decide -- utc/UTC,
	// US/Mountain/America/Denver and an absolute path are each one zone that
	// fails an equality test. This wording asserts only what is always true.
	tzDeprecationAlsoSetMsg = " Note TZ=%q is also set; log timestamps follow TZ while station " +
		"times follow STATION_TIMEZONE."

	// Used when STATION_TIMEZONE did NOT supply the zone, so UTC really does
	// reach every station-local value.
	tzAdvisoryStationUnsetMsg = "TZ=%q resolved to UTC, so log timestamps and every station-local " +
		"time — sunrise/sunset, the almanac's calendar windows and its record date labels " +
		"— will use UTC. If that is not what you intended, set TZ to an IANA zone name such " +
		"as America/Denver."

	// Used when STATION_TIMEZONE DID supply the zone. Claiming station times
	// use UTC here would be false -- measured, TZ=EST5EDT,M3.2.0/2,M11.1.0/2
	// with STATION_TIMEZONE=America/Denver renders a 14:17Z sunrise as
	// "7:17 AM", not "2:17 PM".
	tzAdvisoryStationSetMsg = "TZ=%q resolved to UTC, so log timestamps will use UTC. Station times " +
		"are unaffected because STATION_TIMEZONE takes precedence. If that is not what you " +
		"intended, set TZ to an IANA zone name such as America/Denver."
)

// LoadStation decodes the station identity from the environment.
//
// No value is ever required: an unset variable yields a nil pointer and no
// error, and no combination of absent values can prevent the process from
// starting. A MALFORMED value -- unparseable, out of range, non-finite, an
// unknown STATION_TIMEZONE, or a half-set coordinate pair -- is a fatal
// configuration error, matching ParseBoolEnv's stance that a typo is an
// operator error rather than a silently disabled feature.
//
// EVERY malformed value is reported in one aggregated error, per
// go-standards §15.3: an operator with three bad values fixes them in one
// deploy cycle, not three. (TZ is the one variable read here that can never
// produce a fatal error -- an unresolvable value degrades to UTC with a
// notice.)
//
// PRECONDITION: local must be the runtime's own resolution of TZ. Production
// passes time.Local, which is exactly that; it is injected rather than read
// here so tests are deterministic, because time.Local resolves lazily on first
// use and a test that reads it directly is order-dependent. The notices are
// only true under this precondition -- "log timestamps follow TZ" is inferred
// from local, so an incoherent pair (a non-UTC local beside an unresolvable
// TZ) would make that clause false. That pair is unreachable in production,
// and no test may construct it. A nil local is normalised to time.UTC, so
// StationConfig.Location's never-nil guarantee is a property of this function
// rather than of the caller.
//
// LoadStation deliberately does not read feature flags. RADAR_SITE is
// decoded here unconditionally; main clears it when ENABLE_RADAR is false,
// so flag interpretation stays in one place and this stays a pure decoder.
// For the same reason it returns TimezoneNotices instead of logging them.
func LoadStation(local *time.Location) (StationConfig, error) {
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

	// Extracted rather than inline: inlining this logic puts LoadStation well
	// over .golangci.yml's gocyclo budget of 15 (independent reconstructions
	// measured 23 and 25, depending on the exact inline shape). Extracted,
	// LoadStation and timezoneNotices each measure 10.
	zone, tzConfigured, notices, tzErr := resolveTimezone(local,
		os.Getenv("STATION_TIMEZONE"), os.Getenv("TZ"))
	if tzErr != nil {
		errs = append(errs, tzErr)
	}
	cfg.Location = zone
	cfg.TimezoneConfigured = tzConfigured
	cfg.TimezoneNotices = notices

	return cfg, errors.Join(errs...)
}

// resolveTimezone performs the three independent resolutions of the timezone
// design: the station's zone, the TimezoneConfigured flag, and the operator
// notices. The returned zone is never nil.
//
// PRECONDITION: local must be the runtime's own resolution of tz -- production
// passes time.Local, which is exactly that. The notices below are only true
// under it: "log timestamps follow TZ" is inferred from local, so an incoherent
// pair (a non-UTC local beside an unresolvable tz) would make that clause
// false. Unreachable in production, and no test may construct it.
//
// The returned error is the malformed-STATION_TIMEZONE error, which stays
// fatal. A TZ the runtime could not resolve is deliberately NOT an error: TZ
// is an OS-level variable this project does not own -- glibc accepts POSIX
// forms such as EST5EDT,M3.2.0/2,M11.1.0/2 that time.LoadLocation rejects --
// and making those fatal would crash-loop the appliance over a display
// setting, the exact failure decideUI's design rejects for card flags.
func resolveTimezone(local *time.Location, stationTZ, tz string) (*time.Location, bool, []string, error) {
	if local == nil {
		local = time.UTC
	}

	zone := time.UTC
	stationTZOK := false
	var err error
	if stationTZ != "" {
		loc, loadErr := time.LoadLocation(stationTZ)
		if loadErr != nil {
			err = fmt.Errorf("invalid IANA timezone %q for STATION_TIMEZONE: %w", stationTZ, loadErr)
		} else {
			zone, stationTZOK = loc, true
		}
	}

	// STATION_TIMEZONE wins while it exists. "else" here means
	// "STATION_TIMEZONE did not yield a zone", not "was unset": a malformed
	// STATION_TIMEZONE leaves the zone at UTC even when TZ is set. Production
	// never observes that state -- the error above is fatal in main -- but a
	// test asserting Location there would otherwise resolve two ways.
	//
	// The tz != "" guard is load-bearing. Applying local unconditionally would
	// render the almanac in the host zone on any base image that ships
	// /etc/localtime, while the issue #165 warning -- keyed on
	// TimezoneConfigured -- announced that everything renders UTC.
	if !stationTZOK && tz != "" {
		zone = local
	}

	// The parse check on the STATION_TIMEZONE half is required: the flag is
	// documented as "set AND parsed", and a malformed value must leave it
	// false. For the TZ half there is nothing to parse, so non-empty is the
	// test.
	return zone, stationTZOK || tz != "", timezoneNotices(local, stationTZ, tz, stationTZOK), err
}

// timezoneNotices builds the non-fatal, operator-facing messages. It returns
// nil when there is nothing to say. Evaluated independently of the zone
// resolution, so a TZ that lost the zone is still diagnosed.
func timezoneNotices(local *time.Location, stationTZ, tz string, stationTZOK bool) []string {
	var notices []string

	// tzResolved is the negation of the advisory predicate below: TZ named a
	// zone the runtime actually landed on, or the operator asked for UTC and
	// got it. It gates the "is also set" suffix, because that suffix claims
	// log timestamps follow TZ -- which contradicts the advisory whenever TZ
	// did not resolve.
	tzResolved := tz != "" && (local.String() != "UTC" || isUTCSpelling(tz))

	// Fires even when the value is malformed and the load is about to fail,
	// so the operator learns the deprecation and the typo in one restart --
	// but with different text, because none of the "still honoured / times
	// use %q / set TZ to the same value" clauses is true in that state.
	if stationTZ != "" {
		if stationTZOK {
			n := fmt.Sprintf(deprecationMsgFor(stationTZ), stationTZ)
			// tzResolved is correctness; the TrimPrefix compare is only
			// noise suppression, so the common "both set to the same
			// string" deployment stays quiet. The suffix no longer claims
			// the two DIFFER, so a spelling this compare cannot equate
			// (utc/UTC, a tzdata Link alias, an absolute path) yields a
			// redundant true sentence rather than a false one.
			if tzResolved && strings.TrimPrefix(tz, ":") != stationTZ {
				n += fmt.Sprintf(tzDeprecationAlsoSetMsg, tz)
			}
			notices = append(notices, n)
		} else {
			notices = append(notices, fmt.Sprintf(tzDeprecationMalformedMsg, stationTZ))
		}
	}

	// Expressed as !tzResolved rather than as its own predicate: "TZ resolved"
	// and "TZ landed on UTC unintentionally" are one piece of knowledge, and
	// writing them as two hand-maintained De Morgan duals is exactly the
	// invariant whose breakage produced this design's earlier false
	// diagnostics.
	//
	// local.String() is the only available signal, and the notice says only
	// that the runtime LANDED on UTC -- never why. The runtime's own TZ
	// handling (initLocal, $(go env GOROOT)/src/time/zoneinfo_unix.go:28-69)
	// strips a leading colon and loads absolute paths, neither of which
	// time.LoadLocation does, so re-deriving the resolution here would be
	// wrong in both directions.
	if tz != "" && !tzResolved {
		if stationTZOK {
			notices = append(notices, fmt.Sprintf(tzAdvisoryStationSetMsg, tz))
		} else {
			notices = append(notices, fmt.Sprintf(tzAdvisoryStationUnsetMsg, tz))
		}
	}

	return notices
}

// localZoneName is time.LoadLocation's one name that parses without naming an
// IANA zone: it returns the process's local zone. The stdlib switches on this
// literal, so the comparison below is an exact match of a documented special
// case rather than a heuristic -- "local" and "LOCAL" do not reach it and are
// ordinary unknown zones.
const localZoneName = "Local"

// deprecationMsgFor picks the deprecation variant for a STATION_TIMEZONE that
// parsed. Extracted rather than inlined so timezoneNotices stays at its
// measured gocyclo of 10; see LoadStation's note on the same budget.
func deprecationMsgFor(stationTZ string) string {
	if stationTZ == localZoneName {
		return tzDeprecationLocalMsg
	}
	return tzDeprecationMsg
}

// isUTCSpelling reports whether tz is a spelling of UTC or GMT, so an operator
// who deliberately chose UTC is not advised about it.
//
// The leading colon is stripped because initLocal accepts ":UTC" while the
// name it records does not carry the colon. EqualFold is required because
// TZ=utc yields String()=="utc" on a case-insensitive filesystem (darwin) and
// "UTC" on Linux. The GMT arm is not dead code: on Linux TZ=gmt gives
// String()=="UTC", and this arm is what suppresses the advisory for it.
//
// Accepted false positives -- spellings Go cannot load but the operator
// plainly meant as UTC, which therefore still produce an advisory -- include
// "UTC0", "Z", "UTC+0" and "UTC-0"; the set is platform-dependent and is not
// enumerated here. The advisory stays TRUE in all of them (the runtime really
// did land on UTC); it is only redundant.
func isUTCSpelling(tz string) bool {
	name := strings.TrimPrefix(tz, ":")
	return strings.EqualFold(name, "UTC") || strings.EqualFold(name, "GMT")
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
