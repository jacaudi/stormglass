package tempestudp

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"tempestwx-utilities/internal/tempest"

	"github.com/prometheus/client_golang/prometheus"
)

// Docs: https://weatherflow.github.io/Tempest/api/udp/v143/

// Report is a decoded Tempest broadcast (UDP or REST). Metrics converts it
// to the prometheus.Metric values the exporter emits; several report types
// (RainStartReport, DeviceStatusReport) return nil because their fields
// have no corresponding Prometheus metric today.
type Report interface {
	Metrics() []prometheus.Metric
}

// ParseReport decodes bytes into the concrete Report type its "type" field
// selects (see the switch below), returning an error for any message type
// this exporter doesn't understand. It is the single decode path shared by
// the UDP listener and the REST API client's GetObservations.
func ParseReport(bytes []byte) (Report, error) {
	var typ struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(bytes, &typ); err != nil {
		return nil, err
	}

	var data Report
	switch typ.Type {
	case "evt_precip":
		data = &RainStartReport{}
	case "evt_strike":
		data = &LightningStrikeReport{}
	case "rapid_wind":
		data = &RapidWindReport{}
	case "obs_st":
		data = &TempestObservationReport{}
	case "device_status":
		data = &DeviceStatusReport{}
	case "hub_status":
		data = &HubStatusReport{}
	default:
		return nil, fmt.Errorf("unhandled message type: %q", typ.Type)
	}

	if err := json.Unmarshal(bytes, data); err != nil {
		return nil, err
	}
	return data, nil
}

// RainStartReport is the "evt_precip" broadcast, fired once when a hub
// detects the start of rain.
type RainStartReport struct {
	SerialNumber string `json:"serial_number"`

	// "evt_precip"
	Type string `json:"type"`

	HubSn string `json:"hub_sn"`

	// 0	Time Epoch	Seconds
	Evt []float64 `json:"evt"`
}

// Metrics always returns nil: rain-start events have no corresponding
// Prometheus metric today.
func (r RainStartReport) Metrics() []prometheus.Metric {
	return nil
}

// LightningStrikeReport is the "evt_strike" broadcast, fired once per
// lightning strike a hub detects.
type LightningStrikeReport struct {
	SerialNumber string `json:"serial_number"`

	// "evt_strike"
	Type string `json:"type"`

	HubSn string `json:"hub_sn"`

	// 0	Time Epoch	Seconds
	// 1	Distance	km
	// 2	Energy
	Evt []float64 `json:"evt"`
}

// Metrics always returns nil: this discrete event has no corresponding
// Prometheus metric today. Lightning distance/count are instead recovered
// from the aggregate fields on TempestObservationReport.Metrics.
func (r LightningStrikeReport) Metrics() []prometheus.Metric {
	return nil
}

// RapidWindReport is the "rapid_wind" broadcast, sent roughly every three
// seconds with a single wind-speed/direction sample — much higher frequency
// than TempestObservationReport's per-minute report.
type RapidWindReport struct {
	SerialNumber string `json:"serial_number"`

	// "rapid_wind"
	Type string `json:"type"`

	HubSn string    `json:"hub_sn"`
	Ob    []float64 `json:"ob"`
}

// Metrics returns the wind speed/direction pair from Ob, or nil if Ob isn't
// the expected 3-element [epoch, speed, direction] shape.
func (r RapidWindReport) Metrics() []prometheus.Metric {
	if len(r.Ob) != 3 {
		return nil
	}

	ts := int64(r.Ob[0])
	return withTime(ts, []prometheus.Metric{
		prometheus.MustNewConstMetric(tempest.Wind, prometheus.GaugeValue, r.Ob[1], r.SerialNumber, "rapid"),
		prometheus.MustNewConstMetric(tempest.WindDirection, prometheus.GaugeValue, r.Ob[2], r.SerialNumber),
	})
}

