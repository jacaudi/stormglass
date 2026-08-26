package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewPostgresWriter_InvalidURL(t *testing.T) {
	ctx := context.Background()

	_, err := NewPostgresWriter(ctx, "not-a-valid-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestNewPostgresWriter_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// TODO: implement with testcontainers
	t.Skip("TODO: implement with real Postgres container")
}

func TestPostgresWriter_WriteReport_Observation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// TODO: implement with testcontainers
	t.Skip("TODO: implement with real Postgres")
}

func TestPostgresWriter_FlushObservations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// TODO: implement with testcontainers
	t.Skip("TODO: implement with real Postgres")
}

// Unit test for routing logic
func TestPostgresWriter_RouteObservation(t *testing.T) {
	ctx := t.Context()

	// Mock writer without real DB
	w := &PostgresWriter{
		obsBatch:   make(chan observationRow, 10),
		windBatch:  make(chan rapidWindRow, 10),
		hubBatch:   make(chan hubStatusRow, 10),
		eventBatch: make(chan eventRow, 10),
		ctx:        ctx,
		done:       make(chan struct{}),
	}

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-00001",
		Obs: [][]float64{
			{
				1234567890, // timestamp
				1.5,        // wind lull
				2.0,        // wind avg
				2.5,        // wind gust
				180,        // wind direction
				0,          // wind sample interval
				1013.25,    // pressure (MB)
				20.5,       // temp air (C)
				75.0,       // humidity (%)
				50000,      // illuminance
				3,          // UV
				500,        // irradiance
				0.5,        // rain rate
				0,          // precip type
				0,          // lightning distance
				0,          // lightning count
				3.5,        // battery
				1,          // report interval (minutes)
			},
		},
		FirmwareRevision: 143,
	}

	err := w.WriteReport(ctx, report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have queued one observation
	select {
	case row := <-w.obsBatch:
		if row.serialNumber != "ST-00001" {
			t.Errorf("wrong serial: got %q", row.serialNumber)
		}
		if row.windAvg != 2.0 {
			t.Errorf("wrong wind avg: got %f", row.windAvg)
		}
		if row.tempWetbulb == nil {
			t.Error("expected non-nil temp_wetbulb for a convergent observation")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observation")
	}
}

// fakeObsInserter is a test double for the obsInserter seam, letting
// TestPostgresWriter_DrainOnClose assert on drained rows without a live
// Postgres connection.
type fakeObsInserter struct {
	mu   sync.Mutex
	rows []observationRow
}

func (f *fakeObsInserter) insertObservations(_ context.Context, batch []observationRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, batch...)
	return nil
}

