package sink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tempestwx-utilities/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
)

// Mock writer for testing
type mockWriter struct {
	reportCalls   int
	metricCalls   int
	reportErr     error
	metricsErr    error
	panicMsg      string // non-empty triggers a panic from WriteReport
	closePanicMsg string // non-empty triggers a panic from Close
	flushCalled   bool
	closeCalled   bool
	closeCtx      context.Context
	mu            sync.Mutex
}

func (m *mockWriter) WriteReport(ctx context.Context, report tempestudp.Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reportCalls++
	if m.panicMsg != "" {
		panic(m.panicMsg)
	}
	return m.reportErr
}

func (m *mockWriter) WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metricCalls++
	return m.metricsErr
}

func (m *mockWriter) Flush(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushCalled = true
	return nil
}

func (m *mockWriter) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalled = true
	m.closeCtx = ctx
	if m.closePanicMsg != "" {
		panic(m.closePanicMsg)
	}
	return nil
}

// blockingWriter simulates a writer whose enqueue path is stalled behind a
// full channel (e.g. Postgres's batch channel while the database is down).
// WriteReport blocks until the passed ctx is done and then returns ctx.Err(),
// mirroring the writers' real select-on-ctx.Done() enqueue paths. Embedding
// mockWriter supplies WriteMetrics/Flush/Close for free.
type blockingWriter struct {
	mockWriter
}

func (b *blockingWriter) WriteReport(ctx context.Context, report tempestudp.Report) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestMetricsSink_AddWriter(t *testing.T) {
	sink := NewMetricsSink()

	if sink.WriterCount() != 0 {
		t.Errorf("expected 0 writers, got %d", sink.WriterCount())
	}

	writer := &mockWriter{}
	sink.AddWriter(writer)

	if sink.WriterCount() != 1 {
		t.Errorf("expected 1 writer, got %d", sink.WriterCount())
	}
}