// TempestObservationReport is the "obs_st" broadcast, sent about once a
// minute (per Obs field 17: Report Interval), carrying the full
// weather-field set. Per the WeatherFlow wire format, Obs is an array of
// samples rather than a single one; Metrics ranges over every sample
// present rather than assuming exactly one.
type TempestObservationReport struct {
	SerialNumber string `json:"serial_number"`

	// "obs_st"
	Type string `json:"type"`

	HubSn string `json:"hub_sn"`

	// 0	Time Epoch	Seconds
	// 1	Wind Lull (minimum 3 second sample)	m/s
	// 2	Wind Avg (average over report interval)	m/s
	// 3	Wind Gust (maximum 3 second sample)	m/s
	// 4	Wind Direction	Degrees
	// 5	Wind Sample Interval	seconds
	// 6	Station Pressure	MB
	// 7	Air Temperature	C
	// 8	Relative Humidity	%
	// 9	Illuminance	Lux
	// 10	UV	Index
	// 11	Solar Radiation	W/m^2
	// 12	Rain amount over previous minute	mm
	// 13	Precipitation Type	0 = none, 1 = rain, 2 = hail, 3 = rain + hail (experimental)
	// 14	Lightning Strike Avg Distance	km
	// 15	Lightning Strike Count
	// 16	Battery	Volts
	// 17	Report Interval	Minutes
	Obs [][]float64 `json:"obs"`

	FirmwareRevision int `json:"firmware_revision"`
}

// Metrics converts every sample in Obs to its Prometheus metrics, skipping
// samples too short to contain the required fields (index 0-12) and gating
// the wetbulb, lightning, battery, and report-interval metrics on the
// sample actually carrying those optional trailing fields.
func (r TempestObservationReport) Metrics() []prometheus.Metric {
	var out []prometheus.Metric

	for _, ob := range r.Obs {
		if len(ob) < 13 {
			continue
		}

		wetBulb := WetBulbTemperatureC(ob[7], ob[8], ob[6])

		metrics := []prometheus.Metric{
			prometheus.MustNewConstMetric(tempest.Wind, prometheus.GaugeValue, ob[1], r.SerialNumber, "lull"),
			prometheus.MustNewConstMetric(tempest.Wind, prometheus.GaugeValue, ob[2], r.SerialNumber, "avg"),
			prometheus.MustNewConstMetric(tempest.Wind, prometheus.GaugeValue, ob[3], r.SerialNumber, "gust"),
			prometheus.MustNewConstMetric(tempest.WindDirection, prometheus.GaugeValue, ob[4], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.Pressure, prometheus.GaugeValue, ob[6], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.Temperature, prometheus.GaugeValue, ob[7], r.SerialNumber, "air"),
		}

		// WetBulbTemperatureC returns NaN for non-convergent inputs (e.g.
		// physically impossible humidity/pressure from a malformed report);
		// skip emitting the metric rather than publishing NaN.
		if !math.IsNaN(wetBulb) {
			metrics = append(metrics,
				prometheus.MustNewConstMetric(tempest.Temperature, prometheus.GaugeValue, wetBulb, r.SerialNumber, "wetbulb"),
			)
		}

		metrics = append(metrics,
			prometheus.MustNewConstMetric(tempest.Humidity, prometheus.GaugeValue, ob[8], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.Illuminance, prometheus.GaugeValue, ob[9], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.UV, prometheus.GaugeValue, ob[10], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.Irradiance, prometheus.GaugeValue, ob[11], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.RainRate, prometheus.GaugeValue, ob[12], r.SerialNumber),
		)

		// Lightning metrics (fields 14 and 15)
		if len(ob) >= 16 {
			metrics = append(metrics,
				prometheus.MustNewConstMetric(tempest.LightningDistance, prometheus.GaugeValue, ob[14], r.SerialNumber),
				prometheus.MustNewConstMetric(tempest.LightningStrikeCount, prometheus.GaugeValue, ob[15], r.SerialNumber),
			)
		}

		if len(ob) >= 17 {
			metrics = append(metrics,
				prometheus.MustNewConstMetric(tempest.Battery, prometheus.GaugeValue, ob[16], r.SerialNumber),
			)
		}
		if len(ob) >= 18 {
			metrics = append(metrics,
				prometheus.MustNewConstMetric(tempest.ReportInterval, prometheus.GaugeValue, ob[17], r.SerialNumber),
			)
		}

		out = append(out, withTime(int64(ob[0]), metrics)...)
	}

	return out
}