func (f *fakeObsInserter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// TestPostgresWriter_DrainOnClose proves Close(ctx) drains rows still
// sitting in the buffered channel (not just each worker's local in-flight
// batch) using the passed-in cleanup ctx rather than the writer's own
// (already-canceled) ctx (C-H1).
func TestPostgresWriter_DrainOnClose(t *testing.T) {
	// The writer's own ctx is canceled up front, simulating the real
	// shutdown sequence where SIGTERM cancels the shared context the
	// writer was constructed with before Close(cleanupCtx) is called with a
	// separate, still-live context.
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	cancelWorkerCtx()

	fake := &fakeObsInserter{}
	w := &PostgresWriter{
		obsBatch:      make(chan observationRow, 1000),
		windBatch:     make(chan rapidWindRow, 1000),
		hubBatch:      make(chan hubStatusRow, 1000),
		eventBatch:    make(chan eventRow, 1000),
		batchSize:     100,
		flushInterval: time.Second,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		obsInserter:   fake,
	}

	w.wg.Add(4)
	go w.batchObservations()
	go w.batchRapidWind()
	go w.batchHubStatus()
	go w.batchEvents()

	const wantRows = 250
	for range wantRows {
		w.obsBatch <- observationRow{serialNumber: "ST-DRAIN"}
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := fake.count(); got != wantRows {
		t.Fatalf("expected %d rows drained via Close, got %d", wantRows, got)
	}
}

// ctxHonoringObsInserter mirrors the real insertObservations' ctx-sensitivity,
// the way fakeWindInserter does for rapid wind: a real pgx insert against an
// already-canceled ctx fails immediately with context.Canceled, which
// isRetryable classifies as non-retryable, so flushWithRetry drops the batch.
// The plain fakeObsInserter above ignores ctx entirely, which is precisely why
// TestPostgresWriter_DrainOnClose passed while the live-database
// TestPostgresWriter_DrainOnClose_Integration lost rows (#111).
//
// attempts counts every row handed to the inserter whether or not the write
// succeeded, so a test can wait until the worker has actually consumed the
// channel instead of racing it.
type ctxHonoringObsInserter struct {
	mu       sync.Mutex
	rows     []observationRow
	attempts int
}

func (f *ctxHonoringObsInserter) insertObservations(ctx context.Context, batch []observationRow) error {
	f.mu.Lock()
	f.attempts += len(batch)
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("insert observations: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, batch...)
	return nil
}

func (f *ctxHonoringObsInserter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *ctxHonoringObsInserter) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// TestPostgresWriter_SteadyStateFlushSurvivesWriterCtxCancel proves the
// steady-state observation flush does not depend on the writer's own ctx,
// which SIGTERM cancels *before* Close is ever called in production.
//
// This is the unit-level counterpart to
// TestPostgresWriter_DrainOnClose_Integration (#111) and covers a window the
// existing drain tests do not: a row the worker has already taken off
// obsBatch is no longer in the channel, so Close's drainChannel cannot
// recover it. If its flush fails, the row is gone. Canceling the writer ctx
// before the rows are enqueued makes that window deterministic — every row is
// dequeued and flushed while w.ctx is dead — rather than depending on which
// case a two-ready select happens to pick.
//
// Reverting batchObservations to flush with w.ctx makes this test fail.
func TestPostgresWriter_SteadyStateFlushSurvivesWriterCtxCancel(t *testing.T) {
	// SIGTERM has already canceled the shared context the writer was
	// constructed with; Close has not been called yet.
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	cancelWorkerCtx()

	fake := &ctxHonoringObsInserter{}
	w := &PostgresWriter{
		obsBatch:      make(chan observationRow, 1000),
		windBatch:     make(chan rapidWindRow, 1000),
		hubBatch:      make(chan hubStatusRow, 1000),
		eventBatch:    make(chan eventRow, 1000),
		batchSize:     100,
		flushInterval: time.Second,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		obsInserter:   fake,
	}

	w.wg.Add(1)
	go w.batchObservations()

	const wantRows = 50
	for range wantRows {
		w.obsBatch <- observationRow{serialNumber: "ST-STEADY-CANCEL"}
	}

	// Wait until the worker has consumed and attempted every row, so the
	// assertion below measures the steady-state flush path rather than
	// Close's drain silently rescuing rows the worker never reached.
	deadline := time.Now().Add(5 * time.Second)
	for fake.attemptCount() < wantRows {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for worker to consume rows: attempted %d of %d",
				fake.attemptCount(), wantRows)
		}
		time.Sleep(time.Millisecond)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := fake.count(); got != wantRows {
		t.Fatalf("expected %d rows persisted by the steady-state flush, got %d", wantRows, got)
	}
}

// failFirstObsInserter fails its first insert with a RETRYABLE error (io.EOF,
// which isRetryable treats as a transient connection drop) and succeeds on
// every later attempt. It is the shape needed to reach flushWithRetry's
// backoff sleep at all: a non-retryable error is dropped before the sleep,
// which is why #111's reproduction never exercised this path.
type failFirstObsInserter struct {
	mu    sync.Mutex
	calls int
	rows  []observationRow
}

func (f *failFirstObsInserter) insertObservations(ctx context.Context, batch []observationRow) error {
	f.mu.Lock()
	f.calls++
	isFirst := f.calls == 1
	f.mu.Unlock()

	if isFirst {
		return fmt.Errorf("insert observations: %w", io.EOF)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("insert observations: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, batch...)
	return nil
}

func (f *failFirstObsInserter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *failFirstObsInserter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// TestPostgresWriter_RetryOutlivesWriterCtxCancel proves flushWithRetry does
// not abandon a retryable flush just because the writer's own ctx is dead
// (#153). Restarting Postgres and the exporter together produces exactly this
// pairing: a transient connection error arriving while w.ctx is already
// canceled. Gating the backoff sleep on w.ctx meant the first such failure
// dropped the batch outright, at the moment durability matters most.
//
// Restoring the `case <-w.ctx.Done()` guard in flushWithRetry makes this fail.
func TestPostgresWriter_RetryOutlivesWriterCtxCancel(t *testing.T) {
	// SIGTERM has already canceled the shared context; Close has not run yet.
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	cancelWorkerCtx()

	fake := &failFirstObsInserter{}
	w := &PostgresWriter{
		obsBatch:      make(chan observationRow, 10),
		windBatch:     make(chan rapidWindRow, 10),
		hubBatch:      make(chan hubStatusRow, 10),
		eventBatch:    make(chan eventRow, 10),
		batchSize:     100,
		flushInterval: time.Hour,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		obsInserter:   fake,
	}

	w.wg.Add(1)
	go w.batchObservations()

	w.obsBatch <- observationRow{serialNumber: "ST-RETRY"}

	// Wait for the first (failing) attempt, so Close cannot rescue the row via
	// its drain before the worker has even tried — which would pass the test
	// without exercising the retry path at all.
	deadline := time.Now().Add(5 * time.Second)
	for fake.callCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the first insert attempt")
		}
		time.Sleep(time.Millisecond)
	}

	// Close blocks in wg.Wait() until the worker finishes retrying, so no
	// extra synchronisation is needed here.
	closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := fake.count(); got != 1 {
		t.Fatalf("expected the retry to persist the row, got %d rows after %d attempts",
			got, fake.callCount())
	}
}

// TestHandleObservationReport_NaNWetbulbYieldsNull proves a non-convergent
// (physically impossible) observation stores SQL NULL for temp_wetbulb
// rather than IEEE NaN. stormglass_observations.temp_wetbulb is a nullable
// DOUBLE PRECISION column; unlike internal/tempestudp/report.go's Prometheus
// path (which skips emitting the wetbulb metric on NaN), the Postgres path
// had no such guard before this fix.
func TestHandleObservationReport_NaNWetbulbYieldsNull(t *testing.T) {
	w := &PostgresWriter{
		obsBatch: make(chan observationRow, 10),
		done:     make(chan struct{}),
	}

	// temp=25, humidity=-500 (physically impossible), pressure=900 is the
	// exact non-convergent input proven by
	// tempestudp.TestWetBulb_NonConvergentReturnsNaN to make
	// WetBulbTemperatureC return NaN.
	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-NAN",
		Obs: [][]float64{
			{1234567890, 1.5, 2.0, 2.5, 180, 0, 900, 25, -500, 50000, 3, 500, 0.5},
		},
	}

	if err := w.handleObservationReport(t.Context(), report); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case row := <-w.obsBatch:
		if row.tempWetbulb != nil {
			t.Errorf("expected NULL (nil) temp_wetbulb for a non-convergent observation, got %v", *row.tempWetbulb)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observation")
	}
}

// fakeWindInserter is a test double for the windInserter seam, letting
// TestPostgresWriter_ShutdownFlushesLocalBatchUnderCanceledWctx assert on the
// worker's local in-flight batch without a live Postgres connection.
// attempts counts every row handed to the inserter whether or not the write
// succeeded, so a test can wait until the worker has actually flushed instead
// of racing it.
type fakeWindInserter struct {
	mu       sync.Mutex
	rows     []rapidWindRow
	attempts int
}

// insertRapidWind mirrors the real insertRapidWind's ctx-sensitivity: a real
// pgx insert against an already-canceled ctx fails immediately with
// context.Canceled (a non-retryable error per isRetryable), which is exactly
// how the shutdown-drain bug drops rows. A fake that ignored ctx entirely
// would pass this test regardless of whether the shutdown flush used the
// live or the canceled context, defeating the point of the test.
func (f *fakeWindInserter) insertRapidWind(ctx context.Context, batch []rapidWindRow) error {
	f.mu.Lock()
	f.attempts += len(batch)
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, batch...)
	return nil
}

func (f *fakeWindInserter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeWindInserter) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// TestPostgresWriter_BatchFullFlushSurvivesWriterCtxCancel is the
// accumulator-shaped counterpart to
// TestPostgresWriter_SteadyStateFlushSurvivesWriterCtxCancel. batchRapidWind
// and batchHubStatus flush on batch-full and on the ticker, and
// flushWithRetry drops a batch that fails non-retryably — but the worker has
// already truncated its local slice, so those rows are gone before Close ever
// runs. A flush firing in the window after SIGTERM cancels w.ctx and before
// Close is called must therefore not be bound to w.ctx (#111).
//
// Reverting batchRapidWind's batch-full flush to w.ctx makes this test fail.
func TestPostgresWriter_BatchFullFlushSurvivesWriterCtxCancel(t *testing.T) {
	// SIGTERM has already canceled the shared context; Close has not run yet.
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	cancelWorkerCtx()

	const wantRows = 5

	fake := &fakeWindInserter{}
	w := &PostgresWriter{
		obsBatch:   make(chan observationRow, 10),
		windBatch:  make(chan rapidWindRow, 10),
		hubBatch:   make(chan hubStatusRow, 10),
		eventBatch: make(chan eventRow, 10),
		batchSize:  wantRows, // the wantRows-th row triggers the batch-full flush
		// Long enough that the ticker never fires: this test is about the
		// batch-full path specifically.
		flushInterval: time.Hour,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		windInserter:  fake,
	}

	w.wg.Add(1)
	go w.batchRapidWind()

	for range wantRows {
		w.windBatch <- rapidWindRow{serialNumber: "ST-BATCHFULL"}
	}

	// Wait for the batch-full flush to have been attempted, so the assertion
	// measures that flush rather than Close's shutdown flush of a local slice
	// the worker never got around to filling.
	deadline := time.Now().Add(5 * time.Second)
	for fake.attemptCount() < wantRows {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for batch-full flush: attempted %d of %d",
				fake.attemptCount(), wantRows)
		}
		time.Sleep(time.Millisecond)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := fake.count(); got != wantRows {
		t.Fatalf("expected %d rows persisted by the batch-full flush, got %d", wantRows, got)
	}
}

