package tempestapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockRoundTripper allows us to mock HTTP responses without running a server
type mockRoundTripper struct {
	response *http.Response
	err      error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// Helper to create a mock HTTP response
func mockResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestNewClient(t *testing.T) {
	token := "test-token-123"
	client := NewClient(token)

	if client.token != token {
		t.Errorf("Expected token %s, got %s", token, client.token)
	}
}

// ============================================================================
// ListStations Tests
// ============================================================================

func TestListStations_Success_SingleStation(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Test Station",
				"station_id": 12345,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 67890,
						"device_type": "ST",
						"serial_number": "ST-00012345"
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(stations) != 1 {
		t.Fatalf("Expected 1 station, got %d", len(stations))
	}

	station := stations[0]
	if station.Name != "Test Station" {
		t.Errorf("Expected name 'Test Station', got %s", station.Name)
	}
	if station.StationID != 12345 {
		t.Errorf("Expected station ID 12345, got %d", station.StationID)
	}
	if station.DeviceID != 67890 {
		t.Errorf("Expected device ID 67890, got %d", station.DeviceID)
	}
	if station.SerialNumber != "ST-00012345" {
		t.Errorf("Expected serial number 'ST-00012345', got %s", station.SerialNumber)
	}

	expectedTime := time.Unix(1609459200, 0)
	if !station.CreatedAt.Equal(expectedTime) {
		t.Errorf("Expected created at %v, got %v", expectedTime, station.CreatedAt)
	}
}

func TestListStations_Success_MultipleStations(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Station One",
				"station_id": 11111,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 10001,
						"device_type": "ST",
						"serial_number": "ST-00011111"
					}
				]
			},
			{
				"name": "Station Two",
				"station_id": 22222,
				"created_epoch": 1609545600,
				"devices": [
					{
						"device_id": 20002,
						"device_type": "ST",
						"serial_number": "ST-00022222"
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(stations) != 2 {
		t.Fatalf("Expected 2 stations, got %d", len(stations))
	}

	// Verify first station
	if stations[0].Name != "Station One" {
		t.Errorf("Expected first station name 'Station One', got %s", stations[0].Name)
	}
	if stations[0].StationID != 11111 {
		t.Errorf("Expected first station ID 11111, got %d", stations[0].StationID)
	}

	// Verify second station
	if stations[1].Name != "Station Two" {
		t.Errorf("Expected second station name 'Station Two', got %s", stations[1].Name)
	}
	if stations[1].StationID != 22222 {
		t.Errorf("Expected second station ID 22222, got %d", stations[1].StationID)
	}
}

func TestListStations_Success_MultipleDevicesPerStation(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Mixed Device Station",
				"station_id": 12345,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 11111,
						"device_type": "HB",
						"serial_number": "HB-00011111"
					},
					{
						"device_id": 67890,
						"device_type": "ST",
						"serial_number": "ST-00012345"
					},
					{
						"device_id": 22222,
						"device_type": "AR",
						"serial_number": "AR-00022222"
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(stations) != 1 {
		t.Fatalf("Expected 1 station, got %d", len(stations))
	}

	// Should pick the ST device, not HB or AR
	station := stations[0]
	if station.DeviceID != 67890 {
		t.Errorf("Expected device ID 67890 (ST device), got %d", station.DeviceID)
	}
	if station.SerialNumber != "ST-00012345" {
		t.Errorf("Expected serial number 'ST-00012345', got %s", station.SerialNumber)
	}
}

func TestListStations_NoSTDevice(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Hub Only Station",
				"station_id": 12345,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 11111,
						"device_type": "HB",
						"serial_number": "HB-00011111"
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should return empty slice when no ST devices found
	if len(stations) != 0 {
		t.Errorf("Expected 0 stations, got %d", len(stations))
	}
}

func TestListStations_EmptyStations(t *testing.T) {
	mockBody := `{
		"stations": [],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(stations) != 0 {
		t.Errorf("Expected 0 stations, got %d", len(stations))
	}
}

func TestListStations_MixedValidAndInvalid(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "No ST Device",
				"station_id": 11111,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 99999,
						"device_type": "HB",
						"serial_number": "HB-00099999"
					}
				]
			},
			{
				"name": "Valid Station",
				"station_id": 22222,
				"created_epoch": 1609545600,
				"devices": [
					{
						"device_id": 67890,
						"device_type": "ST",
						"serial_number": "ST-00022222"
					}
				]
			},
			{
				"name": "Empty Devices",
				"station_id": 33333,
				"created_epoch": 1609632000,
				"devices": []
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should only return the valid station
	if len(stations) != 1 {
		t.Fatalf("Expected 1 station, got %d", len(stations))
	}

	if stations[0].Name != "Valid Station" {
		t.Errorf("Expected 'Valid Station', got %s", stations[0].Name)
	}
}

func TestListStations_InvalidJSON(t *testing.T) {
	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, "invalid json{{{")
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	_, err := client.ListStations(context.Background())

	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestListStations_HTTPError(t *testing.T) {
	client := NewClient("test-token")
	client.http.Transport = &mockRoundTripper{
		err: errors.New("network error"),
	}

	_, err := client.ListStations(context.Background())

	if err == nil {
		t.Error("Expected network error, got nil")
	}
}

func TestListStations_ContextCancellation(t *testing.T) {
	client := NewClient("test-token")
	client.http.Transport = &mockRoundTripper{
		err: context.Canceled,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.ListStations(ctx)

	if err == nil {
		t.Error("Expected context cancellation error, got nil")
	}
}

// ============================================================================
// GetObservations Tests
// ============================================================================

func TestGetObservations_Success(t *testing.T) {
	// Valid obs_st response with proper data structure
	mockBody := `{
		"type": "obs_st",
		"serial_number": "ORIGINAL-SERIAL",
		"hub_sn": "HB-00001234",
		"obs": [
			[1609459200, 0.5, 1.2, 2.3, 180, 3, 1013.25, 20.5, 65, 50000, 3, 500, 0, 0, 0, 0, 2.6, 1]
		],
		"firmware_revision": 143
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	station := Station{
		Name:         "Test Station",
		StationID:    12345,
		DeviceID:     67890,
		SerialNumber: "ST-00012345",
		CreatedAt:    time.Now(),
	}

	startTime := time.Unix(1609459000, 0)
	endTime := time.Unix(1609459300, 0)

	metrics, err := client.GetObservations(context.Background(), station, startTime, endTime)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(metrics) == 0 {
		t.Error("Expected metrics to be returned, got none")
	}

	// Verify that metrics were created (exact count depends on tempestudp.TempestObservationReport.Metrics())
	// We can't easily verify the serial number was set without accessing internal fields,
	// but the fact that metrics were created without error indicates success
}

func TestGetObservations_MultipleObservations(t *testing.T) {
	// Response with multiple observations
	mockBody := `{
		"type": "obs_st",
		"serial_number": "ORIGINAL-SERIAL",
		"hub_sn": "HB-00001234",
		"obs": [
			[1609459200, 0.5, 1.2, 2.3, 180, 3, 1013.25, 20.5, 65, 50000, 3, 500, 0, 0, 0, 0, 2.6, 1],
			[1609459260, 0.6, 1.3, 2.4, 185, 3, 1013.30, 20.6, 66, 51000, 3, 510, 0.1, 0, 0, 0, 2.6, 1],
			[1609459320, 0.7, 1.4, 2.5, 190, 3, 1013.35, 20.7, 67, 52000, 3, 520, 0.2, 0, 0, 0, 2.6, 1]
		],
		"firmware_revision": 143
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	station := Station{
		DeviceID:     67890,
		SerialNumber: "ST-00012345",
	}

	metrics, err := client.GetObservations(context.Background(), station, time.Now(), time.Now())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(metrics) == 0 {
		t.Error("Expected metrics from multiple observations, got none")
	}
}

func TestGetObservations_HTTPError(t *testing.T) {
	client := NewClient("test-token")
	client.http.Transport = &mockRoundTripper{
		err: errors.New("network error"),
	}

	station := Station{DeviceID: 67890, SerialNumber: "ST-00012345"}

	_, err := client.GetObservations(context.Background(), station, time.Now(), time.Now())

	if err == nil {
		t.Error("Expected network error, got nil")
	}
}

func TestGetObservations_InvalidJSON(t *testing.T) {
	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, "not valid json")
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	station := Station{DeviceID: 67890, SerialNumber: "ST-00012345"}

	_, err := client.GetObservations(context.Background(), station, time.Now(), time.Now())

	if err == nil {
		t.Error("Expected JSON parsing error, got nil")
	}
}

func TestGetObservations_ContextCancellation(t *testing.T) {
	client := NewClient("test-token")
	client.http.Transport = &mockRoundTripper{
		err: context.Canceled,
	}

	station := Station{DeviceID: 67890, SerialNumber: "ST-00012345"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.GetObservations(ctx, station, time.Now(), time.Now())

	if err == nil {
		t.Error("Expected context cancellation error, got nil")
	}
}

func TestGetObservations_EmptyObservations(t *testing.T) {
	// Valid response but with empty observations array
	mockBody := `{
		"type": "obs_st",
		"serial_number": "ST-00012345",
		"hub_sn": "HB-00001234",
		"obs": [],
		"firmware_revision": 143
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	station := Station{DeviceID: 67890, SerialNumber: "ST-00012345"}

	metrics, err := client.GetObservations(context.Background(), station, time.Now(), time.Now())

	if err != nil {
		t.Fatalf("Expected no error for empty observations, got %v", err)
	}

	// Empty observations should return empty metrics
	if len(metrics) != 0 {
		t.Errorf("Expected 0 metrics for empty observations, got %d", len(metrics))
	}
}

// ============================================================================
// Edge Case Tests
// ============================================================================

func TestListStations_StationWithZeroDeviceID(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Weird Station",
				"station_id": 12345,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 0,
						"device_type": "ST",
						"serial_number": "ST-00012345"
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// deviceId == 0 should be filtered out (line 76 check)
	if len(stations) != 0 {
		t.Errorf("Expected 0 stations (device ID is 0), got %d", len(stations))
	}
}

func TestListStations_StationWithEmptySerialNumber(t *testing.T) {
	mockBody := `{
		"stations": [
			{
				"name": "Missing Serial",
				"station_id": 12345,
				"created_epoch": 1609459200,
				"devices": [
					{
						"device_id": 67890,
						"device_type": "ST",
						"serial_number": ""
					}
				]
			}
		],
		"status": {
			"status_code": 0,
			"status_message": "SUCCESS"
		}
	}`

	client := NewClient("test-token")
	resp := mockResponse(http.StatusOK, mockBody)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	stations, err := client.ListStations(context.Background())

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Empty serial number should be filtered out (line 76 check)
	if len(stations) != 0 {
		t.Errorf("Expected 0 stations (serial number is empty), got %d", len(stations))
	}
}

func TestGetObservations_URLParameters(t *testing.T) {
	// Test that URL is constructed correctly and auth is sent via header, not URL
	var capturedURL string
	var capturedAuth string

	client := NewClient("my-secret-token")
	client.http.Transport = mockRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		capturedAuth = req.Header.Get("Authorization")

		mockResp := `{
			"type": "obs_st",
			"serial_number": "ORIG",
			"hub_sn": "HB-00001234",
			"obs": [[1609459200, 0.5, 1.2, 2.3, 180, 3, 1013.25, 20.5, 65, 50000, 3, 500, 0, 0, 0, 0, 2.6, 1]],
			"firmware_revision": 143
		}`

		return mockResponse(http.StatusOK, mockResp), nil
	})

	station := Station{
		DeviceID:     98765,
		SerialNumber: "ST-00098765",
	}

	startTime := time.Unix(1609459000, 0)
	endTime := time.Unix(1609459300, 0)

	_, err := client.GetObservations(context.Background(), station, startTime, endTime)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify URL contains all required parameters, but never the token
	if !strings.Contains(capturedURL, "device/98765") {
		t.Errorf("URL should contain device ID 98765: %s", capturedURL)
	}
	if strings.Contains(capturedURL, "token=") {
		t.Errorf("URL must not contain a token query parameter: %s", capturedURL)
	}
	if capturedAuth != "Bearer my-secret-token" {
		t.Errorf("Authorization header = %q, want %q", capturedAuth, "Bearer my-secret-token")
	}
	if !strings.Contains(capturedURL, "time_start=1609459000") {
		t.Errorf("URL should contain start time: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "time_end=1609459300") {
		t.Errorf("URL should contain end time: %s", capturedURL)
	}
}

// mockRoundTripperFunc allows using a function as a RoundTripper
type mockRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f mockRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ============================================================================
// Hardening Tests (F-H1..H4, F-MEDIUM)
// ============================================================================

// redirectTransport rewrites the outgoing request's scheme/host to point at a
// local httptest.Server, so the real request built by the client (headers,
// path, query) is delivered over the wire to a real server for inspection.
type redirectTransport struct {
	target *url.URL
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// newRedirectingClient returns a Client whose requests are transparently
// redirected to server, so tests can assert on the actual request the client
// sends while getting a response from a real httptest.Server.
func newRedirectingClient(token string, server *httptest.Server) *Client {
	c := NewClient(token)
	target, err := url.Parse(server.URL)
	if err != nil {
		panic(err)
	}
	c.http.Transport = &redirectTransport{target: target}
	return c
}

func TestClient_UsesBearerHeader_NotURLToken(t *testing.T) {
	var gotAuth, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"stations":[],"status":{"status_code":0,"status_message":"SUCCESS"}}`))
	}))
	defer server.Close()

	client := newRedirectingClient("secret-token", server)

	if _, err := client.ListStations(context.Background()); err != nil {
		t.Fatalf("ListStations() error = %v", err)
	}

	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if strings.Contains(gotQuery, "token=") {
		t.Errorf("request URL query %q must not contain a token parameter", gotQuery)
	}
}

func TestClient_ReturnsErrorOn401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newRedirectingClient("secret-token", server)

	if _, err := client.ListStations(context.Background()); err == nil {
		t.Error("ListStations() error = nil, want error mentioning status 401")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("ListStations() error = %v, want it to mention status 401", err)
	}

	station := Station{DeviceID: 1, SerialNumber: "ST-1"}
	if _, err := client.GetObservations(context.Background(), station, time.Now(), time.Now()); err == nil {
		t.Error("GetObservations() error = nil, want error mentioning status 401")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("GetObservations() error = %v, want it to mention status 401", err)
	}
}

func TestClient_HasTimeout(t *testing.T) {
	client := NewClient("secret-token")

	if client.http.Timeout != 30*time.Second {
		t.Errorf("http.Timeout = %v, want %v", client.http.Timeout, 30*time.Second)
	}
}

func TestClient_UnhandledReportType_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"rapid_wind","serial_number":"ST-1","hub_sn":"HB-1","ob":[1609459200,1.2,180]}`))
	}))
	defer server.Close()

	client := newRedirectingClient("secret-token", server)
	station := Station{DeviceID: 1, SerialNumber: "ST-1"}

	_, err := client.GetObservations(context.Background(), station, time.Now(), time.Now())
	if err == nil {
		t.Fatal("GetObservations() error = nil, want error for unhandled report type")
	}
}

