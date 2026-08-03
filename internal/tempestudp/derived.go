// Package tempestudp decodes raw Tempest UDP broadcast JSON into typed
// Report values (report.go) and converts them to Prometheus metrics, plus
// the derived-quantity calculations (dew point here, wet bulb in
// wetbulb.go) the UDP payload doesn't carry directly. It is the shared
// decode path for both the live UDP listener and the REST API's historical
// observations (see tempestapi.Client.GetObservations), so backfilled and
// live rows go through identical parsing.
package tempestudp

import "math"

// DewPointC computes the dew point temperature in Celsius from air
// temperature and relative humidity using the Magnus-Tetens approximation
// with the Alduchov-Eskridge (1996) / Lawrence (2005) constants a=17.625,
// b=243.04°C, which is accurate to within about 0.35°C over -40°C to 50°C
// for humidity above ~1%:
//
//	γ  = ln(RH/100) + a*T/(b+T)
//	Td = b*γ/(a-γ)
//
// Inputs and output are both SI (°C, %).
func DewPointC(temperatureC, humidityPercent float64) float64 {
	const a, b = 17.625, 243.04
	gamma := math.Log(humidityPercent/100) + a*temperatureC/(b+temperatureC)
	return b * gamma / (a - gamma)
}

// HeatIndexC computes the NWS "feels like" heat index in Celsius from air
// temperature and relative humidity via the Rothfusz regression (computed in
// Fahrenheit, per the NWS coefficients, then converted back to Celsius):
// https://www.wpc.ncep.noaa.gov/html/heatindex_equation.shtml
//
//	HI = -42.379 + 2.04901523*T + 10.14333127*RH - 0.22475541*T*RH
//	     - 0.00683783*T*T - 0.05481717*RH*RH + 0.00122874*T*T*RH
//	     + 0.00085282*T*RH*RH - 0.00000199*T*T*RH*RH
//
// Below 80°F (26.7°C), the NWS heat index is approximately the air
// temperature itself; this implementation returns the input temperature
// unchanged in that regime. This is a deliberate simplification of the full
// NWS algorithm, which instead averages a simpler formula with T and only
// falls through to the full regression if that average reaches 80°F — the
// difference between the two below the threshold is negligible for this
// exporter's purposes, and the low/high-RH regression adjustments (also part
// of the full NWS algorithm, applying only at RH<13% or RH>85% within
// 80-112°F) are omitted for the same reason: Contract B needs a single
// SI-in/SI-out heat index value, not the full NWS caveat table.
func HeatIndexC(temperatureC, humidityPercent float64) float64 {
	tF := temperatureC*9/5 + 32
	if tF < 80 {
		return temperatureC
	}

	rh := humidityPercent
	hiF := -42.379 + 2.04901523*tF + 10.14333127*rh - 0.22475541*tF*rh -
		0.00683783*tF*tF - 0.05481717*rh*rh + 0.00122874*tF*tF*rh +
		0.00085282*tF*rh*rh - 0.00000199*tF*tF*rh*rh

	return (hiF - 32) * 5 / 9
}
