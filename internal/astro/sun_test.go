package astro

import (
	"maps"
	"math"
	"slices"
	"testing"
	"time"
)

// tolerance is an ACCURACY bound, not a transcription check: a literal
// transcription of A.1 deviates from USNO by up to ~65 s over every non-nil
// value in A.3 -- the worst case being row 17, at 71.29° latitude, which is
// outside NOAA's own +/-1 min claim (NOAA scopes that to |lat| < 72°). USNO
// publishes whole minutes on top of that. It is deliberately NOT tightened:
// seven of the ten solar correction terms could be dropped and every row
// below would still pass, so term presence is checked structurally by
// TestSolarPosition_TermsAreAllPresent instead (design §7.1).
const tolerance = 90 * time.Second

func mustTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

// vectors is Appendix A.3 rows 1-17, transcribed verbatim, plus one row
// (18) derived in-repo rather than taken from A.3 -- see that row's own
// comment for its provenance. datePassed is the calendar date the row asks
// about; the test drives SunriseSunset with noon UTC on that date, so
// t.Date() in t's own location is exactly datePassed.
var vectors = []struct {
	name     string
	lat, lon float64
	year     int
	month    time.Month
	day      int
	wantRise *time.Time
	wantSet  *time.Time
}{
	{"denver_june_solstice_set_on_next_day", 39.74, -104.98, 2026, time.June, 21,
		mustTime("2026-06-21T11:32:00Z"), mustTime("2026-06-22T02:31:00Z")},
	{"denver_december_solstice", 39.74, -104.98, 2026, time.December, 21,
		mustTime("2026-12-21T14:17:00Z"), mustTime("2026-12-21T23:39:00Z")},
	{"denver_march_equinox_set_on_next_day", 39.74, -104.98, 2026, time.March, 20,
		mustTime("2026-03-20T13:03:00Z"), mustTime("2026-03-21T01:12:00Z")},
	{"london_june_solstice_both_on_day", 51.5074, -0.1278, 2026, time.June, 21,
		mustTime("2026-06-21T03:43:00Z"), mustTime("2026-06-21T20:22:00Z")},
	{"sydney_june_solstice_sunrise_on_prior_day", -33.8688, 151.2093, 2026, time.June, 21,
		mustTime("2026-06-20T21:00:00Z"), mustTime("2026-06-21T06:54:00Z")},
	{"sydney_december_solstice_summer_sunrise_on_prior_day", -33.8688, 151.2093, 2026, time.December, 21,
		mustTime("2026-12-20T18:41:00Z"), mustTime("2026-12-21T09:05:00Z")},
	{"quito_march_equinox_equator_west", -0.1807, -78.4678, 2026, time.March, 20,
		mustTime("2026-03-20T11:18:00Z"), mustTime("2026-03-20T23:24:00Z")},
	{"singapore_september_equinox_equator_east_sunrise_on_prior_day", 1.3521, 103.8198, 2026, time.September, 23,
		mustTime("2026-09-22T22:54:00Z"), mustTime("2026-09-23T11:00:00Z")},
	{"utqiagvik_polar_night", 71.2906, -156.7887, 2026, time.December, 21, nil, nil},
	{"utqiagvik_midnight_sun", 71.2906, -156.7887, 2026, time.June, 21, nil, nil},
	{"longyearbyen_high_arctic_no_sunrise", 78.2232, 15.6469, 2026, time.January, 15, nil, nil},
	{"longyearbyen_high_arctic_no_sunset", 78.2232, 15.6469, 2026, time.June, 21, nil, nil},
	// UTC rise/set is unaffected by DST; these rows exist to prove
	// SunriseSunset never consults t.Location() (design Appendix A.3).
	{"new_york_dst_spring_forward_utc_unaffected", 40.7128, -74.0060, 2026, time.March, 8,
		mustTime("2026-03-08T11:19:00Z"), mustTime("2026-03-08T22:55:00Z")},
	{"new_york_dst_fall_back_utc_unaffected", 40.7128, -74.0060, 2026, time.November, 1,
		mustTime("2026-11-01T11:26:00Z"), mustTime("2026-11-01T21:52:00Z")},
	{"utqiagvik_last_sunrise_before_polar_night", 71.2906, -156.7887, 2026, time.November, 18,
		mustTime("2026-11-18T21:42:00Z"), mustTime("2026-11-18T22:42:00Z")},
	{"utqiagvik_first_polar_night_day", 71.2906, -156.7887, 2026, time.November, 19, nil, nil},
	// Row 17 -- the half-populated pair. A build that collapses an asymmetric
	// result to (nil, nil) fails HERE and passes everything else (design §6.4).
	{"utqiagvik_sunrise_without_sunset", 71.2906, -156.7887, 2026, time.May, 10,
		mustTime("2026-05-10T10:58:00Z"), nil},
	// The antimeridian. Solar noon at lon = -180 falls at exactly midnight
	// + 24 h -- the first instant of the NEXT UTC day -- so the interval in
	// which "solar noon is inside UTC date U" holds is (-180, 180], open at
	// the lower end. The shipped code assumed it was closed and returned the
	// following day's pair here, in EVERY zone including UTC.
	//
	// The expected values are the ones lon = +180 produces, which is the same
	// meridian and must agree; they are independently sanity-checked by
	// inspection, since solar noon at 180 is 00:00 UTC and the equatorial
	// solstice day is ~12 h, putting sunrise ~18:00 UTC on D-1 and sunset
	// ~06:00 UTC on D.
	//
	// This is deliberately not a live paired time.Time.Equal(lon=+180)
	// assertion: on correct code the two longitudes differ by ~1.97 us (they
	// anchor on UTC dates a day apart, so refineEvent's first pass runs a
	// Julian Day apart). An exact-equality pairing would fail correct code;
	// convert to a paired assertion only with a tolerance.
	{"antimeridian_west_sign_matches_east_sign", 0, -180, 2026, time.June, 21,
		mustTime("2026-06-20T17:58:01Z"), mustTime("2026-06-21T06:05:24Z")},
}

