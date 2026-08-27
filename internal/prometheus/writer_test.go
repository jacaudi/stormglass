package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// newTestPushGateway starts a local HTTP server that accepts any push
// request and returns its URL. Close (per Task 0.9b) makes exactly one
// final-flush Add() call per writer, which performs a real HTTP request —
// pointing that at an unreachable dead port (e.g. localhost:9091) makes
// timing depend on this environment's TCP-refusal latency for closed ports,
// which is not always instant and can make tests asserting "Close returns
// promptly" flaky for reasons unrelated to the code under test.
func newTestPushGateway(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestPrometheusWriter_WriteReport(t *testing.T) {
	ctx := context.Background()

	writer := NewPrometheusWriter(newTestPushGateway(t), "test-job")

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-00001",
		Obs: [][]float64{
			{1234567890, 1.5, 2.0, 2.5, 180, 0, 1013.25, 20.5, 75, 50000, 3, 500, 0.5},
		},
	}

	err := writer.WriteReport(ctx, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that metrics were queued
	// This is a basic test - full integration test would verify push gateway
	time.Sleep(100 * time.Millisecond)
}

func TestPrometheusWriter_Close(t *testing.T) {
	writer := NewPrometheusWriter(newTestPushGateway(t), "test-job")

	err := writer.Close(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPrometheusClose_Idempotent verifies calling Close twice does not panic
// (double-close of a channel) and returns nil both times.
func TestPrometheusClose_Idempotent(t *testing.T) {
	writer := NewPrometheusWriter(newTestPushGateway(t), "test-job")

	if err := writer.Close(t.Context()); err != nil {
		t.Fatalf("first Close: unexpected error: %v", err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatalf("second Close: unexpected error: %v", err)
	}
}

// TestPrometheusWriter_ClosePushTimeoutBounded verifies the pusher's HTTP
// client carries a bounded Timeout, so a push gateway that accepts the
// connection but never responds cannot stall the final-flush Add() (and
// therefore Close) indefinitely. Add() uses context.Background() internally
// (see github.com/prometheus/client_golang/prometheus/push), so without a
// client-level Timeout this hangs until the test's own outer deadline — the
// 0.9b review finding.
func TestPrometheusWriter_ClosePushTimeoutBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client gives up waiting
	}))
	t.Cleanup(server.Close)

	writer := NewPrometheusWriter(server.URL, "test-job")

	closeDone := make(chan error, 1)
	go func() { closeDone <- writer.Close(t.Context()) }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	case <-time.After(pushTimeout + 5*time.Second):
		t.Fatal("Close did not return within the bounded push timeout")
	}
}

// TestPrometheusWriteDuringClose_NoPanic drives concurrent WriteMetrics
// producers against a Close in progress. Before the done-gate, this panics
// with a send-on-closed-channel from either the outbox send or the more
// signal in WriteMetrics, and Close itself double-closes those channels.
// Run with -race.
//
// Each producer sends a bounded number of metric batches (not an unbounded
// tight loop): flooding outbox with hundreds of same-name/same-label metric
// sends from one reused report makes the underlying Prometheus Gatherer's
// duplicate-metric-descriptor error formatting (which runs on every Add,
// before any network call) arbitrarily expensive and turns "Close returns
// promptly" into a workload-size assertion rather than a done-gate one.
func TestPrometheusWriteDuringClose_NoPanic(t *testing.T) {
	writer := NewPrometheusWriter(newTestPushGateway(t), "test-job")

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-00001",
		Obs: [][]float64{
			{1234567890, 1.5, 2.0, 2.5, 180, 0, 1013.25, 20.5, 75, 50000, 3, 500, 0.5},
		},
	}
	metrics := report.Metrics()

	const writesPerProducer = 20

	var producers sync.WaitGroup
	for range 4 {
		producers.Go(func() {
			for range writesPerProducer {
				_ = writer.WriteMetrics(t.Context(), metrics)
			}
		})
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- writer.Close(t.Context())
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return promptly")
	}

	producers.Wait()
}

