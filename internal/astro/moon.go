package astro

import (
	"math"
	"time"
)

// moonPhaseNames are the eight equal 1/8-cycle bands, centred on their
// canonical points with boundaries at odd multiples of 1/16. These strings
// are USER-VISIBLE output (the UI renders moonPhaseName verbatim), so they
// are pinned by the design §7.1 rather than left to taste.
var moonPhaseNames = [8]string{
	"New Moon",
	"Waxing Crescent",
	"First Quarter",
	"Waxing Gibbous",
	"Full Moon",
	"Waning Gibbous",
	"Last Quarter",
	"Waning Crescent",
}

// MoonPhase returns the phase fraction (0 = new, 0.25 = first quarter,
// 0.5 = full), its conventional name, and the illuminated fraction, for the
// instant t.
//
// Meeus chapter 48 formula 48.4, rearranged from the PHASE ANGLE i to the
// elongation psi. Because psi = 180° - i, every correction term FLIPS SIGN
// relative to the published form; getting that backwards produces ~15
// percentage-point illumination errors that still look plausible.
//
// The simpler mean-synodic-month-from-epoch approach is rejected on
// measurement, not taste: 16.8 h worst-case phase-instant error against 50
// USNO events and 7.69 pp illumination error against JPL Horizons, versus
// ~46 min and ~0.31 pp here for about ten more lines.
//
// deltaT is deliberately ignored (~69 s in 2026, costing ~0.008 pp -- some
// 20x below the model's own error). Do not "fix" it.
func MoonPhase(t time.Time) (phase float64, name string, illumination float64) {
	jde := julianDayFull(t)
	tc := (jde - 2451545.0) / 36525.0
	t2 := tc * tc
	t3 := t2 * tc
	t4 := t3 * tc

	// Mean elongation, the sun's mean anomaly (Meeus 47.3 -- note the T²
	// coefficient differs from sun.go's, which uses Meeus 25.3; they are two
	// different published series, not two ports disagreeing), and the moon's
	// mean anomaly.
	d := 297.8501921 + 445267.1114034*tc - 0.0018819*t2 + t3/545868 - t4/113065000
	m := 357.5291092 + 35999.0502909*tc - 0.0001536*t2 + t3/24490000
	mp := 134.9633964 + 477198.8675055*tc + 0.0087414*t2 + t3/69699 - t4/14712000

	psi := d +
		6.289*math.Sin(rad(mp)) -
		2.100*math.Sin(rad(m)) +
		1.274*math.Sin(rad(2*d-mp)) +
		0.658*math.Sin(rad(2*d)) +
		0.214*math.Sin(rad(2*mp)) +
		0.110*math.Sin(rad(d))

	phase = euclideanMod(psi, 360) / 360
	// Adds no error of its own: given psi = 180° - i this reduces
	// algebraically to Meeus 48.1's (1 + cos i)/2. The composite still
	// carries 48.4's own ~0.15 pp bias at the quarters, which is inside the
	// 0.5 pp budget and must not be "corrected" -- doing so needs the moon's
	// distance and the full Meeus chapter 47 series.
	illumination = (1 - math.Cos(2*math.Pi*phase)) / 2
	name = phaseName(phase)

	return phase, name, illumination
}

// phaseName maps a phase fraction to its band name. Extracted so the band
// test can exercise the REAL index expression rather than re-implementing it
// -- a test that inlines this arithmetic pins only the array's ordering and
// stays green when the rounding term is dropped.
//
// Requires phase in [0, 1): a negative phase yields a negative index, and
// Go's % preserves the dividend's sign, so the lookup would panic. euclideanMod
// is what guarantees the domain.
func phaseName(phase float64) string {
	return moonPhaseNames[int(math.Floor(phase*8+0.5))%8]
}

// julianDayFull returns the CONTINUOUS Julian Day for t, including time of
// day. julianDay0 gives only the 00:00 UT form; omitting the fractional day
// costs ~5 percentage points of illumination.
func julianDayFull(t time.Time) float64 {
	u := t.UTC()
	y, mo, d := u.Date()
	frac := float64(u.Hour()*3600+u.Minute()*60+u.Second()) / 86400.0
	return julianDay0(y, int(mo), d) + frac
}

// euclideanMod returns x mod m in [0, m). Required rather than math.Mod,
// which returns NEGATIVE results for negative input.
//
// psi goes negative for dates before roughly 1999-12-08, where the mean
// elongation's linear term (445267.11 * T) outruns its 297.85 constant. NONE
// of the A.4 vectors reach that far back -- the earliest is 2000-01-06, at
// psi = +359.98 -- so the appendix table does NOT guard this branch and a
// plain math.Mod passes all ten. TestMoonPhase_NegativeElongation does guard
// it; do not delete that test on the grounds that the vectors cover it.
func euclideanMod(x, m float64) float64 {
	r := math.Mod(x, m)
	if r < 0 {
		r += m
	}
	return r
}