func TestSunriseSunset_USNOVectors(t *testing.T) {
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			at := time.Date(v.year, v.month, v.day, 12, 0, 0, 0, time.UTC)
			rise, set := SunriseSunset(v.lat, v.lon, at)
			assertInstant(t, "sunrise", rise, v.wantRise)
			assertInstant(t, "sunset", set, v.wantSet)
		})
	}
}

// assertInstant compares an optional instant: nil is an exact comparison,
// non-nil is compared within tolerance.
func assertInstant(t *testing.T, label string, got, want *time.Time) {
	t.Helper()
	switch {
	case want == nil && got == nil:
		return
	case want == nil:
		t.Fatalf("%s: got %s, want nil", label, got.UTC().Format(time.RFC3339))
	case got == nil:
		t.Fatalf("%s: got nil, want %s", label, want.UTC().Format(time.RFC3339))
	}
	if d := got.Sub(*want); d < -tolerance || d > tolerance {
		t.Fatalf("%s: got %s, want %s (off by %s, tolerance %s)",
			label, got.UTC().Format(time.RFC3339), want.UTC().Format(time.RFC3339), d, tolerance)
	}
}

// TestSunriseSunset_UsesLocalDateNotUTCDate proves SunriseSunset reads the
// calendar date from t's OWN location. The instant below is 2026-11-18
// 16:00 in a UTC-9 zone, which is 2026-11-19 01:00 UTC. A.3 row 15 says
// Utqiagvik has a sunrise on the 18th; row 16 says the 19th is the first
// polar-night day. An implementation calling t.UTC().Date() returns nil.
func TestSunriseSunset_UsesLocalDateNotUTCDate(t *testing.T) {
	akst := time.FixedZone("AKST", -9*3600)
	at := time.Date(2026, time.November, 18, 16, 0, 0, 0, akst)

	rise, _ := SunriseSunset(71.2906, -156.7887, at)
	if rise == nil {
		t.Fatal("sunrise: got nil -- the date was taken from t.UTC(), not from t's own location")
	}
	assertInstant(t, "sunrise", rise, mustTime("2026-11-18T21:42:00Z"))
}