func TestClient_ChecksStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stations":[],"status":{"status_code":2,"status_message":"BAD TOKEN"}}`))
	}))
	defer server.Close()

	client := newRedirectingClient("secret-token", server)

	_, err := client.ListStations(context.Background())
	if err == nil {
		t.Fatal("ListStations() error = nil, want error for non-zero status_code")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("ListStations() error = %v, want it to mention status_code 2", err)
	}
}

// GetObservations must classify a non-zero WeatherFlow envelope status_code as
// a *StatusError, exactly as fetchStations and Observations already do.
//
// Both response shapes are covered deliberately, because they fail differently
// today and only one of them matches the way issue #49 describes the gap:
// without the top-level "type" discriminator ParseReport already rejects the
// body, but with it the envelope is ignored and the caller sees zero metrics
// and no error.
func TestGetObservations_APIStatusFailure_ClassifiesAsStatusError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "status-only envelope",
			body: `{"status":{"status_code":2,"status_message":"BAD TOKEN"}}`,
		},
		{
			name: "typed envelope with empty obs",
			body: `{"type":"obs_st","status":{"status_code":2,"status_message":"BAD TOKEN"},"obs":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c := NewClient("test-token", WithBaseURL(srv.URL))

			station := Station{DeviceID: 1, SerialNumber: "ST-1"}
			_, err := c.GetObservations(t.Context(), station, time.Unix(0, 0), time.Unix(60, 0))
			if err == nil {
				t.Fatal("GetObservations() error = nil, want a *StatusError for non-zero status_code")
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("GetObservations() error = %v (%T), want errors.As to find a *StatusError", err, err)
			}
			if se.StatusCode != 2 {
				t.Errorf("StatusError.StatusCode = %d, want 2", se.StatusCode)
			}
			if se.Message != "BAD TOKEN" {
				t.Errorf("StatusError.Message = %q, want %q", se.Message, "BAD TOKEN")
			}
		})
	}
}

func TestListDevicesReturnsEverySTDevice(t *testing.T) {
	// One station, TWO Tempest sensors, plus a hub that must be ignored.
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
		"station_id": 1, "name": "Home", "created_epoch": 1600000000,
		"devices": [
			{"device_id": 11, "device_type": "HB", "serial_number": "HB-00000001"},
			{"device_id": 22, "device_type": "ST", "serial_number": "ST-00000022"},
			{"device_id": 33, "device_type": "ST", "serial_number": "ST-00000033"}
		]
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	devices, err := c.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2 (both ST sensors, hub excluded)", len(devices))
	}
	got := map[string]int{}
	for _, d := range devices {
		got[d.SerialNumber] = d.DeviceID
		if !d.CreatedAt.Equal(time.Unix(1600000000, 0)) {
			t.Errorf("%s CreatedAt = %v, want the owning station's created_epoch", d.SerialNumber, d.CreatedAt)
		}
	}
	if got["ST-00000022"] != 22 || got["ST-00000033"] != 33 {
		t.Errorf("device map = %v, want ST-00000022→22 and ST-00000033→33", got)
	}
}

// ListDevices must not silently accept a malformed ST entry. The API can
// legitimately answer 200/status_code=0 with an empty obs window for a
// phantom device, which is byte-identical to the permanent-hole signal a real
// gap produces — so a dropped/malformed sensor must self-report via a WARN,
// not vanish quietly.
func TestListDevicesWarnsOnMalformedEntry(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty serial number",
			body: `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
				"station_id": 1, "name": "Home", "created_epoch": 1600000000,
				"devices": [{"device_id": 22, "device_type": "ST", "serial_number": ""}]
			}]}`,
		},
		{
			name: "zero device id",
			body: `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
				"station_id": 1, "name": "Home", "created_epoch": 1600000000,
				"devices": [{"device_id": 0, "device_type": "ST", "serial_number": "ST-00000022"}]
			}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)
			c := NewClient("test-token", WithBaseURL(srv.URL))

			devices, err := c.ListDevices(t.Context())
			if err != nil {
				t.Fatalf("ListDevices: %v", err)
			}
			// The device is still returned — dropping it is the failure this
			// guard exists to prevent.
			if len(devices) != 1 {
				t.Fatalf("got %d devices, want 1 (malformed device must still be returned)", len(devices))
			}
			if !strings.Contains(buf.String(), "Home") {
				t.Errorf("malformed ST device must log a WARN naming the station; got:\n%s", buf.String())
			}
		})
	}
}

