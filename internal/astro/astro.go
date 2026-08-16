// Package astro computes solar and lunar quantities for the station almanac.
//
// The equations are VENDORED deliberately rather than fetched or depended
// on. Sunrise/sunset transcribes NOAA's Solar Calculator JavaScript
// (gml.noaa.gov/grad/solcalc/main.js), which NOAA now states is "no longer
// actively supported or maintained"; the mathematics is Meeus and is
// unaffected, but the URL may vanish, so it must not become a live or
// documentation-time dependency. Moon phase transcribes Meeus chapter 48
// formula 48.4. Both sequences, their traps, and the authoritative test
// vectors are recorded in Appendix A of
// docs/designs/2026-08-13-weatherflow-token-and-shaping-design.md.
//
// The package is pure: no state, no I/O, no dependency outside the standard
// library.
package astro

import "math"

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// julianDay0 returns the Julian Day at 00:00 UT for the proleptic Gregorian
// calendar date y-m-d (Meeus formula 7.1, A.1 step 1). m is 1-based.
func julianDay0(y, m, d int) float64 {
	if m <= 2 {
		y--
		m += 12
	}
	a := math.Floor(float64(y) / 100)
	b := 2 - a + math.Floor(a/4)
	return math.Floor(365.25*float64(y+4716)) + math.Floor(30.6001*float64(m+1)) + float64(d) + b - 1524.5
}