// TestSunriseSunset_DatelineZones covers four of the IANA zones whose legal
// calendar runs a day ahead of the sun -- Kiritimati +14, Apia +13,
// Tongatapu +13 and Chatham +12:45 -- plus one control, Auckland in NZDT.
// Four is not the whole set: Pacific/Fakaofo and Pacific/Kanton are also +13
// near 171 degrees W and take the same shift branch. Neither has a row,
// deliberately -- Kanton sits within 0.02 degrees of longitude of Apia, so it
// would add breadth without adding discrimination.
// Auckland's legal offset is also +13, but its solar offset is +11.65, so its
// skew is only +1.35 and its anchor must NOT shift -- it is the row that fails
// under issue #166's own suggested fix.
//
// The offset field is offsetSec rather than offsetHours because Chatham is
// +12:45. Note that its row does not, on its own, demonstrate fractional-offset
// handling: +12:00, +12:45 and +13:00 all land in the same floor bucket at that
// longitude and produce byte-identical instants. It is a date-selection
// regression guard. A test that genuinely exercises the fractional path needs
// an (offset, lon) pair whose shift expression straddles a floor boundary
// within 0.03125.
//
// Values are USNO (aa.usno.navy.mil/api/rstt/oneday). The first three were
// transcribed from design section 2.3 and later re-queried live, returning
// HTTP 200 with exact matches; the Chatham and Tongatapu rows were queried
// live when they were added (issue #171). Re-verifying any of them against
// USNO is welcome. Do NOT substitute another provider: api.sunrise-sunset.org
// measured 160-230 s wide of USNO on day length, which would breach the
// +-90 s tolerance this file must not relax.
//
// time.FixedZone is used rather than time.LoadLocation for three reasons:
// SunriseSunset reads only t.Zone(), so the code path is identical; no
// _ "time/tzdata" import is then needed (this package imports only math and
// time, so it does not inherit internal/config's embedded database); and a
// pinned offset cannot change meaning when a tzdata update revises a zone,
// which for these locations is not hypothetical -- Samoa moved across the
// dateline in 2011.
//
// Comparing instants is sufficient to catch the bug: the defect is a
// whole-day error, which is three orders of magnitude outside tolerance.
func TestSunriseSunset_DatelineZones(t *testing.T) {
	tests := []struct {
		name              string
		lat, lon          float64
		offsetSec         int
		year              int
		month             time.Month
		day               int
		wantRise, wantSet *time.Time
	}{
		{
			// USNO: Rise 06:29, Set 18:40 local (+14).
			name: "kiritimati_utc_plus_14", lat: 1.87, lon: -157.43, offsetSec: 14 * 3600,
			year: 2026, month: time.August, day: 14,
			wantRise: mustTime("2026-08-13T16:29:00Z"),
			wantSet:  mustTime("2026-08-14T04:40:00Z"),
		},
		{
			// USNO: Rise 06:43, Set 18:21 local (+13).
			name: "apia_utc_plus_13", lat: -13.83, lon: -171.77, offsetSec: 13 * 3600,
			year: 2026, month: time.August, day: 14,
			wantRise: mustTime("2026-08-13T17:43:00Z"),
			wantSet:  mustTime("2026-08-14T05:21:00Z"),
		},
		{
			// CONTROL, and the load-bearing row. Auckland in NZDT also has a
			// +13 legal offset, but its SOLAR offset is +11.65, so its skew is
			// only +1.35 and the anchor must NOT shift. Issue #166's own
			// suggested fix -- deriving the date from local CLOCK noon --
			// passes the two rows above and FAILS this one, 190 days a year.
			// USNO: Rise 06:18, Set 20:42 local (+13).
			name: "auckland_nzdt_must_not_shift", lat: -36.85, lon: 174.76, offsetSec: 13 * 3600,
			year: 2026, month: time.January, day: 15,
			wantRise: mustTime("2026-01-14T17:18:00Z"),
			wantSet:  mustTime("2026-01-15T07:42:00Z"),
		},
		{
			// USNO: Rise 07:29, Set 17:44 local (+12:45). Chatham is the only
			// one of the four anomaly zones with a non-integer offset, which is
			// why this table's field is offsetSec rather than offsetHours.
			//
			// What this row proves is anomaly-zone DATE SELECTION, not
			// fractional arithmetic: measured, +12:00, +12:45 and +13:00 all
			// fall in the same floor bucket for this longitude and return
			// byte-identical instants, so the assertion cannot distinguish
			// them. Pre-#166 code returned local 2026-08-15 here.
			name: "chatham_utc_plus_12_45", lat: -43.95, lon: -176.55, offsetSec: 12*3600 + 45*60,
			year: 2026, month: time.August, day: 14,
			wantRise: mustTime("2026-08-13T18:44:00Z"),
			wantSet:  mustTime("2026-08-14T04:59:00Z"),
		},
		{
			// USNO: Rise 07:05, Set 18:27 local (+13). The fourth anomaly zone.
			// Pre-#166 code returned local 2026-08-15 here too.
			name: "tongatapu_utc_plus_13", lat: -21.13, lon: -175.20, offsetSec: 13 * 3600,
			year: 2026, month: time.August, day: 14,
			wantRise: mustTime("2026-08-13T18:05:00Z"),
			wantSet:  mustTime("2026-08-14T05:27:00Z"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := time.FixedZone("TEST", tc.offsetSec)
			at := time.Date(tc.year, tc.month, tc.day, 0, 0, 0, 0, loc)

			rise, set := SunriseSunset(tc.lat, tc.lon, at)
			assertInstant(t, "sunrise", rise, tc.wantRise)
			assertInstant(t, "sunset", set, tc.wantSet)
		})
	}
}

