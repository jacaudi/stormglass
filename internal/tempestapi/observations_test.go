package tempestapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient points a Client at an httptest.Server via the existing
// WithBaseURL seam (client.go:35-39).
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient("test-token", WithBaseURL(srv.URL))
}

func TestObservationsMapsNullToNilNotZero(t *testing.T) {
	// obs[6] (pressure) and obs[16] (battery) are null. They must decode to
	// nil pointers, NOT 0.0 — a 0.0 mb pressure reads as real to
	// SummarizeObservations' MIN(pressure) and to every chart.
	body := `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":[
		[1700000000,0.1,0.2,0.3,180,3,null,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,null,1]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	if obs[0].Pressure != nil {
		t.Errorf("Pressure = %v, want nil (JSON null must not become 0.0)", *obs[0].Pressure)
	}
	if obs[0].Battery != nil {
		t.Errorf("Battery = %v, want nil", *obs[0].Battery)
	}
	if obs[0].TempAir == nil || *obs[0].TempAir != 20.5 {
		t.Errorf("TempAir = %v, want 20.5", obs[0].TempAir)
	}
	if obs[0].SerialNumber != "ST-1" {
		t.Errorf("SerialNumber = %q, want %q", obs[0].SerialNumber, "ST-1")
	}
	if !obs[0].Timestamp.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("Timestamp = %v, want %v", obs[0].Timestamp, time.Unix(1700000000, 0).UTC())
	}
}

func TestObservationsEmptyWindowIsNotAnError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"obs empty", `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":[]}`},
		{"obs null", `{"status":{"status_code":0,"status_message":"SUCCESS"},"type":"obs_st","obs":null}`},
		{"status only, no type, no obs", `{"status":{"status_code":0,"status_message":"SUCCESS - Either no capabilities or no recent observations"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
			if err != nil {
				t.Fatalf("empty window must not be an error, got %v", err)
			}
			if len(obs) != 0 {
				t.Errorf("got %d observations, want 0", len(obs))
			}
		})
	}
}

func TestObservationsNonZeroStatusCodeIsStatusError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"status_code":404,"status_message":"NOT FOUND"}}`))
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 1}, time.Unix(0, 0), time.Unix(1, 0))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != 404 || se.Message != "NOT FOUND" {
		t.Errorf("got %+v, want StatusCode 404 / NOT FOUND", se)
	}
}

func TestObservationsHTTPErrorIsStatusError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 1}, time.Unix(0, 0), time.Unix(1, 0))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T: %v", err, err)
	}
	if se.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus = %d, want 503", se.HTTPStatus)
	}
}

func TestObservationsSkipsRowWithNullTimestamp(t *testing.T) {
	// A row with no timestamp cannot be keyed by (serial, timestamp) and
	// must be dropped rather than written at the epoch. This exercises the
	// same drop path as the logging tests below, so it needs the same
	// buffer handler to keep the WARN out of real stderr.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	body := `{"status":{"status_code":0},"type":"obs_st","obs":[
		[null,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,2.6,1],
		[1700000000,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0,0,10.0,2,2.6,1]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 (the null-timestamp row must be dropped)", len(obs))
	}
	if !obs[0].Timestamp.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("Timestamp = %v, want %v (the surviving row, not the dropped one)", obs[0].Timestamp, time.Unix(1700000000, 0).UTC())
	}
	if logged := buf.String(); !strings.Contains(logged, "dropped=1") {
		t.Errorf("the drop must be logged with a count; got:\n%s", logged)
	}
}

// The guards are GRADUATED, matching the UDP ingest path (>= 13 floor, tail
// filled conditionally). A 13-element tuple must be KEPT with its core
// measurements and NULL for the indices it does not carry — not dropped.
func TestObservationsKeepsShortTupleWithNullTail(t *testing.T) {
	// Exactly 13 elements: timestamp through rain_rate, nothing after.
	body := `{"status":{"status_code":0},"type":"obs_st","obs":[
		[1700000000,0.1,0.2,0.3,180,3,1013.0,20.5,55.0,1000,1.5,300,0.0]
	]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 — a 13-element tuple must not be dropped", len(obs))
	}
	if obs[0].TempAir == nil || *obs[0].TempAir != 20.5 {
		t.Errorf("TempAir = %v, want 20.5 (core measurements must survive)", obs[0].TempAir)
	}
	if obs[0].Battery != nil {
		t.Errorf("Battery = %v, want nil (index 16 absent from a 13-element tuple)", *obs[0].Battery)
	}
	if obs[0].ReportInterval != nil {
		t.Errorf("ReportInterval = %v, want nil (index 17 absent)", *obs[0].ReportInterval)
	}
}

// Below the floor there is nothing usable, so the tuple is dropped — and the
// drop MUST be logged.
//
// Asserting only len(obs)==0 would be insufficient: that outcome is identical
// to an empty window, which is exactly the confusion the WARN exists to
// prevent. A window whose tuples were all malformed would otherwise report
// zero rows and read as a permanent hole — the one machine-readable
// diagnostic the reporting design rests on.
func TestObservationsDropsTupleBelowFloorAndLogsIt(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	body := `{"status":{"status_code":0},"type":"obs_st","obs":[[1700000000,0.1,0.2]]}`
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	obs, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("got %d observations, want 0 — a 3-element tuple is below the 13 floor", len(obs))
	}

	logged := buf.String()
	if !strings.Contains(logged, "dropped=1") {
		t.Errorf("the drop must be logged with a count; got:\n%s", logged)
	}
	if !strings.Contains(logged, "ST-1") {
		t.Errorf("the drop log must name the serial; got:\n%s", logged)
	}
}

// The contrast case: a genuinely empty window must NOT log a drop warning,
// or the signal is worthless.
func TestObservationsEmptyWindowLogsNoDropWarning(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"status_code":0},"obs":[]}`))
	})
	if _, err := c.Observations(t.Context(), Station{DeviceID: 1, SerialNumber: "ST-1"}, time.Unix(0, 0), time.Unix(1, 0)); err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if strings.Contains(buf.String(), "dropped") {
		t.Errorf("an empty window must not log a drop warning; got:\n%s", buf.String())
	}
}

func TestObservationsRequestsCorrectWindow(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":{"status_code":0},"obs":[]}`))
	})
	_, err := c.Observations(t.Context(), Station{DeviceID: 77}, time.Unix(1700000000, 0), time.Unix(1700086400, 0))
	if err != nil {
		t.Fatalf("Observations: %v", err)
	}
	if want := "/observations/device/77"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if want := "time_start=1700000000&time_end=1700086400"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
	// This is the ONLY test that directly asserts Observations sends the
	// Bearer header — deleting the header line from Observations left the
	// entire suite green before this assertion existed, because every other
	// test on this path either doesn't inspect the header or exercises a
	// different method (fetchStations, via ListStations).
	if want := "Bearer test-token"; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}
