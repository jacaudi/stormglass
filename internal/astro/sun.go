package astro

import (
	"math"
	"time"
)

// zenith is the solar zenith angle defining sunrise/sunset: 90° plus 50′,
// being 16′ of solar semidiameter and 34′ of mean horizon refraction. USNO
// uses 90.8333 (exactly 90°50′); the 1-2 second difference is far inside
// tolerance, and this matches NOAA (design §7.1 item 4).
const zenith = 90.833

// SunriseSunset returns the sunrise/sunset pair bracketing solar noon on the
// calendar date that t falls on IN t's OWN LOCATION -- callers pass a t
// already in the station's timezone, so "today" means the station's today.
// lat and lon are degrees, with EAST-POSITIVE longitude (verified against
// USNO's published Denver transit; some older NOAA spreadsheets are
// west-positive and mixing them silently inverts results).
//
// Both results are absolute instants and either may fall on an adjacent UTC
// day: at west longitudes sunset commonly lands on D+1, at east longitudes
// sunrise on D-1. Either may INDEPENDENTLY be nil where that event does not
// occur -- near the polar boundary a day can have a sunrise and no sunset,
// because refineEvent recomputes each event at its own Julian Day and the
// hour-angle guard is therefore evaluated twice with different results
// (design §6.4, guarded by A.3 row 17). Both are nil on a fully dark or
// fully lit day.
func SunriseSunset(lat, lon float64, t time.Time) (sunrise, sunset *time.Time) {
	y, mo, d := t.Date() // t's OWN location -- NOT t.UTC().Date() (design §7.1 item 1)
	jd := julianDay0(y, int(mo), d)
	midnightUTC := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)

	return refineEvent(jd, midnightUTC, lat, lon, true),
		refineEvent(jd, midnightUTC, lat, lon, false)
}

// refineEvent runs A.1 step 14 for a single event: a first pass at jd, then
// EXACTLY ONE recomputation at that event's own Julian Day, matching NOAA's
// calcSunriseSet (which calls calcSunriseSetUTC per event). Sharing one
// recomputation between the two events instead shifts sunset by up to 32 s
// -- inside the tolerance, so the vectors would not catch it.
func refineEvent(jd float64, midnightUTC time.Time, lat, lon float64, rise bool) *time.Time {
	first, ok := eventMinutes(jd, lat, lon, rise)
	if !ok {
		return nil
	}
	second, ok := eventMinutes(jd+first/1440.0, lat, lon, rise)
	if !ok {
		return nil
	}
	// A.1 step 15. second legitimately falls below 0 or above 1440 and MUST
	// NOT be clamped; time.Duration arithmetic carries it into the adjacent
	// UTC day, which is the intended result.
	at := midnightUTC.Add(time.Duration(second * float64(time.Minute)))
	return &at
}

// eventMinutes returns the requested event's offset in minutes from 00:00
// UTC (A.1 steps 12-13). ok is false when the event does not occur:
// cosH > 1 is polar night and cosH < -1 is midnight sun. The guard tests
// cosH BEFORE math.Acos -- letting Acos return NaN and testing math.IsNaN
// also works but discards the sign, and with it the distinction.
func eventMinutes(jd, lat, lon float64, rise bool) (float64, bool) {
	eqTime, decl := solarPosition(jd)

	cosH := math.Cos(rad(zenith))/(math.Cos(rad(lat))*math.Cos(rad(decl))) -
		math.Tan(rad(lat))*math.Tan(rad(decl))
	if cosH > 1 || cosH < -1 {
		return 0, false
	}
	h := deg(math.Acos(cosH))
	if !rise {
		h = -h
	}
	return 720 - 4.0*(lon+h) - eqTime, true
}

// solarPosition returns the equation of time in MINUTES and the sun's
// declination in degrees for Julian Day jd (A.1 steps 2-11).
func solarPosition(jd float64) (eqTime, decl float64) {
	t := (jd - 2451545.0) / 36525.0

	l0, m, e, y, eps := solarIntermediates(jd)

	// Step 6: equation of the centre.
	c := math.Sin(rad(m))*(1.914602-t*(0.004817+0.000014*t)) +
		math.Sin(rad(2*m))*(0.019993-0.000101*t) +
		math.Sin(rad(3*m))*0.000289
	// Steps 7-8: true longitude, then apparent longitude.
	o := l0 + c
	omega := 125.04 - 1934.136*t
	lambda := o - 0.00569 - 0.00478*math.Sin(rad(omega))
	// Step 10: declination. (Step 9, the obliquity, is in solarIntermediates,
	// which returns the eps used here and by step 11's y.)
	decl = deg(math.Asin(math.Sin(rad(eps)) * math.Sin(rad(lambda))))
	// Step 11: equation of time. l0 is the normalised value and m the
	// un-normalised one, exactly as NOAA does it -- l0's normalisation is
	// numerically inert here because sin 2L0, sin 4L0 and cos 2L0 are all
	// 360-periodic, but m must not be normalised before step 6 uses it.
	etime := y*math.Sin(rad(2*l0)) -
		2*e*math.Sin(rad(m)) +
		4*e*y*math.Sin(rad(m))*math.Cos(rad(2*l0)) -
		0.5*y*y*math.Sin(rad(4*l0)) -
		1.25*e*e*math.Sin(rad(2*m))
	eqTime = deg(etime) * 4.0

	return eqTime, decl
}

// solarIntermediates returns the quantities the rest of the model is built
// from: the normalised geometric mean longitude, the UN-normalised geometric
// mean anomaly, the orbital eccentricity, tan²(ε/2), and the corrected mean
// obliquity ε itself.
//
// ε is returned rather than recomputed by the caller because the declination
// and the equation of time must use the SAME value. They previously used two
// textually identical copies, which nothing kept in step -- issue #167.
// Extracted so the term-presence test can reconstruct individual terms
// without duplicating the series (A.1 steps 3-5, 9, 11).
func solarIntermediates(jd float64) (l0, m, e, y, eps float64) {
	t := (jd - 2451545.0) / 36525.0

	// Step 3: geometric mean longitude, normalised to [0, 360).
	l0 = math.Mod(280.46646+t*(36000.76983+t*0.0003032), 360)
	if l0 < 0 {
		l0 += 360
	}
	// Step 4: geometric mean anomaly. Deliberately NOT normalised -- step 6
	// consumes it as written. (Its T² coefficient is -0.0001537, from Meeus
	// 25.3, the low-accuracy solar series NOAA uses. moon.go's M uses
	// -0.0001536, from Meeus 47.3, the lunar-theory solar argument. These are
	// two different published series, not two ports disagreeing.)
	m = 357.52911 + t*(35999.05029-0.0001537*t)
	// Step 5: eccentricity of Earth's orbit.
	e = 0.016708634 - t*(0.000042037+0.0000001267*t)

	omega := 125.04 - 1934.136*t
	sec := 21.448 - t*(46.8150+t*(0.00059-t*0.001813))
	eps0 := 23.0 + (26.0+sec/60.0)/60.0
	eps = eps0 + 0.00256*math.Cos(rad(omega))
	y = math.Tan(rad(eps/2)) * math.Tan(rad(eps/2))

	return l0, m, e, y, eps
}