// eqTimeTerms returns A.1 step 11's five equation-of-time terms by name, in
// the radian scale solarPosition sums them in.
func eqTimeTerms(l0, m, e, y float64) map[string]float64 {
	return map[string]float64{
		"y_sin_2L0":              y * math.Sin(rad(2*l0)),
		"minus_2e_sin_M":         -2 * e * math.Sin(rad(m)),
		"plus_4ey_sin_M_cos_2L0": 4 * e * y * math.Sin(rad(m)) * math.Cos(rad(2*l0)),
		"minus_half_y2_sin_4L0":  -0.5 * y * y * math.Sin(rad(4*l0)),
		"minus_1p25_e2_sin_2M":   -1.25 * e * e * math.Sin(rad(2*m)),
	}
}

// referenceEqTime sums the terms with one optionally omitted, converting to
// minutes exactly as A.1 step 11 does. omit == "" builds the full sum.
func referenceEqTime(terms map[string]float64, omit string) float64 {
	var sum float64
	for name, v := range terms {
		if name != omit {
			sum += v
		}
	}
	return deg(sum) * 4.0
}

// eqCentreTerms returns A.1 step 6's three equation-of-centre terms. These
// reach the result through the declination rather than the equation of time,
// so they need their own reference chain below.
func eqCentreTerms(tc, m float64) map[string]float64 {
	return map[string]float64{
		"sin_M_1p914602":  math.Sin(rad(m)) * (1.914602 - tc*(0.004817+0.000014*tc)),
		"sin_2M_0p019993": math.Sin(rad(2*m)) * (0.019993 - 0.000101*tc),
		"sin_3M_0p000289": math.Sin(rad(3*m)) * 0.000289,
	}
}

// referenceObliquity is an INDEPENDENT transcription of A.1 step 9 -- the
// corrected mean obliquity of the ecliptic, in degrees. It is written from the
// design, NOT derived from sun.go, which is what lets it anchor sun.go.
//
// Reconciling this function to match a changed implementation defeats the
// check entirely. If it starts failing, check solarIntermediates against A.1
// step 9 BEFORE touching anything here.
//
// referenceDeclination now calls this function rather than carrying its own
// transcription, so this is the single reference feeding BOTH the obliquity
// anchor (TestSolarIntermediates_ObliquityIsAnchored) and the declination
// anchor (TestSolarPosition_TermsAreAllPresent): a bad edit here silently
// weakens two checks at once, not one.
func referenceObliquity(jd float64) float64 {
	tc := (jd - 2451545.0) / 36525.0
	omega := 125.04 - 1934.136*tc
	sec := 21.448 - tc*(46.8150+tc*(0.00059-tc*0.001813))
	return 23.0 + (26.0+sec/60.0)/60.0 + 0.00256*math.Cos(rad(omega))
}

// TestSolarIntermediates_ObliquityIsAnchored pins the single obliquity that
// sun.go computes, and pins the fact that the equation of time is built from
// THAT value rather than from a second series.
//
// Both assertions are load-bearing and they catch different mutants:
//
//	assertion 1 catches a drifted eps -- including the specific recurrence of
//	  issue #167, where someone re-inlines a correct obliquity for the
//	  declination and leaves the shared one wrong. The +-90 s vector tolerance
//	  absorbs up to ~0.3 deg of that drift, so nothing else catches it.
//	assertion 2 catches a correct eps whose y was built from a DIFFERENT
//	  series -- the shape the bug had before the two were merged.
//
// Do not drop either.
func TestSolarIntermediates_ObliquityIsAnchored(t *testing.T) {
	for _, jd := range probeDates {
		_, _, _, y, eps := solarIntermediates(jd)

		if want := referenceObliquity(jd); math.Abs(eps-want) > 1e-9 {
			t.Fatalf("jd %.1f: solarIntermediates eps = %.12f, independent reference = %.12f.\n"+
				"Check A.1 step 9 in sun.go BEFORE editing referenceObliquity.", jd, eps, want)
		}

		// tan^2(eps/2) is A.1 step 9's y. Measured headroom is ~1e-18, so
		// 1e-12 is six orders loose and cannot flake; it is tight enough that
		// a second obliquity series feeding y cannot hide behind it.
		tan := math.Tan(rad(eps / 2))
		if want := tan * tan; math.Abs(y-want) > 1e-12 {
			t.Fatalf("jd %.1f: y = %.18f but tan^2(eps/2) = %.18f -- y is built from a "+
				"DIFFERENT obliquity than the one returned.", jd, y, want)
		}
	}
}

