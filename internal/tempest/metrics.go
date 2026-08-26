// Package tempest holds the Prometheus metric descriptors shared by the UDP
// listener and API-export paths: one *prometheus.Desc per field a Tempest
// station reports, registered once in init() and collected into All for
// callers (e.g. the OTel bridge) that need to enumerate every descriptor
// rather than reference one by name.
package tempest

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Uptime through LightningStrikeCount are the descriptors for every metric
// this exporter emits, split by init() below into device-health (Uptime,
// Rssi, Reboots, BusErrors) and weather-observation groups. See All for the
// flattened slice form.
var (
	Uptime    *prometheus.Desc
	Rssi      *prometheus.Desc
	Reboots   *prometheus.Desc
	BusErrors *prometheus.Desc

	Illuminance          *prometheus.Desc
	UV                   *prometheus.Desc
	RainRate             *prometheus.Desc
	Wind                 *prometheus.Desc // "lull", "avg", "gust", "rapid"
	WindDirection        *prometheus.Desc
	Battery              *prometheus.Desc
	ReportInterval       *prometheus.Desc
	Irradiance           *prometheus.Desc
	RainTotal            *prometheus.Desc
	Pressure             *prometheus.Desc
	Temperature          *prometheus.Desc // "air", "wetbulb"
	Humidity             *prometheus.Desc
	LightningDistance    *prometheus.Desc
	LightningStrikeCount *prometheus.Desc
)

// All is every descriptor above, flattened into one slice and populated by
// init() after each Desc is constructed. Used where a caller needs to
// register or enumerate the full metric set rather than a single field.
var All []*prometheus.Desc

func init() {
	Uptime = prometheus.NewDesc("stormglass_uptime_seconds_total", "The uptime of the device", []string{"instance"}, nil)
	Rssi = prometheus.NewDesc("stormglass_rssi_dbm", "A measurement of wireless signal strength", []string{"instance"}, nil)
	Reboots = prometheus.NewDesc("stormglass_reboots_total", "The number of times the device has rebooted", []string{"instance"}, nil)
	BusErrors = prometheus.NewDesc("stormglass_bus_errors_total", "The number of I2C bus errors experienced by the device", []string{"instance"}, nil)

	Illuminance = prometheus.NewDesc("stormglass_illuminance_lux", "A measurement of luminous flux per unit area", []string{"instance"}, nil)
	UV = prometheus.NewDesc("stormglass_uv_index", "A measurement of ultraviolet light intensity", []string{"instance"}, nil)
	RainRate = prometheus.NewDesc("stormglass_rain_rate_mm_min", "The amount of rain which fell on the sensor in the previous minute", []string{"instance"}, nil)
	Wind = prometheus.NewDesc("stormglass_wind_ms", "A wind speed measurement", []string{"instance", "kind"}, nil)
	WindDirection = prometheus.NewDesc("stormglass_wind_direction_degrees", "The direction from which the wind is blowing", []string{"instance"}, nil)
	Battery = prometheus.NewDesc("stormglass_battery_volts", "The electric potential of the battery", []string{"instance"}, nil)
	ReportInterval = prometheus.NewDesc("stormglass_report_interval_minutes", "Report interval in minutes", []string{"instance"}, nil)
	Irradiance = prometheus.NewDesc("stormglass_irradiance_w_m2", "The total solar irradiance, expressed in watts per square meter", []string{"instance"}, nil)
	RainTotal = prometheus.NewDesc("stormglass_rainfall_total", "The amount of accumulated rain", []string{"instance"}, nil)
	Pressure = prometheus.NewDesc("stormglass_pressure_mb", "Station pressure in millibars", []string{"instance"}, nil)
	Temperature = prometheus.NewDesc("stormglass_temperature_c", "A temperature measurement", []string{"instance", "kind"}, nil)
	Humidity = prometheus.NewDesc("stormglass_humidity_percent", "A relative humidity measurement", []string{"instance"}, nil)
	LightningDistance = prometheus.NewDesc("stormglass_lightning_distance_km", "Average distance of lightning strikes detected in the last minute", []string{"instance"}, nil)
	LightningStrikeCount = prometheus.NewDesc("stormglass_lightning_strike_count", "Number of lightning strikes detected in the last minute", []string{"instance"}, nil)

	All = []*prometheus.Desc{
		Uptime,
		Rssi,
		Reboots,
		BusErrors,

		Illuminance,
		UV,
		RainRate,
		Wind,
		WindDirection,
		Battery,
		ReportInterval,
		Irradiance,
		RainTotal,
		Pressure,
		Temperature,
		Humidity,
		LightningDistance,
		LightningStrikeCount,
	}
}
