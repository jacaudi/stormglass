package astro

import (
	"math"
	"slices"
	"testing"
	"time"
)

const (
	illumTolerance = 0.005 // 0.5 percentage points, expressed as a fraction
	phaseTolerance = 0.005
)

// moonVectors is Appendix A.4, transcribed verbatim. wantIllum is Horizons'
// published Illu% converted to a fraction and is the AUTHORITY. wantPhase is
// the appendix's "expected phase" column -- model output, not published; it
// must NOT be re-derived from the phase-angle column, which misses by more
// than the tolerance on three of these ten rows near syzygy.
var moonVectors = []struct {
	name      string
	instant   string // RFC3339 UTC
	wantIllum float64
	wantPhase float64
	wantName  string
}{
	{"usno_new_moon_2026_01", "2026-01-18T19:52:00Z", 0.0008678, 0.99987, "New Moon"},
	// NOTE: the design annotates this row as exercising the negative-modulo
	// path. It does NOT -- see Step 7. psi here is +359.98 and T is +0.000144,
	// so this instant is not even pre-J2000. Keep the row (it is a real
	// Horizons vector at a useful near-zero phase); do not keep the claim.
	{"meeus_ch49_epoch", "2000-01-06T18:14:00Z", 0.0002022, 0.99995, "New Moon"},
	{"usno_first_quarter_2026_03", "2026-03-25T19:18:00Z", 0.5012661, 0.24992, "First Quarter"},
	{"usno_full_moon_2026_06", "2026-06-29T23:56:00Z", 0.9987539, 0.49978, "Full Moon"},
	{"usno_last_quarter_2026_11", "2026-11-01T20:28:00Z", 0.5012906, 0.74922, "Last Quarter"},
	{"waning_gibbous_2026_04", "2026-04-08T00:00:00Z", 0.7048836, 0.68215, "Waning Gibbous"},
	{"first_quarter_2026_06", "2026-06-21T12:00:00Z", 0.4583584, 0.23717, "First Quarter"},
	// Convention trap, kept deliberately: phase ~= 0.04664 sits in the New
	// Moon band at 2.15% illumination, where USNO would say "Waxing
	// Crescent". This documents Sect 7.1's naming divergence, not a bug.
	{"new_moon_convention_trap_2026_08", "2026-08-14T00:00:00Z", 0.0215411, 0.04664, "New Moon"},
	{"last_quarter_2026_10", "2026-10-05T00:00:00Z", 0.3395809, 0.80181, "Last Quarter"},
	{"full_moon_out_of_year_2030_07", "2030-07-15T00:00:00Z", 0.9991922, 0.49685, "Full Moon"},
}

// circularDelta compares two phase fractions on a circle: vectors 1 and 10
// sit at phase ~0.9999, and a naive abs() against a want just above 0 yields
// ~0.99 and fails (design A.4).
func circularDelta(got, want float64) float64 {
	d := math.Abs(got - want)
	if d > 0.5 {
		d = 1 - d
	}
	return d
}

func TestMoonPhase_Vectors(t *testing.T) {
	for _, v := range moonVectors {
		t.Run(v.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, v.instant)
			if err != nil {
				t.Fatal(err)
			}
			phase, name, illum := MoonPhase(at)

			if d := circularDelta(phase, v.wantPhase); d > phaseTolerance {
				t.Errorf("phase = %.5f, want %.5f (circular delta %.5f > %.5f)", phase, v.wantPhase, d, phaseTolerance)
			}
			if d := math.Abs(illum - v.wantIllum); d > illumTolerance {
				t.Errorf("illumination = %.5f, want %.5f (off by %.5f)", illum, v.wantIllum, d)
			}
			// Names are asserted against §7.1's band table, NEVER against
			// USNO's curphase strings -- USNO reserves the quarter names for
			// exact instants and would say "Waxing Crescent" at 46%
			// illumination where the band table says "First Quarter".
			if name != v.wantName {
				t.Errorf("name = %q, want %q", name, v.wantName)
			}
		})
	}
}