// referenceDeclination rebuilds A.1 steps 6-10 with one equation-of-centre
// term optionally omitted. The chain is duplicated from solarPosition
// deliberately: the anchor assertion fails loudly if the two ever drift,
// which is exactly what makes the per-term checks trustworthy.
func referenceDeclination(jd float64, omit string) float64 {
	tc := (jd - 2451545.0) / 36525.0
	l0, m, _, _, _ := solarIntermediates(jd)

	var c float64
	for name, v := range eqCentreTerms(tc, m) {
		if name != omit {
			c += v
		}
	}

	omega := 125.04 - 1934.136*tc
	lambda := l0 + c - 0.00569 - 0.00478*math.Sin(rad(omega))
	return deg(math.Asin(math.Sin(rad(referenceObliquity(jd))) * math.Sin(rad(lambda))))
}

// probeDates spans the year: a single instant can drive a sin/cos factor to
// zero and make a present term look absent, so every per-term assertion
// below is over the MAXIMUM divergence across all four.
var probeDates = []float64{
	julianDay0(2026, 3, 20) + 0.5,
	julianDay0(2026, 6, 21) + 0.5,
	julianDay0(2026, 9, 23) + 0.5,
	julianDay0(2026, 12, 21) + 0.5,
}

func TestSolarPosition_TermsAreAllPresent(t *testing.T) {
	// ANCHOR. Without this the per-term checks would compare the reference
	// against itself and pass against an implementation missing terms.
	for _, jd := range probeDates {
		l0, m, e, y, _ := solarIntermediates(jd)
		gotEqTime, gotDecl := solarPosition(jd)

		if full := referenceEqTime(eqTimeTerms(l0, m, e, y), ""); math.Abs(gotEqTime-full) > 1e-9 {
			t.Fatalf("jd %.1f: solarPosition eqTime = %.12f, reference sum = %.12f.\n"+
				"A TERM MISSING FROM sun.go FIRES THIS ANCHOR FIRST, before any per-term "+
				"subtest runs -- so check solarPosition against A.1 step 11 BEFORE touching "+
				"this test. Reconcile eqTimeTerms only once sun.go is provably correct: "+
				"editing the reference to match a broken implementation defeats this check "+
				"entirely.", jd, gotEqTime, full)
		}
		if full := referenceDeclination(jd, ""); math.Abs(gotDecl-full) > 1e-9 {
			t.Fatalf("jd %.1f: solarPosition decl = %.12f, reference = %.12f.\n"+
				"As above: check solarPosition's equation-of-centre terms against A.1 step 6 "+
				"BEFORE editing referenceDeclination.", jd, gotDecl, full)
		}
	}

	l0, m, e, y, _ := solarIntermediates(probeDates[0])

	// Sorted, so subtest order is deterministic -- map iteration is not.
	for _, name := range slices.Sorted(maps.Keys(eqTimeTerms(l0, m, e, y))) {
		t.Run("eqtime_"+name, func(t *testing.T) {
			var maxDelta float64
			for _, jd := range probeDates {
				a, b, c, d, _ := solarIntermediates(jd)
				got, _ := solarPosition(jd)
				without := referenceEqTime(eqTimeTerms(a, b, c, d), name)
				maxDelta = max(maxDelta, math.Abs(got-without))
			}
			// An implementation missing this term would produce exactly the
			// one-term-short sum at every date.
			if maxDelta < 1e-9 {
				t.Fatalf("solarPosition's eqTime matches a sum with %s removed at every probe "+
					"date (max divergence %.12g) -- the term is missing", name, maxDelta)
			}
		})
	}

	tc := (probeDates[0] - 2451545.0) / 36525.0
	for _, name := range slices.Sorted(maps.Keys(eqCentreTerms(tc, m))) {
		t.Run("eqcentre_"+name, func(t *testing.T) {
			var maxDelta float64
			for _, jd := range probeDates {
				_, got := solarPosition(jd)
				maxDelta = max(maxDelta, math.Abs(got-referenceDeclination(jd, name)))
			}
			if maxDelta < 1e-9 {
				t.Fatalf("solarPosition's declination matches a chain with %s removed at every "+
					"probe date (max divergence %.12g) -- the term is missing", name, maxDelta)
			}
		})
	}
}