// gatherPushCollector runs the push path's collector through a real registry
// the same way push.Pusher does (push/push.go calls Gatherers.Gather() BEFORE
// any HTTP request), so these tests see exactly what the gateway would --
// including a Gather() error, which is how #187's naive fix fails.
func gatherPushCollector(t *testing.T, outbox chan prometheus.Metric) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(newPushCollector(outbox)); err != nil {
		t.Fatalf("register push collector: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() failed -- this is the failure Pusher.push hits before any HTTP request: %v", err)
	}
	return mfs
}

func testMetric(value float64, ts time.Time, serial string) prometheus.Metric {
	desc := prometheus.NewDesc("stormglass_test_metric", "test", []string{"serial"}, nil)
	m := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, serial)
	return prometheus.NewMetricWithTimestamp(ts, m)
}

// TestPushPath_StripsTimestamps is #187 (a). A real Pushgateway rejects any
// pushed sample carrying a timestamp with 400 "pushed metrics must not have
// timestamps", so every push from this path failed.
func TestPushPath_StripsTimestamps(t *testing.T) {
	outbox := make(chan prometheus.Metric, 4)
	outbox <- testMetric(1.0, time.Unix(1700000000, 0), "ST-0001")

	mfs := gatherPushCollector(t, outbox)
	if len(mfs) != 1 {
		t.Fatalf("got %d metric families, want 1", len(mfs))
	}
	for _, m := range mfs[0].Metric {
		if m.TimestampMs != nil {
			t.Errorf("pushed sample carries TimestampMs=%d; a Pushgateway 400s on that", m.GetTimestampMs())
		}
	}
}

// TestPushPath_DrainTwoSamplesOfOneSeries is #187 (b), and it is the reason
// the naive fix is not enough. client_golang's checkMetricConsistency hashes
// the TIMESTAMP into its uniqueness key (registry.go:983-986), so today two
// samples of one series hash apart and no error fires. Strip the timestamps
// and they collide: Gather() aborts with "was collected before with the same
// name and label values" and the push dies locally, before any HTTP request.
// outboxCollector drains the whole channel per push, so two obs_st broadcasts
// between pushes make this the common case, not an edge.
//
// Values differ (1.0 then 2.0) deliberately: once timestamps are stripped,
// distinct values are the ONLY way to tell first-wins from last-wins.
func TestPushPath_DrainTwoSamplesOfOneSeries(t *testing.T) {
	outbox := make(chan prometheus.Metric, 4)
	outbox <- testMetric(1.0, time.Unix(1700000000, 0), "ST-0001")
	outbox <- testMetric(2.0, time.Unix(1700000060, 0), "ST-0001")

	mfs := gatherPushCollector(t, outbox)
	if len(mfs) != 1 {
		t.Fatalf("got %d metric families, want 1", len(mfs))
	}
	if got := len(mfs[0].Metric); got != 1 {
		t.Fatalf("got %d samples for one series, want exactly 1 (deduped)", got)
	}
	if got := mfs[0].Metric[0].GetGauge().GetValue(); got != 2.0 {
		t.Errorf("surviving sample value = %v, want 2 (last-queued wins; the outbox is FIFO so last = newest)", got)
	}
}

// TestPushPath_DedupeKeepsDistinctSeriesApart guards the identity choice.
// m.Desc().String() carries label NAMES but not VALUES, so deduping on it
// would collapse two stations' serial-labelled series into one -- real
// cross-device data loss. metricKey includes the values.
func TestPushPath_DedupeKeepsDistinctSeriesApart(t *testing.T) {
	outbox := make(chan prometheus.Metric, 4)
	outbox <- testMetric(1.0, time.Unix(1700000000, 0), "ST-0001")
	outbox <- testMetric(2.0, time.Unix(1700000000, 0), "ST-0002")

	mfs := gatherPushCollector(t, outbox)
	if len(mfs) != 1 {
		t.Fatalf("got %d metric families, want 1", len(mfs))
	}
	if got := len(mfs[0].Metric); got != 2 {
		t.Errorf("got %d samples, want 2 -- two stations must not collapse into one series", got)
	}
}
