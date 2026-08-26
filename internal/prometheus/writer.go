package prometheus

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/prometheus/common/expfmt"
)

// pushTimeout bounds every pusher.Add() call (periodic pushes and the
// final-flush push in pushWorker) so a dead/slow push gateway can never
// stall shutdown. Add() uses context.Background() internally, so without
// this the pusher's HTTP client would otherwise have no deadline at all.
const pushTimeout = 10 * time.Second

// PrometheusWriter wraps the existing Prometheus push logic. The name
// stutters (prometheus.PrometheusWriter) but is an established,
// widely-referenced identifier (main.go); renaming is a cross-file rename
// out of scope for this lint-debt pass, not a doc-comment fix.
//
//nolint:revive // established name; see doc comment above
type PrometheusWriter struct {
	pusher *push.Pusher
	outbox chan prometheus.Metric
	more   chan bool
	wg     sync.WaitGroup

	// done is the sole shutdown signal: closing it tells every producer
	// send in WriteMetrics/Flush and pushWorker that Close is in
	// progress. outbox and more are never closed (see Close), which is
	// what keeps a concurrent producer send from ever panicking on a
	// send-on-closed-channel (D-H1).
	done      chan struct{}
	closeOnce sync.Once
}

// NewPrometheusWriter creates a new Prometheus writer.
func NewPrometheusWriter(pushURL, jobName string) *PrometheusWriter {
	warnDeprecated()

	outbox := make(chan prometheus.Metric, 1000)
	more := make(chan bool, 1)

	// Create collector that drains outbox
	collector := &outboxCollector{outbox: outbox}

	// Create pusher
	pusher := push.New(pushURL, jobName).
		Collector(collector).
		Format(expfmt.NewFormat(expfmt.TypeTextPlain)).
		Client(&http.Client{Timeout: pushTimeout})

	w := &PrometheusWriter{
		pusher: pusher,
		outbox: outbox,
		more:   more,
		done:   make(chan struct{}),
	}

	// Start background push worker
	w.wg.Add(1)
	go w.pushWorker()

	log.Printf("prometheus: configured push to %q with job %q", pushURL, jobName)

	return w
}

// WriteReport converts report to metrics and queues for pushing.
func (w *PrometheusWriter) WriteReport(ctx context.Context, report tempestudp.Report) error {
	metrics := report.Metrics()
	return w.WriteMetrics(ctx, metrics)
}

// WriteMetrics queues metrics for pushing.
func (w *PrometheusWriter) WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	for _, m := range metrics {
		select {
		case w.outbox <- m:
		case <-w.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			log.Printf("prometheus: outbox full, dropping metric")
		}
	}

	// Signal push worker
	select {
	case w.more <- true:
	case <-w.done:
	default:
		// Already signaled
	}

	return nil
}

// Flush is a no-op for Prometheus (push happens in background).
func (w *PrometheusWriter) Flush(ctx context.Context) error {
	// Trigger immediate push
	select {
	case w.more <- true:
	case <-w.done:
	default:
	}
	return nil
}

// Close signals the push worker to stop via the done gate and waits for it
// to finish. It is idempotent (safe to call more than once) and never closes
// outbox or more, so a concurrent WriteMetrics/Flush send can never panic on
// a send-on-closed-channel (D-H1).
func (w *PrometheusWriter) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		close(w.done)
		w.wg.Wait() // Wait for pushWorker to finish
	})
	log.Printf("prometheus: closed")
	return nil
}

func (w *PrometheusWriter) pushWorker() {
	defer w.wg.Done()
	for {
		select {
		case <-w.more:
			if err := w.pusher.Add(); err != nil {
				log.Printf("prometheus: push error: %v", err)
			}
		case <-w.done:
			_ = w.pusher.Add() // final flush of whatever the collector can drain
			return
		}
	}
}

// outboxCollector drains the outbox channel non-blockingly.
type outboxCollector struct {
	outbox <-chan prometheus.Metric
}

func (c *outboxCollector) Describe(descs chan<- *prometheus.Desc) {
	// Don't need to describe - metrics are already created
}

func (c *outboxCollector) Collect(metrics chan<- prometheus.Metric) {
	for {
		select {
		case m := <-c.outbox:
			metrics <- m
		default:
			return
		}
	}
}
