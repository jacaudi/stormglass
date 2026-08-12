// Package tempestapi is a REST client for the WeatherFlow Tempest API: station
// and device discovery (ListStations, ListDevices), historical observation
// fetches decoded into the same tempestudp.Report types the UDP listener
// produces (GetObservations), and a raw-JSON passthrough (proxy.go) for the
// httpserver's read-through cache. internal/backfill is the primary consumer
// of the device-preserving ListDevices path; main.go's API-export mode uses
// the station-collapsing ListStations path instead — see ListStations'
// comment for why the two intentionally disagree.
package tempestapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"time"

	"tempestwx-utilities/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
)

// defaultBaseURL is the production WeatherFlow REST host. It is the single
// source for every request this client builds (ListStations, GetObservations,
// Proxy) -- all three would need to change together if WeatherFlow's host
// ever changed, so it lives once here rather than being hardcoded per method.
const defaultBaseURL = "https://swd.weatherflow.com/swd/rest"

// Client is an authenticated WeatherFlow REST client. The zero value is not
// usable — construct one with NewClient, which fills in the auth token and
// production base URL.
type Client struct {
	token   string
	http    *http.Client
	baseURL string
}

// ClientOption configures a Client built by NewClient.
type ClientOption func(*Client)

// WithBaseURL overrides the WeatherFlow REST base URL. Production never sets
// this (the zero-arg NewClient(token) call already points at WeatherFlow);
// it exists so tests can redirect the client at an httptest.Server.
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

