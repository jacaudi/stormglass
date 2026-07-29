package tempestapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	Status struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
	} `json:"status"`
}

// fetchStations performs the GET /stations call and validates the status
// envelope. Behavior is byte-identical to what ListStations did inline.
func (c *Client) fetchStations(ctx context.Context) (*stationsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/stations", nil)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	var data stationsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if data.Status.StatusCode != 0 {
		return nil, &StatusError{
			StatusCode: data.Status.StatusCode,
			Message:    data.Status.StatusMessage,
		}
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
		var deviceId int
		var instance string
		for _, dev := range station.Devices {
			if dev.DeviceType == "ST" {
				deviceId = dev.DeviceID
				instance = dev.SerialNumber
			}
		}

		if deviceId != 0 && instance != "" {
			out = append(out, Station{
				Name:         station.Name,
				DeviceID:     deviceId,
				SerialNumber: instance,
				StationID:    station.StationID,
				CreatedAt:    time.Unix(station.CreatedEpoch, 0),
			})
		}
	}
	return out, nil
}

func (c *Client) GetObservations(ctx context.Context, station Station, startAt time.Time, endAt time.Time) ([]prometheus.Metric, error) {
	url := fmt.Sprintf("%s/observations/device/%d?time_start=%d&time_end=%d", c.baseURL, station.DeviceID, startAt.Unix(), endAt.Unix())
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
		return nil, fmt.Errorf("weatherflow API status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
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