// TestPostgresWriter_ShutdownFlushesLocalBatchUnderCanceledWctx proves the
// residual C-H1 shutdown-drain bug: batchRapidWind accumulates rows below
// batchSize in a LOCAL slice (never flushed, never sitting in the channel for
// drainChannel to find), and on shutdown must flush that local slice using
// the live cleanup ctx passed to Close — not the writer's own w.ctx, which
// SIGTERM cancels before Close ever runs in production. Mirrors the real
// wiring: w.ctx is canceled up front, exactly like main.go's shared signal
// context is canceled before the deferred sink.Close(cleanupCtx) executes.
func TestPostgresWriter_ShutdownFlushesLocalBatchUnderCanceledWctx(t *testing.T) {
	// w.ctx starts LIVE so the rows below land deterministically in
	// batchRapidWind's local accumulator (no race against a ctx.Done() case
	// that's already ready). It is only canceled afterward, mirroring the
	// real SIGTERM sequence: the shared context is canceled first, then
	// Close(cleanupCtx) runs with its own, separately-live context.
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())

	fake := &fakeWindInserter{}
	w := &PostgresWriter{
		obsBatch:      make(chan observationRow, 10),
		windBatch:     make(chan rapidWindRow, 10),
		hubBatch:      make(chan hubStatusRow, 10),
		eventBatch:    make(chan eventRow, 10),
		batchSize:     100, // rows below this stay in the worker's local slice
		flushInterval: time.Hour,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		windInserter:  fake,
	}

	w.wg.Add(4)
	go w.batchObservations()
	go w.batchRapidWind()
	go w.batchHubStatus()
	go w.batchEvents()

	const wantRows = 5
	for i := range wantRows {
		w.windBatch <- rapidWindRow{windSpeed: float64(i)}
	}

	// Wait for batchRapidWind to pull the rows off the channel into its local
	// accumulator (below batchSize, so it does NOT flush them yet). w.ctx is
	// still live here, so the worker has no competing shutdown case to race
	// against — this drains deterministically.
	deadline := time.After(time.Second)
	for len(w.windBatch) != 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for batchRapidWind to drain windBatch into its local accumulator")
		case <-time.After(time.Millisecond):
		}
	}

	// Now simulate SIGTERM: cancel the shared context the writer was
	// constructed with, before Close runs with its own live cleanup ctx.
	cancelWorkerCtx()

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := fake.count(); got != wantRows {
		t.Fatalf("expected %d in-flight rows flushed via the live cleanup ctx, got %d (rows dropped because shutdown flush used the canceled w.ctx)", wantRows, got)
	}
}