// ListStations must keep its existing one-per-station collapse — ModeAPIExport
// depends on it. This pins the divergence so the shared decode refactor cannot
// silently change it.
func TestListStationsStillCollapsesToOneDevicePerStation(t *testing.T) {
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"stations":[{
		"station_id": 1, "name": "Home", "created_epoch": 1600000000,
		"devices": [
			{"device_id": 22, "device_type": "ST", "serial_number": "ST-00000022"},
			{"device_id": 33, "device_type": "ST", "serial_number": "ST-00000033"}
		]
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	stations, err := c.ListStations(t.Context())
	if err != nil {
		t.Fatalf("ListStations: %v", err)
	}
	if len(stations) != 1 {
		t.Fatalf("got %d stations, want 1", len(stations))
	}
	if stations[0].SerialNumber != "ST-00000033" {
		t.Errorf("SerialNumber = %q, want ST-00000033 (last ST wins — unchanged behavior)", stations[0].SerialNumber)
	}
}

// ListStations' HTTP-level failure (non-200 response) must be classifiable
// via errors.As, not just matched by message text — the retry layer decides
// whether to retry by extracting *StatusError, and a message-only assertion
// would keep passing even if fetchStations reverted to a plain fmt.Errorf.
func TestListStations_HTTPFailure_ClassifiesAsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	_, err := c.ListStations(t.Context())
	if err == nil {
		t.Fatal("ListStations() error = nil, want a *StatusError for HTTP 429")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("ListStations() error = %v (%T), want errors.As to find a *StatusError", err, err)
	}
	if se.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("StatusError.HTTPStatus = %d, want %d", se.HTTPStatus, http.StatusTooManyRequests)
	}
}

// ListStations' API-status-level failure (HTTP 200 with a non-zero
// status.status_code envelope) must also be classifiable via errors.As.
func TestListStations_APIStatusFailure_ClassifiesAsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stations":[],"status":{"status_code":2,"status_message":"BAD TOKEN"}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test-token", WithBaseURL(srv.URL))

	_, err := c.ListStations(t.Context())
	if err == nil {
		t.Fatal("ListStations() error = nil, want a *StatusError for non-zero status_code")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("ListStations() error = %v (%T), want errors.As to find a *StatusError", err, err)
	}
	if se.StatusCode != 2 {
		t.Errorf("StatusError.StatusCode = %d, want 2", se.StatusCode)
	}
	if se.Message != "BAD TOKEN" {
		t.Errorf("StatusError.Message = %q, want %q", se.Message, "BAD TOKEN")
	}
}
