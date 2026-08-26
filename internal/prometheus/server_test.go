package prometheus

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempest"
	"tempestwx-utilities/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsServer_StartAndClose(t *testing.T) {
	server := NewMetricsServer("0") // Use port 0 for random available port

	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	if err := server.Close(t.Context()); err != nil {
		t.Fatalf("failed to close server: %v", err)
	}
}

func TestMetricsServer_StartReturnsBindError(t *testing.T) {
	// Discover a free port by binding to :0 with a plain listener, then
	// release it immediately. Closing a listener that never accepted a
	// connection releases the port with no lingering state, so it's safe
	// to reuse straight away.
	probe, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to discover a free port: %v", err)
	}
	port := strconv.Itoa(probe.Addr().(*net.TCPAddr).Port)
	if err := probe.Close(); err != nil {
		t.Fatalf("failed to release probe listener: %v", err)
	}

	server1 := NewMetricsServer(port)
	if err := server1.Start(); err != nil {
		t.Fatalf("first server failed to bind %s: %v", port, err)
	}
	defer func() { _ = server1.Close(t.Context()) }()

	// server1 is still holding the port, so a second server bound to the
	// same port must fail synchronously.
	server2 := NewMetricsServer(port)
	err = server2.Start()
	defer func() { _ = server2.Close(t.Context()) }()

	if err == nil {
		t.Fatal("expected second Start() on the same port to return a bind error, got nil")
	}
}

func TestMetricsServer_WriteReport(t *testing.T) {
	server := NewMetricsServer("0")
	ctx := context.Background()

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-00001",
		Obs: [][]float64{
			{1234567890, 1.5, 2.0, 2.5, 180, 0, 1013.25, 20.5, 75, 50000, 3, 500, 0.5},
		},
	}

	err := server.WriteReport(ctx, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify metrics were stored
	if len(server.collector.metrics) == 0 {
		t.Error("expected metrics to be stored in collector")
	}
}

func TestMetricsServer_WriteMetrics(t *testing.T) {
	server := NewMetricsServer("0")
	ctx := context.Background()

	// Create a test metric
	metric := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		25.5,
		"ST-00001", "air",
	)

	if err := server.WriteMetrics(ctx, []prometheus.Metric{metric}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify metric was stored
	if len(server.collector.metrics) != 1 {
		t.Errorf("expected 1 metric, got %d", len(server.collector.metrics))
	}
}

func TestMetricsServer_MetricsEndpoint(t *testing.T) {
	server := NewMetricsServer("19090")

	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Close(t.Context()) }()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Add a metric
	metric := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		22.5,
		"ST-00001", "air",
	)
	_ = server.WriteMetrics(context.Background(), []prometheus.Metric{metric})

	// Fetch metrics endpoint
	resp, err := http.Get("http://127.0.0.1:19090/metrics")
	if err != nil {
		t.Fatalf("failed to fetch metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "stormglass_temperature_c") {
		t.Error("expected metrics response to contain stormglass_temperature_c")
	}
}

func TestMetricsServer_HealthEndpoint(t *testing.T) {
	server := NewMetricsServer("19091")

	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Close(t.Context()) }()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:19091/health")
	if err != nil {
		t.Fatalf("failed to fetch health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "OK" {
		t.Errorf("expected 'OK', got %q", string(body))
	}
}

func TestLatestMetricsCollector_Update(t *testing.T) {
	collector := &latestMetricsCollector{
		metrics: make(map[string]prometheus.Metric),
	}

	// Add initial metric
	metric1 := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		20.0,
		"ST-00001", "air",
	)
	collector.Update([]prometheus.Metric{metric1})

	if len(collector.metrics) != 1 {
		t.Errorf("expected 1 metric, got %d", len(collector.metrics))
	}

	// Update with new value for same metric (should replace)
	metric2 := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		25.0,
		"ST-00001", "air",
	)
	collector.Update([]prometheus.Metric{metric2})

	if len(collector.metrics) != 1 {
		t.Errorf("expected 1 metric after update, got %d", len(collector.metrics))
	}

	// Add different metric (different labels)
	metric3 := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		18.0,
		"ST-00001", "wetbulb",
	)
	collector.Update([]prometheus.Metric{metric3})

	if len(collector.metrics) != 2 {
		t.Errorf("expected 2 metrics, got %d", len(collector.metrics))
	}
}

func TestLatestMetricsCollector_Collect(t *testing.T) {
	collector := &latestMetricsCollector{
		metrics: make(map[string]prometheus.Metric),
	}

	// Add metrics
	metric1 := prometheus.MustNewConstMetric(
		tempest.Temperature,
		prometheus.GaugeValue,
		20.0,
		"ST-00001", "air",
	)
	metric2 := prometheus.MustNewConstMetric(
		tempest.Humidity,
		prometheus.GaugeValue,
		65.0,
		"ST-00001",
	)
	collector.Update([]prometheus.Metric{metric1, metric2})

	// Collect metrics
	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}

	if count != 2 {
		t.Errorf("expected 2 metrics from Collect, got %d", count)
	}
}

func TestMetricsServer_Flush(t *testing.T) {
	server := NewMetricsServer("0")
	ctx := context.Background()

	// Flush should be a no-op and return nil
	err := server.Flush(ctx)
	if err != nil {
		t.Errorf("expected nil error from Flush, got %v", err)
	}
}