// TestPostgresClose_Idempotent proves Close(ctx) can be called more than
// once without panicking (C-H3: the old implementation unconditionally
// closed the four batch channels on every call, so a second Close double-
// closed them).
func TestPostgresClose_Idempotent(t *testing.T) {
	w := &PostgresWriter{
		obsBatch:   make(chan observationRow, 10),
		windBatch:  make(chan rapidWindRow, 10),
		hubBatch:   make(chan hubStatusRow, 10),
		eventBatch: make(chan eventRow, 10),
		ctx:        t.Context(),
		done:       make(chan struct{}),
	}

	ctx := t.Context()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestPostgresWriteDuringClose_NoPanic proves WriteReport (which sends into
// the batch channels) can run concurrently with Close without a
// send-on-closed-channel panic (D-H1), and that Close still returns
// promptly rather than deadlocking on a drain of a never-closed channel.
func TestPostgresWriteDuringClose_NoPanic(t *testing.T) {
	workerCtx, cancelWorkerCtx := context.WithCancel(t.Context())
	defer cancelWorkerCtx()

	w := &PostgresWriter{
		obsBatch:      make(chan observationRow, 10),
		windBatch:     make(chan rapidWindRow, 10),
		hubBatch:      make(chan hubStatusRow, 10),
		eventBatch:    make(chan eventRow, 10),
		batchSize:     100,
		flushInterval: time.Second,
		maxRetries:    3,
		ctx:           workerCtx,
		done:          make(chan struct{}),
		obsInserter:   &fakeObsInserter{},
	}

	w.wg.Add(4)
	go w.batchObservations()
	go w.batchRapidWind()
	go w.batchHubStatus()
	go w.batchEvents()

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-RACE",
		Obs: [][]float64{
			{1234567890, 1, 1, 1, 1, 1, 1013.25, 20, 75, 50000, 3, 500, 0.5, 0, 0, 0, 3.5, 1},
		},
	}

	stop := make(chan struct{})
	var producers sync.WaitGroup
	for range 5 {
		producers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = w.WriteReport(t.Context(), report)
				}
			}
		})
	}

	closeCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	closeErr := make(chan error, 1)
	go func() {
		closeErr <- w.Close(closeCtx)
	}()

	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return in time (deadlock)")
	}

	close(stop)
	producers.Wait()
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"connection failure SQLSTATE 08006", &pgconn.PgError{Code: "08006"}, true},
		{"connection establishment failure SQLSTATE 08001", &pgconn.PgError{Code: "08001"}, true},
		{"server rejected connection SQLSTATE 08004", &pgconn.PgError{Code: "08004"}, true},
		{"transaction resolution unknown SQLSTATE 08007", &pgconn.PgError{Code: "08007"}, true},
		{"serialization failure SQLSTATE 40001", &pgconn.PgError{Code: "40001"}, true},
		{"deadlock detected SQLSTATE 40P01", &pgconn.PgError{Code: "40P01"}, true},
		{"too many connections SQLSTATE 53300", &pgconn.PgError{Code: "53300"}, true},
		{"admin shutdown SQLSTATE 57P01", &pgconn.PgError{Code: "57P01"}, true},
		{"crash shutdown SQLSTATE 57P02", &pgconn.PgError{Code: "57P02"}, true},
		{"cannot connect now (server starting up) SQLSTATE 57P03", &pgconn.PgError{Code: "57P03"}, true},
		{"query canceled SQLSTATE 57014", &pgconn.PgError{Code: "57014"}, false},
		{"configuration limit exceeded SQLSTATE 53400", &pgconn.PgError{Code: "53400"}, false},
		{"constraint violation SQLSTATE 23505", &pgconn.PgError{Code: "23505"}, false},
		{"unrecognized SQLSTATE XX000", &pgconn.PgError{Code: "XX000"}, false},
		{"plain unknown error", errors.New("some totally unknown failure"), false},
		{
			"transient network error (connection refused)",
			&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			true,
		},
		{
			// Uniquely exercises the *net.OpError catch-all: DNSError.Timeout()
			// is false (IsNotFound, not IsTimeout) so the net.Error/Timeout()
			// branch above does not match; only errors.As(err, &opErr) does.
			"dns not found",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", IsNotFound: true}},
			true,
		},
		{
			// Uniquely exercises the net.Error/Timeout() branch: *net.DNSError
			// implements net.Error directly but is NOT a *net.OpError, so the
			// catch-all below cannot match it.
			"network timeout",
			&net.DNSError{Err: "i/o timeout", IsTimeout: true},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