// TestMoonPhase_WaxingConventionMatchesUI pins the convention the UI's moon
// SVG depends on: it treats phase <= 0.5 as waxing (AlmanacCard.tsx). A
// model that returned, say, 0 = full would render every crescent mirrored.
func TestMoonPhase_WaxingConventionMatchesUI(t *testing.T) {
	// USNO first quarter 2026 -- unambiguously waxing.
	firstQuarter, _ := time.Parse(time.RFC3339, "2026-03-25T19:18:00Z")
	// USNO last quarter 2026 -- unambiguously waning.
	lastQuarter, _ := time.Parse(time.RFC3339, "2026-11-01T20:28:00Z")

	if p, _, _ := MoonPhase(firstQuarter); p > 0.5 {
		t.Errorf("first quarter phase = %.5f, want <= 0.5 (the UI's waxing branch)", p)
	}
	if p, _, _ := MoonPhase(lastQuarter); p <= 0.5 {
		t.Errorf("last quarter phase = %.5f, want > 0.5 (the UI's waning branch)", p)
	}
}

// TestMoonPhase_NameBands pins §7.1's eight bands at their boundaries,
// which the ten vectors only sample. Boundaries sit at odd multiples of
// 1/16; each case probes just inside the band above and below.
//
// It calls phaseName -- the SAME function MoonPhase calls. An earlier draft
// re-implemented the index expression here instead, which pinned only the
// ordering of the moonPhaseNames array: dropping the +0.5 rounding term from
// MoonPhase left this test green. Do not inline the expression again.
func TestMoonPhase_NameBands(t *testing.T) {
	tests := []struct {
		phase float64
		want  string
	}{
		{0.0, "New Moon"},
		{1.0/16 - 1e-9, "New Moon"},
		{1.0/16 + 1e-9, "Waxing Crescent"},
		{3.0/16 + 1e-9, "First Quarter"},
		{5.0/16 + 1e-9, "Waxing Gibbous"},
		{7.0/16 + 1e-9, "Full Moon"},
		{9.0/16 + 1e-9, "Waning Gibbous"},
		{11.0/16 + 1e-9, "Last Quarter"},
		{13.0/16 + 1e-9, "Waning Crescent"},
		{15.0/16 + 1e-9, "New Moon"}, // wraps back to index 0
	}
	for _, tc := range tests {
		if got := phaseName(tc.phase); got != tc.want {
			t.Errorf("phase %.6f: name = %q, want %q", tc.phase, got, tc.want)
		}
	}
}

// TestEuclideanMod pins the helper directly. Go's math.Mod is a REMAINDER,
// not a modulus: it takes the sign of the dividend.
func TestEuclideanMod(t *testing.T) {
	tests := []struct {
		x, m, want float64
	}{
		{370, 360, 10},
		{360, 360, 0},
		{0, 360, 0},
		{-10, 360, 350},             // math.Mod gives -10
		{-370, 360, 350},            // math.Mod gives -10
		{-44233.6236, 360, 46.3764}, // a real psi from 1990-01-01
	}
	for _, tc := range tests {
		if got := euclideanMod(tc.x, tc.m); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("euclideanMod(%g, %g) = %g, want %g", tc.x, tc.m, got, tc.want)
		}
	}
}

// TestMoonPhase_NegativeElongation is the end-to-end guard for the branch
// A.4 misses. For dates before ~1999-12-08 the mean elongation goes negative,
// and with math.Mod in place of euclideanMod the phase would go negative too
// -- which not only breaks the [0, 1) contract but drives the name index
// negative, and Go's % keeps that sign, so moonPhaseNames[negative] PANICS
// with an index-out-of-range.
//
// No external source is needed: these are invariants of the model itself.
func TestMoonPhase_NegativeElongation(t *testing.T) {
	for _, instant := range []string{
		"1999-12-01T00:00:00Z", // psi ~ -78, just past the sign change
		"1990-01-01T00:00:00Z", // psi ~ -44234
		"1950-01-01T00:00:00Z", // psi ~ -222338
	} {
		t.Run(instant, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, instant)
			if err != nil {
				t.Fatal(err)
			}

			// Must not panic, and must stay in contract.
			phase, name, illum := MoonPhase(at)

			if phase < 0 || phase >= 1 {
				t.Errorf("phase = %v, want [0, 1) -- math.Mod was used instead of euclideanMod", phase)
			}
			if illum < 0 || illum > 1 {
				t.Errorf("illumination = %v, want [0, 1]", illum)
			}
			if !slices.Contains(moonPhaseNames[:], name) {
				t.Errorf("name = %q, want one of the eight pinned names", name)
			}
		})
	}
}
