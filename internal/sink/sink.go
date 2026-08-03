// Package sink fans a single stream of Tempest reports/metrics out to
// however many MetricsWriter backends are registered (Prometheus, SQLite,
// PostgreSQL, OTel), concurrently, with per-writer panic recovery and
// error aggregation so one degraded backend never blocks or crashes
// delivery to the others.
package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"tempestwx-utilities/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
)

// defaultWriteTimeout bounds a single writer's acceptance of one report in
// SendReport. SendReport is called synchronously from the single UDP read
// goroutine (main.go:456) with the top-level application ctx, which is not
// canceled during normal operation — so an unbounded per-writer send (e.g. a
// full Postgres batch channel while the database is degraded) would stall
// ingest for every sink, not just the degraded one. Writers already select on
// ctx.Done() in their enqueue paths, so bounding the ctx here is sufficient
// to release them; no writer changes are needed (#47).
//
// SendMetrics is deliberately left unbounded: its only caller is the API
// export/backfill path (main.go:562), where blocking is correct backpressure
// — wait for the database rather than silently truncate a batch that can
// hold up to 200,000 metrics. A timeout there would expire mid-batch and
// drop the remaining rows while the export advances, a silent partial data
// loss with no analogous shared-read-loop hazard to justify the cost.
//
// 5s matches eventBlockTimeout in internal/sqlite/writer.go:29, which bounds
// the analogous case (a discrete event blocking on a full channel). Kept
// consistent rather than picking an independent value for the same class of
// wait.
const defaultWriteTimeout = 5 * time.Second

// MetricsWriter is the interface that all metric backends must implement.
type MetricsWriter interface {
	// WriteReport writes a parsed Tempest report (UDP mode - typed structs)
	WriteReport(ctx context.Context, report tempestudp.Report) error

	// WriteMetrics writes Prometheus metrics (API export mode)
	WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error

	// Flush ensures any buffered data is written
	Flush(ctx context.Context) error

	// Close performs cleanup using the caller-supplied context.
	Close(ctx context.Context) error
}

// MetricsSink coordinates sending metrics to multiple backends.
type MetricsSink struct {
	writers []MetricsWriter
	mu      sync.RWMutex

	// writeTimeout bounds SendReport's per-writer send (see
	// defaultWriteTimeout). It is a field, not a package-level var, so tests
	// can shrink it on a single sink instance without mutable package state.
	writeTimeout time.Duration
}

// NewMetricsSink creates a new metrics sink.
func NewMetricsSink() *MetricsSink {
	return &MetricsSink{
		writers:      make([]MetricsWriter, 0),
		writeTimeout: defaultWriteTimeout,
	}
}

// AddWriter registers a new metrics writer.
func (s *MetricsSink) AddWriter(writer MetricsWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writers = append(s.writers, writer)
}

// WriterCount returns the number of registered writers.
func (s *MetricsSink) WriterCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.writers)
}

// SendReport sends a typed report to all writers. A panic in one writer is
// recovered and reported as an error rather than crashing the process or
// blocking delivery to the other writers; per-writer errors are aggregated
// via errors.Join rather than silently discarded.
func (s *MetricsSink) SendReport(ctx context.Context, report tempestudp.Report) error {
	s.mu.RLock()
	writers := slices.Clone(s.writers)
	s.mu.RUnlock()

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, w := range writers {
		wg.Add(1)
		go func(writer MetricsWriter) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("writer panic recovered", "panic", r, "writer", fmt.Sprintf("%T", writer))
					mu.Lock()
					errs = append(errs, fmt.Errorf("writer %T panicked: %v", writer, r))
					mu.Unlock()
				}
			}()
			wctx, cancel := context.WithTimeout(ctx, s.writeTimeout)
			defer cancel()
			if err := writer.WriteReport(wctx, report); err != nil {
				mu.Lock() // mutex guards the append — required, else -race flags the concurrent slice write
				errs = append(errs, fmt.Errorf("writer %T: %w", writer, err))
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	return errors.Join(errs...) // nil when errs is empty
}

// SendMetrics sends Prometheus metrics to all writers. Same panic-recovery
// and error-aggregation semantics as SendReport, but deliberately does not
// bound each writer's send with a timeout — see defaultWriteTimeout for why.
// The caller's ctx is passed straight through so cancellation still works.
func (s *MetricsSink) SendMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	s.mu.RLock()
	writers := slices.Clone(s.writers)
	s.mu.RUnlock()

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, w := range writers {
		wg.Add(1)
		go func(writer MetricsWriter) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("writer panic recovered", "panic", r, "writer", fmt.Sprintf("%T", writer))
					mu.Lock()
					errs = append(errs, fmt.Errorf("writer %T panicked: %v", writer, r))
					mu.Unlock()
				}
			}()
			if err := writer.WriteMetrics(ctx, metrics); err != nil {
				mu.Lock() // mutex guards the append — required, else -race flags the concurrent slice write
				errs = append(errs, fmt.Errorf("writer %T: %w", writer, err))
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	return errors.Join(errs...) // nil when errs is empty
}

// Close flushes and closes all writers using the caller-supplied context
// (never a stored one), aggregating per-writer errors via errors.Join. A
// panic in one writer's Flush/Close is recovered and reported as an error
// rather than crashing the process during shutdown — mirroring the panic
// recovery already in SendReport/SendMetrics.
func (s *MetricsSink) Close(ctx context.Context) error {
	s.mu.RLock()
	writers := slices.Clone(s.writers)
	s.mu.RUnlock()

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, w := range writers {
		wg.Add(1)
		go func(writer MetricsWriter) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("writer panic recovered during close", "panic", r, "writer", fmt.Sprintf("%T", writer))
					mu.Lock()
					errs = append(errs, fmt.Errorf("writer %T panicked during close: %v", writer, r))
					mu.Unlock()
				}
			}()
			if err := writer.Flush(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			if err := writer.Close(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	return errors.Join(errs...)
}
