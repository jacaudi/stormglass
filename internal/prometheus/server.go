package prometheus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jacaudi/stormglass/internal/metrics"
	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// MetricsServer exposes a /metrics endpoint for Prometheus scraping.
// It implements the sink.MetricsWriter interface.
type MetricsServer struct {
	port       string
	server     *http.Server
	collector  *latestMetricsCollector
	registry   *prometheus.Registry
	shutdownWg sync.WaitGroup
}

// NewMetricsServer creates a new metrics server listening on the given port.
func NewMetricsServer(port string) *MetricsServer {
	warnDeprecated()

	collector := &latestMetricsCollector{
		metrics: make(map[string]prometheus.Metric),
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			log.Printf("metrics: health response write error: %v", err)
		}
	})

	addr := ":" + port
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &MetricsServer{
		port:      port,
		server:    server,
		collector: collector,
		registry:  registry,
	}
}

// Start binds the metrics listener synchronously, returning any bind error
// (e.g. port already in use) to the caller, then serves the endpoint in a
// background goroutine.
func (s *MetricsServer) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("metrics: listen on %s: %w", s.server.Addr, err)
	}

	s.shutdownWg.Go(func() {
		log.Printf("metrics: starting HTTP server on port %s", s.port)
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics: server error: %v", err)
		}
	})
	return nil
}

// WriteReport converts a UDP report to metrics and stores them.
func (s *MetricsServer) WriteReport(ctx context.Context, report tempestudp.Report) error {
	metrics := report.Metrics()
	return s.WriteMetrics(ctx, metrics)
}

// WriteMetrics stores the latest metrics for scraping.
func (s *MetricsServer) WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	s.collector.Update(metrics)
	return nil
}

// Flush is a no-op for the metrics server.
func (s *MetricsServer) Flush(ctx context.Context) error {
	return nil
}

// Close shuts down the HTTP server gracefully. The passed-in ctx is accepted
// for MetricsWriter conformance and currently unused — the shutdown still
// builds its own bounded 5s context internally; see Task 0.9b/0.10.
func (s *MetricsServer) Close(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics: shutdown error: %v", err)
		return err
	}

	s.shutdownWg.Wait()
	log.Printf("metrics: server closed")
	return nil
}

// latestMetricsCollector stores the latest value for each unique metric.
// This allows Prometheus to scrape the current values at any time.
type latestMetricsCollector struct {
	mu      sync.RWMutex
	metrics map[string]prometheus.Metric
}

// Update stores or replaces metrics with the same identity.
func (c *latestMetricsCollector) Update(metrics []prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, m := range metrics {
		key := metricKey(m)
		c.metrics[key] = m
	}
}

// Describe sends all metric descriptors to the channel.
func (c *latestMetricsCollector) Describe(descs chan<- *prometheus.Desc) {
	// Send all known metric descriptors
	for _, desc := range metrics.All {
		descs <- desc
	}
}

// Collect sends all stored metrics to the channel.
func (c *latestMetricsCollector) Collect(metrics chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, m := range c.metrics {
		metrics <- m
	}
}

// metricKey generates a unique key for a metric based on its description and labels.
func metricKey(m prometheus.Metric) string {
	var d dto.Metric
	if err := m.Write(&d); err != nil {
		// Fallback to description string if write fails
		return m.Desc().String()
	}

	// Build key from description and label values
	var key strings.Builder
	key.WriteString(m.Desc().String())
	for _, label := range d.Label {
		fmt.Fprintf(&key, ",%s=%s", label.GetName(), label.GetValue())
	}
	return key.String()
}