func TestMetricsSink_SendReport(t *testing.T) {
	ctx := context.Background()
	sink := NewMetricsSink()

	writer1 := &mockWriter{}
	writer2 := &mockWriter{}
	sink.AddWriter(writer1)
	sink.AddWriter(writer2)

	// Create a simple report
	report := &tempestudp.TempestObservationReport{
		SerialNumber: "TEST-001",
		Obs:          [][]float64{{1234567890, 1.5, 2.0, 2.5}},
	}

	err := sink.SendReport(ctx, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both writers should be called
	if writer1.reportCalls != 1 {
		t.Errorf("writer1: expected 1 call, got %d", writer1.reportCalls)
	}
	if writer2.reportCalls != 1 {
		t.Errorf("writer2: expected 1 call, got %d", writer2.reportCalls)
	}
}

func TestMetricsSink_SendMetrics(t *testing.T) {
	ctx := context.Background()
	sink := NewMetricsSink()

	writer := &mockWriter{}
	sink.AddWriter(writer)

	metrics := []prometheus.Metric{} // Empty for now
	err := sink.SendMetrics(ctx, metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if writer.metricCalls != 1 {
		t.Errorf("expected 1 call, got %d", writer.metricCalls)
	}
}

func TestMetricsSink_WriterError(t *testing.T) {
	ctx := context.Background()
	sink := NewMetricsSink()

	// Writer that returns error
	failWriter := &mockWriter{reportErr: errors.New("write failed")}
	goodWriter := &mockWriter{}

	sink.AddWriter(failWriter)
	sink.AddWriter(goodWriter)

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "TEST-001",
	}

	// A failing writer's error is now aggregated and returned (A-LOW: no more
	// dead nil returns that silently swallow writer errors).
	err := sink.SendReport(ctx, report)
	if err == nil {
		t.Error("expected aggregated error when a writer fails, got nil")
	}

	// Both writers should have been called
	if failWriter.reportCalls != 1 {
		t.Errorf("failWriter should have been called")
	}
	if goodWriter.reportCalls != 1 {
		t.Errorf("goodWriter should have been called")
	}
}

func TestMetricsSink_Close(t *testing.T) {
	ctx := context.Background()
	sink := NewMetricsSink()

	writer1 := &mockWriter{}
	writer2 := &mockWriter{}
	sink.AddWriter(writer1)
	sink.AddWriter(writer2)

	err := sink.Close(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both writers should have flush and close called
	if !writer1.flushCalled {
		t.Error("writer1.Flush should have been called")
	}
	if !writer1.closeCalled {
		t.Error("writer1.Close should have been called")
	}
	if !writer2.flushCalled {
		t.Error("writer2.Flush should have been called")
	}
	if !writer2.closeCalled {
		t.Error("writer2.Close should have been called")
	}
}

// TestSink_RecoversPanickingWriter verifies a panicking writer cannot crash
// the sink or block delivery to sibling writers (A-MEDIUM: panic recovery).
func TestSink_RecoversPanickingWriter(t *testing.T) {
	ctx := context.Background()
	s := NewMetricsSink()

	panicker := &mockWriter{panicMsg: "boom"}
	healthy := &mockWriter{}
	s.AddWriter(panicker)
	s.AddWriter(healthy)

	report := &tempestudp.TempestObservationReport{SerialNumber: "TEST-001"}

	err := s.SendReport(ctx, report)
	if err == nil {
		t.Fatal("expected a non-nil error naming the recovered panic")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error to mention the panic value, got: %v", err)
	}

	// The panicking writer was still invoked, and the sibling writer must
	// still have been called despite the panic.
	if panicker.reportCalls != 1 {
		t.Errorf("panicker: expected 1 call, got %d", panicker.reportCalls)
	}
	if healthy.reportCalls != 1 {
		t.Errorf("healthy writer: expected 1 call, got %d", healthy.reportCalls)
	}
}

// TestSink_SendReportAggregatesErrors verifies SendReport joins per-writer
// errors instead of silently discarding them (A-LOW: no dead nil returns).
func TestSink_SendReportAggregatesErrors(t *testing.T) {
	ctx := context.Background()
	report := &tempestudp.TempestObservationReport{SerialNumber: "TEST-001"}

	t.Run("two failing writers aggregate", func(t *testing.T) {
		s := NewMetricsSink()
		s.AddWriter(&mockWriter{reportErr: errors.New("writer1 failed")})
		s.AddWriter(&mockWriter{reportErr: errors.New("writer2 failed")})

		err := s.SendReport(ctx, report)
		if err == nil {
			t.Fatal("expected non-nil aggregated error")
		}
		if !strings.Contains(err.Error(), "writer1 failed") || !strings.Contains(err.Error(), "writer2 failed") {
			t.Errorf("expected both writer errors present, got: %v", err)
		}
	})

	t.Run("one failing one ok names the failure", func(t *testing.T) {
		s := NewMetricsSink()
		s.AddWriter(&mockWriter{reportErr: errors.New("writer1 failed")})
		s.AddWriter(&mockWriter{})

		err := s.SendReport(ctx, report)
		if err == nil {
			t.Fatal("expected non-nil error naming the failing writer")
		}
		if !strings.Contains(err.Error(), "writer1 failed") {
			t.Errorf("expected failure to be named, got: %v", err)
		}
	})

	t.Run("all ok returns nil", func(t *testing.T) {
		s := NewMetricsSink()
		s.AddWriter(&mockWriter{})
		s.AddWriter(&mockWriter{})

		if err := s.SendReport(ctx, report); err != nil {
			t.Errorf("expected nil error when all writers succeed, got: %v", err)
		}
	})
}

// TestSink_ClosePanicRecovered verifies a writer whose Close panics cannot
// crash the sink or block the sibling writer's shutdown, mirroring
// SendReport's/SendMetrics's existing panic-recovery pattern.
func TestSink_ClosePanicRecovered(t *testing.T) {
	ctx := context.Background()
	s := NewMetricsSink()

	panicker := &mockWriter{closePanicMsg: "boom on close"}
	healthy := &mockWriter{}
	s.AddWriter(panicker)
	s.AddWriter(healthy)

	err := s.Close(ctx)
	if err == nil {
		t.Fatal("expected a non-nil error naming the recovered panic")
	}
	if !strings.Contains(err.Error(), "boom on close") {
		t.Errorf("expected error to mention the panic value, got: %v", err)
	}

	if !panicker.closeCalled {
		t.Error("panicker.Close should have been called")
	}
	if !healthy.closeCalled {
		t.Error("healthy.Close should still have been called despite the sibling panic")
	}
}

// TestSink_SendReportBoundsBlockingWriter verifies that a single stalled
// writer cannot block SendReport forever (#47). The UDP read loop calls
// SendReport synchronously with the top-level application ctx, which is
// never canceled during normal operation — so an unbounded per-writer send
// (e.g. a full Postgres batch channel) would stall ingest for every sink.
//
// The test passes a context.Background()-derived ctx that this test never
// cancels, so the only thing that can unblock the blocking writer is the
// sink bounding it internally. SendReport itself runs in a goroutine and is
// raced against a guard timeout so a regression (infinite block) fails via
// t.Fatal rather than hanging the suite.
func TestSink_SendReportBoundsBlockingWriter(t *testing.T) {
	// Shrink the package's per-writer timeout for this test only, so the
	// assertions below don't have to wait out the real production 5s bound.
	orig := writeTimeout
	writeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeTimeout = orig })

	s := NewMetricsSink()

	blocking := &blockingWriter{}
	fast := &mockWriter{}
	s.AddWriter(blocking)
	s.AddWriter(fast)

	report := &tempestudp.TempestObservationReport{SerialNumber: "TEST-001"}

	done := make(chan error, 1)
	go func() {
		done <- s.SendReport(context.Background(), report)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a non-nil error naming the timed-out blocking writer")
		}
		if !strings.Contains(err.Error(), "blockingWriter") {
			t.Errorf("expected error to name the blocking writer, got: %v", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected error to wrap context.DeadlineExceeded, got: %v", err)
		}
		if fast.reportCalls != 1 {
			t.Errorf("fast writer: expected 1 call, got %d", fast.reportCalls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendReport did not return within 2s — regression: a stalled writer blocked the shared fan-out (#47)")
	}
}

// TestSink_ClosePassesContext verifies Close(ctx) forwards the caller's
// cleanup context to each writer rather than a stored/cancelled one.
func TestSink_ClosePassesContext(t *testing.T) {
	s := NewMetricsSink()
	writer := &mockWriter{}
	s.AddWriter(writer)

	// context.WithCancel yields a context distinct from context.Background(),
	// so identity comparison below actually proves the parameter was forwarded.
	cleanupCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Close(cleanupCtx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if writer.closeCtx != cleanupCtx {
		t.Error("expected Close to receive the caller-supplied cleanup context")
	}
}
