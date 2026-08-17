// Package radar selects the nearest WSR-88D (NEXRAD) radar site to a given
// coordinate and provides an allowlist of valid site codes.
package radar

import "math"

// Site is a WSR-88D radar station: its three-letter site code (the ICAO
// identifier with the leading character dropped, e.g. "KTLX" -> "TLX") and
// its location.
type Site struct {
	Code     string
	Lat, Lon float64
}

// earthRadiusKm is used for the haversine great-circle distance calculation.
// Only relative ordering between candidate sites matters for NearestSite, so
// the exact radius value does not affect correctness.
const earthRadiusKm = 6371.0

// NearestSite returns the Code of the WSR-88D site closest to (lat, lon) and
// the great-circle (haversine) distance to it in kilometres.
//
// lat and lon are assumed finite and in range. config.LoadStation
// (internal/config/station.go:158-163) rejects NaN, +/-Inf and out-of-range
// values before any coordinate can reach this function's only production
// caller, so no guard is duplicated here. The note is worth stating because
// the precondition now lives across a package boundary: given a non-finite
// input every distance comparison is false and the zero Site is returned,
// i.e. ("", +Inf).
func NearestSite(lat, lon float64) (code string, distanceKm float64) {
	var nearest Site
	minDist := math.Inf(1)
	for _, s := range sites {
		d := haversineKm(lat, lon, s.Lat, s.Lon)
		if d < minDist {
			minDist = d
			nearest = s
		}
	}
	return nearest.Code, minDist
}

// haversineKm returns the great-circle distance in kilometers between two
// lat/lon points, both given in decimal degrees.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	phi1, phi2 := lat1*math.Pi/180, lat2*math.Pi/180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}

// IsValidSite reports whether code is an exact match for a known WSR-88D
// site code (uppercase, three characters). It is used as an SSRF allowlist
// guard before any code is used to build a downstream request, so it must
// reject anything that is not byte-for-byte equal to a table entry.
func IsValidSite(code string) bool {
	for _, s := range sites {
		if s.Code == code {
			return true
		}
	}
	return false
}