// DeviceStatusReport is the "device_status" broadcast, sent roughly once a
// minute with the sensor's own health (voltage, RSSI, firmware, sensor
// fault bits) — distinct from HubStatusReport, which covers the hub itself.
type DeviceStatusReport struct {
	SerialNumber string `json:"serial_number"`

	// "device_status"
	Type string `json:"type"`

	HubSn            string  `json:"hub_sn"`
	Timestamp        int     `json:"timestamp"`
	Uptime           int     `json:"uptime"`
	Voltage          float64 `json:"voltage"`
	FirmwareRevision int     `json:"firmware_revision"`
	Rssi             int     `json:"rssi"`
	HubRssi          int     `json:"hub_rssi"`

	// Binary Value	Applies to device	Status description
	// 0b000000000	All	Sensors OK
	// 0b000000001	AIR, Tempest	lightning failed
	// 0b000000010	AIR, Tempest	lightning noise
	// 0b000000100	AIR, Tempest	lightning disturber
	// 0b000001000	AIR, Tempest	pressure failed
	// 0b000010000	AIR, Tempest	temperature failed
	// 0b000100000	AIR, Tempest	rh failed
	// 0b001000000	SKY, Tempest	wind failed
	// 0b010000000	SKY, Tempest	precip failed
	// 0b100000000	SKY, Tempest	light/uv failed
	// any bits above 0b100000000 are reserved for internal use and should be ignored
	SensorStatus int `json:"sensor_status"`

	// 0	Debugging is disabled
	// 1	Debugging is enabled
	Debug int `json:"debug"`
}

// Metrics always returns nil: device health has no corresponding
// Prometheus metric today — HubStatusReport.Metrics covers the hub's
// analogous fields (uptime, RSSI, reboots, bus errors) instead.
func (r DeviceStatusReport) Metrics() []prometheus.Metric {
	return nil
}

// HubStatusReport is the "hub_status" broadcast, sent roughly once a minute
// with the hub's own health (uptime, RSSI, reboot/bus-error counts via
// RadioStats) — distinct from DeviceStatusReport, which covers an
// individual sensor.
type HubStatusReport struct {
	SerialNumber string `json:"serial_number"`

	// "hub_status"
	Type string `json:"type"`

	FirmwareRevision string  `json:"firmware_revision"`
	Uptime           float64 `json:"uptime"`
	Rssi             float64 `json:"rssi"`
	Timestamp        int64   `json:"timestamp"`

	// BOR	Brownout reset
	// PIN	PIN reset
	// POR	Power reset
	// SFT	Software reset
	// WDG	Watchdog reset
	// WWD	Window watchdog reset
	// LPW	Low-power reset
	ResetFlags string `json:"reset_flags"`

	Seq int   `json:"seq"`
	Fs  []int `json:"fs"`

	// 0	Version
	// 1	Reboot Count
	// 2	I2C Bus Error Count
	// 3	Radio Status (0 = Radio Off, 1 = Radio On, 3 = Radio Active)
	// 4	Radio Network ID
	RadioStats []float64 `json:"radio_stats"`

	MqttStats []int `json:"mqtt_stats"`
}

// Metrics returns uptime and RSSI unconditionally, plus reboot/bus-error
// counts when RadioStats carries at least indices 0-2 (a malformed or
// short array must not panic — see the guard below).
func (r HubStatusReport) Metrics() []prometheus.Metric {
	metrics := []prometheus.Metric{
		prometheus.MustNewConstMetric(tempest.Uptime, prometheus.CounterValue, r.Uptime, r.SerialNumber),
		prometheus.MustNewConstMetric(tempest.Rssi, prometheus.GaugeValue, r.Rssi, r.SerialNumber),
	}

	// radio_stats[1] and [2] (reboots, bus errors) are only present on a
	// well-formed hub_status broadcast; a malformed/short array must not panic.
	if len(r.RadioStats) >= 3 {
		metrics = append(metrics,
			prometheus.MustNewConstMetric(tempest.Reboots, prometheus.CounterValue, r.RadioStats[1], r.SerialNumber),
			prometheus.MustNewConstMetric(tempest.BusErrors, prometheus.CounterValue, r.RadioStats[2], r.SerialNumber),
		)
	}

	return withTime(r.Timestamp, metrics)
}

func withTime(unix int64, metrics []prometheus.Metric) []prometheus.Metric {
	t := time.Unix(unix, 0)
	out := make([]prometheus.Metric, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, prometheus.NewMetricWithTimestamp(t, m))
	}
	return out
}