// NewClient returns a Client authenticated with token, pointed at the
// production WeatherFlow API with a 30s request timeout. Options (currently
// only WithBaseURL, used by tests) apply after those defaults.
func NewClient(token string, opts ...ClientOption) *Client {
	c := &Client{
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: defaultBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// get performs an authenticated GET against url and returns the response
// body. It is the single source for the HTTP transport contract shared by
// fetchStations, GetObservations, and Observations: set the Bearer auth
// header, cap the body at 10 MiB, and classify a non-200 response as a
// *StatusError. Those three call sites used to duplicate this block, and the
// duplication had already produced an untested copy of the auth header — an
// edit that deleted it from Observations left the entire suite green. Response
// decoding stays with each caller; no response type enters this signature.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{HTTPStatus: resp.StatusCode}
	}

	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

// Station identifies one ST (Tempest) device: which station it belongs to,
// its device ID (used to fetch observations), and the serial number that
// correlates it with rows this device's UDP broadcasts have already written
// to a store. Returned by ListStations and ListDevices — see ListDevices'
// comment for how the two differ on multi-device stations.
type Station struct {
	Name         string
	StationID    int
	DeviceID     int
	SerialNumber string
	CreatedAt    time.Time
}

// stationsResponse is the /stations payload. ListStations and ListDevices
// both decode into it, so the JSON contract lives in exactly one place.
type stationsResponse struct {
	statusEnvelope
	Stations []struct {
		CreatedEpoch int64 `json:"created_epoch"`
		Devices      []struct {
			DeviceID     int    `json:"device_id"`
			DeviceType   string `json:"device_type"`
			SerialNumber string `json:"serial_number"`
		} `json:"devices"`
		Name      string `json:"name"`
		StationID int    `json:"station_id"`
	} `json:"stations"`
}

// fetchStations performs the GET /stations call and validates the status
// envelope. Behavior is byte-identical to what ListStations did inline.
func (c *Client) fetchStations(ctx context.Context) (*stationsResponse, error) {
	body, err := c.get(ctx, c.baseURL+"/stations")
	if err != nil {
		return nil, err
	}

	var data stationsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if err := data.err(); err != nil {
		return nil, err
	}
	return &data, nil
}

// ListDevices returns one Station per ST device across all stations.
//
// It exists because ListStations collapses each station to a SINGLE ST device
// (its loop has no break, so the last one wins). With two Tempest units on one
// station that silently loses a sensor — the UDP listener records both serials,
// but a caller driven by ListStations would only ever learn one, and the other
// unit's gaps would never close, unlogged.
//
// ListStations is deliberately left with its collapsing behavior: ModeAPIExport
// depends on it, and changing it is an explicit non-goal.
func (c *Client) ListDevices(ctx context.Context) ([]Station, error) {
	data, err := c.fetchStations(ctx)
	if err != nil {
		return nil, err
	}
	var out []Station
	for _, station := range data.Stations {
		for _, dev := range station.Devices {
			if dev.DeviceType != "ST" {
				continue
			}
			// A phantom device (zero ID or no serial) is deliberately still
			// RETURNED, never skipped: the API can answer 200/status_code=0
			// with an empty obs window for such a device, which is
			// byte-identical to the permanent-hole signal a real gap
			// produces. Dropping it here would silently swap "this sensor
			// doesn't exist" for "this sensor has no gaps" -- so the
			// assumption is made self-verifying with a WARN instead.
			if dev.DeviceID == 0 || dev.SerialNumber == "" {
				slog.Warn("tempestapi: ST device has a malformed identifier",
					"station", station.Name, "station_id", station.StationID,
					"device_id", dev.DeviceID, "serial_number", dev.SerialNumber)
			}
			out = append(out, Station{
				Name:         station.Name,
				StationID:    station.StationID,
				DeviceID:     dev.DeviceID,
				SerialNumber: dev.SerialNumber,
				CreatedAt:    time.Unix(station.CreatedEpoch, 0),
			})
		}
	}
	return out, nil
}

// ListStations returns one Station per station, collapsing each station's
// devices to a SINGLE ST device — the last one seen, because the loop below
// has no break. That behavior is load-bearing for ModeAPIExport and is
// deliberately preserved; backfill uses ListDevices instead.
func (c *Client) ListStations(ctx context.Context) ([]Station, error) {
	data, err := c.fetchStations(ctx)
	if err != nil {
		return nil, err
	}

	var out []Station
	for _, station := range data.Stations {
		var deviceID int
		var instance string
		for _, dev := range station.Devices {
			if dev.DeviceType == "ST" {
				deviceID = dev.DeviceID
				instance = dev.SerialNumber
			}
		}

		if deviceID != 0 && instance != "" {
			out = append(out, Station{
				Name:         station.Name,
				DeviceID:     deviceID,
				SerialNumber: instance,
				StationID:    station.StationID,
				CreatedAt:    time.Unix(station.CreatedEpoch, 0),
			})
		}
	}
	return out, nil
}

// GetObservations fetches station's observations in [startAt, endAt] and
// decodes them through the same tempestudp.ParseReport path the UDP
// listener uses, so backfilled and live rows share one decode
// implementation. station.SerialNumber is stamped onto the decoded report
// because the API response itself carries no serial number.
//
// A non-zero WeatherFlow envelope status_code is an error here. That IS an
// observable change for ModeAPIExport: main.go's export loop calls os.Exit(1)
// on any error from this function, so a window that used to contribute zero
// observations and continue now aborts the run. That is deliberate — the loop
// already aborts on 401/429/5xx and on parse failures, so an application-level
// failure joining that set is consistent, and a silently-empty export window
// is the data loss this reports rather than hides.
func (c *Client) GetObservations(ctx context.Context, station Station, startAt time.Time, endAt time.Time) ([]prometheus.Metric, error) {
	url := fmt.Sprintf("%s/observations/device/%d?time_start=%d&time_end=%d", c.baseURL, station.DeviceID, startAt.Unix(), endAt.Unix())
	// A non-200 response now surfaces as *StatusError rather than a plain
	// fmt.Errorf. StatusError.Error()'s HTTP branch renders the byte-identical
	// "weatherflow API status %d" string, so this is not an observable
	// behavior change for ModeAPIExport, which only formats the error with
	// %v and never type-asserts on it (main.go's export loop).
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}

	// The envelope is checked BEFORE ParseReport, because ParseReport
	// dispatches on the top-level "type" and cannot see status at all. Two
	// distinct failures collapse into this one check: a status-only error
	// envelope (no "type") used to surface as `unhandled message type: ""`,
	// which no caller can classify, and a typed error envelope with an empty
	// obs array used to surface as zero metrics and no error at all.
	var env statusEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode observation envelope: %w", err)
	}
	if err := env.err(); err != nil {
		return nil, err
	}

	report, err := tempestudp.ParseReport(body)
	if err != nil {
		log.Printf("read %s", string(body))
		return nil, err
	}

	switch r := report.(type) {
	case *tempestudp.TempestObservationReport:
		r.SerialNumber = station.SerialNumber
	default:
		return nil, fmt.Errorf("unhandled report type %T", report)
	}

	metrics := report.Metrics()
	return metrics, nil
}
