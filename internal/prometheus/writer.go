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
	dto "github.com/prometheus/client_model/go"
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
	collector := newPushCollector(outbox)

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

// newPushCollector builds the collector the pusher gathers from.
// newPushCollector builds the collector the pusher gathers from.
//
// The push path needs two things the scrape path does not (issue #187):
//
//  1. No timestamps. A real Pushgateway rejects any pushed sample carrying
//     one -- 400 "pushed metrics must not have timestamps" -- so every push
//     from this path failed. report.go wraps each metric with
//     NewMetricWithTimestamp for the scrape path, which is correct there and
//     fatal here.
//
//  2. Dedupe by series. Stripping alone would trade a 400 for a worse
//     failure: client_golang hashes the timestamp into its uniqueness key
//     (registry.go), so today two samples of one series hash apart. Remove
//     the timestamps and they collide, and Gather() aborts the whole push
//     locally -- before any HTTP request -- with "was collected before with
//     the same name and label values". outboxCollector drains the entire
//     channel per push, so two obs_st broadcasts between pushes make that
//     the common case, not an edge.
//
// Last-queued wins, which is both correct and lossless in the only sense
// that matters here: the Pushgateway retains just the last pushed state per
// series, so intermediate samples in a single push could not be represented
// even if sent -- and the scrape path's latestMetricsCollector already keeps
// latest-per-series, so this gives the push path parity. The outbox is FIFO,
// so last-queued is the newest broadcast.
func newPushCollector(outbox <-chan prometheus.Metric) prometheus.Collector {
	return &pushCollector{outbox: outbox}
}

type pushCollector struct {
	outbox <-chan prometheus.Metric
}

func (c *pushCollector) Describe(chan<- *prometheus.Desc) {
	// Unchecked collector: the metrics are already fully described.
}

func (c *pushCollector) Collect(metrics chan<- prometheus.Metric) {
	// Ordered so the emitted batch stays FIFO; the map holds the winner.
	var order []string
	latest := make(map[string]prometheus.Metric)

	for drained := false; !drained; {
		select {
		case m := <-c.outbox:
			// metricKey (server.go) is desc + label NAMES AND VALUES. Keying
			// on Desc().String() alone would carry names but not values, so
			// two stations' serial-labelled series would collapse into one.
			k := metricKey(m)
			if _, seen := latest[k]; !seen {
				order = append(order, k)
			}
			latest[k] = m
		default:
			drained = true
		}
	}

	for _, k := range order {
		metrics <- &timestampStrippingMetric{Metric: latest[k]}
	}
}

// timestampStrippingMetric delegates Write, then clears TimestampMs.
type timestampStrippingMetric struct{ prometheus.Metric }

func (m *timestampStrippingMetric) Write(out *dto.Metric) error {
	if err := m.Metric.Write(out); err != nil {
		return err
	}
	out.TimestampMs = nil
	return nil
}
