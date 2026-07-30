package main

import (
	"cmp"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tempestwx-utilities/internal/config"
	"tempestwx-utilities/internal/httpserver"
	"tempestwx-utilities/internal/otel"
	"tempestwx-utilities/internal/postgres"
	"tempestwx-utilities/internal/prometheus"
	"tempestwx-utilities/internal/radar"
	"tempestwx-utilities/internal/sink"
	"tempestwx-utilities/internal/sqlite"
	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/tempestudp"
	"tempestwx-utilities/web"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	otelapi "go.opentelemetry.io/otel"
	otellogglobal "go.opentelemetry.io/otel/log/global"
)

// version is the running binary's version, recorded as the OTel
// service.version resource attribute (see otel.Config.ServiceVersion). It is
// injected at build time via `-ldflags -X main.version=${VERSION}` (see the
// Dockerfile and buildx bake config); it stays the "dev" default for
// non-release/local builds that don't set VERSION.
var version = "dev"

// teeHandler fans every slog.Record out to multiple handlers. It exists to
// work around a real stdlib side effect: slog.SetDefault(l) calls
// log.SetOutput(&handlerWriter{l.Handler(), ...}) whenever l.Handler() isn't
// the unexported *defaultHandler type (see log/slog/logger.go's SetDefault
// doc comment), which means installing the OTel log bridge as the sole slog
// default would silently redirect ALL of main's existing log.Printf/log.Fatal
// output away from stderr into the OTel pipeline only. Fanning out to a
// plain stderr handler alongside the OTel one preserves that visibility.
type teeHandler struct {
	handlers []slog.Handler
}

// newTeeHandler returns a slog.Handler that forwards every record to each of
// handlers.
func newTeeHandler(handlers ...slog.Handler) *teeHandler {
	return &teeHandler{handlers: handlers}
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, sub := range h.handlers {
		if sub.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, sub := range h.handlers {
		if !sub.Enabled(ctx, r.Level) {
			continue
		}
		if err := sub.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		next[i] = sub.WithAttrs(attrs)
	}
	return &teeHandler{handlers: next}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		next[i] = sub.WithGroup(name)
	}
	return &teeHandler{handlers: next}
}

// Old collector implementation removed - now using MetricsSink

// notifyFunc matches signal.NotifyContext's signature so tests can inject a
// fake and assert on the exact signal set without sending real signals.
type notifyFunc func(parent context.Context, sig ...os.Signal) (context.Context, context.CancelFunc)

// signalContext derives a context that is canceled on SIGINT or SIGTERM,
// giving deferred cleanup (e.g. sink.Close, PostgresWriter.Close) a chance to
// run on graceful shutdown (resolves A-H1: SIGTERM was not handled).
func signalContext(parent context.Context, notify notifyFunc) (context.Context, context.CancelFunc) {
	return notify(parent, os.Interrupt, syscall.SIGTERM)
}

// Mode identifies which operational mode main is running in, since the
// "at least one writer" invariant differs between them (see requireWriters).
type Mode int

const (
	ModeUDP       Mode = iota // UDP listener (no TOKEN)
	ModeAPIExport             // historical export (TOKEN set)
)

// requireWriters enforces the "at least one writer" invariant, but only where
// it applies: UDP mode always needs a writer; API-export mode is satisfied by a
// DB writer OR KEEP_EXPORT_FILES (fixes A-H2 — gzip-only export was unreachable).
func requireWriters(mode Mode, writerCount int, keepFiles bool) error {
	if writerCount > 0 {
		return nil
	}
	if mode == ModeAPIExport && keepFiles {
		return nil
	}
	return fmt.Errorf("no writers configured: set ENABLE_POSTGRES / ENABLE_OTEL / ENABLE_PROMETHEUS_* (or KEEP_EXPORT_FILES in API-export mode)")
}

// storeChoice is the result of selectStore: which persistence backends to
// register, and (when sqlite is selected) the path to open it at.
type storeChoice struct {
	postgres   bool
	sqlite     bool
	sqlitePath string
}

// selectStore: SQLite is the default store (R2); Postgres is opt-in via
// ENABLE_POSTGRES. Both may run concurrently (fan-out). SQLite is disabled only
// when Postgres is the sole configured store AND no SQLITE_PATH override is set.
func selectStore(enablePostgres bool, sqlitePathEnv string) storeChoice {
	c := storeChoice{postgres: enablePostgres}
	if !enablePostgres || sqlitePathEnv != "" {
		c.sqlite = true
		c.sqlitePath = cmp.Or(sqlitePathEnv, "/data/tempest.db")
	}
	return c
}

// runHealthcheck implements the `tempestwx-utilities healthcheck` subcommand
// used by the Docker HEALTHCHECK instruction: the chainguard/static final
// image has no shell/curl/wget to run a conventional CMD probe, so the
// binary probes itself instead. It GETs /healthz on the same HTTP_ADDR the
// running server bound (default ":8080", matching srv.Addr below) and
// returns 0 if that responds 200, 1 otherwise.
func runHealthcheck() int {
	addr := cmp.Or(os.Getenv("HTTP_ADDR"), ":8080")
	// HTTP_ADDR may be either ":8080" (bind-all shorthand) or "0.0.0.0:8080"
	// (explicit host:port) — net.SplitHostPort handles both, returning the
	// port either way. The healthcheck always probes 127.0.0.1 regardless of
	// the configured bind host, since it runs inside the same container as
	// the server it's checking. If SplitHostPort fails to find a port
	// separator at all (a malformed HTTP_ADDR with no ":"), fall back to
	// treating addr as the port suffix, matching the pre-fix behavior.
	port := addr
	if _, p, err := net.SplitHostPort(addr); err == nil {
		port = p
	}
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url) //nolint:gosec // G107: localhost self-healthcheck, addr from controlled HTTP_ADDR env, not external input
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// isKnownSubcommand reports whether name is a subcommand this binary
// dispatches. It exists so an unrecognized argument becomes a usage error
// instead of silently falling through to daemon mode — a typo such as
// `tempestwx-utilities backfil` previously started a UDP listener.
func isKnownSubcommand(name string) bool {
	switch name {
	case "healthcheck", "backfill":
		return true
	default:
		return false
	}
}

const usageText = `tempestwx-utilities — Tempest weather station data utilities

usage:
  tempestwx-utilities                 run the UDP listener / API export daemon (configured by env)
  tempestwx-utilities backfill [...]  fill gaps in the observation history from the Tempest REST API
  tempestwx-utilities healthcheck     probe the running server's /healthz endpoint

run "tempestwx-utilities backfill --help" for the backfill flags
`

func main() {
	// A non-flag first argument is a subcommand. An unknown one is a usage
	// error, never a silent fallthrough to daemon mode.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		if !isKnownSubcommand(os.Args[1]) {
			fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usageText)
			os.Exit(2)
		}
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(runHealthcheck())
		case "backfill":
			// runBackfill owns all of its cleanup via internal defers and
			// wires its own signal context.
			os.Exit(runBackfill(context.Background(), os.Args[2:]))
		default:
			// Unreachable while this switch and isKnownSubcommand agree — but
			// the subcommand name is duplicated across the two, so they CAN
			// desync. Without this arm a desync falls through to daemon mode
			// and silently starts the UDP listener, which is precisely the
			// bug the isKnownSubcommand check above exists to prevent.
			// Fail loudly instead.
			fmt.Fprintf(os.Stderr,
				"internal error: %q passed isKnownSubcommand but has no dispatch case\n\n%s",
				os.Args[1], usageText)
			os.Exit(2)
		}
	}

	ctx, done := signalContext(context.Background(), signal.NotifyContext)
	defer done()

	// Initialize sink for both modes
	metricsSink := sink.NewMetricsSink()

	// sqliteDB is set below (UDP mode only) when the sqlite store is
	// selected. It must be closed AFTER the sink drains the sqlite writer
	// (sink.Close flushes buffered writes) — hence it is closed inside the
	// same deferred cleanup, after metricsSink.Close returns, rather than via
	// its own defer (which LIFO ordering would run BEFORE the sink drains).
	var sqliteDB *sql.DB
	// sqliteRDB is the dedicated read-only handle (UDP mode only, when the
	// sqlite store is selected), opened after sqliteDB and passed to the
	// writer via sqlite.WithReadDB so query-side reads run concurrently with
	// the single ingest writer (WAL) instead of queuing behind it. Closed
	// alongside sqliteDB in the deferred cleanup below, after the sink drains.
	var sqliteRDB *sql.DB
	// sw is the *sqlite.Writer handle (UDP mode only, when the sqlite store is
	// selected), captured here so it can also be wired into httpserver.Deps.Observations
	// below -- the same writer instance both drains the metrics sink and backs
	// the JSON API's read endpoints.
	var sw *sqlite.Writer
	// srv is the UI/JSON-API HTTP server (UDP mode only; see the mode-gated
	// startup below). It must be shut down BEFORE metricsSink.Close so the API
	// stops accepting reads before the writers it reads from start draining.
	var srv *http.Server
	// otelShutdown is set below (ENABLE_OTEL only) to the func Setup returns.
	// It runs LAST in the deferred cleanup, after the sink has drained and
	// sqlite has closed, so any buffered OTel data from those Close calls
	// (traces/metrics/logs already emitted during the run) still has a chance
	// to flush before the providers shut down.
	var otelShutdown func(context.Context) error
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if srv != nil {
			if err := srv.Shutdown(cleanupCtx); err != nil {
				slog.Error("http server shutdown", "err", err)
			}
		}
		if err := metricsSink.Close(cleanupCtx); err != nil {
			slog.Error("sink close", "err", err)
		}
		if sqliteRDB != nil {
			if err := sqliteRDB.Close(); err != nil {
				slog.Error("sqlite read db close", "err", err)
			}
		}
		if sqliteDB != nil {
			if err := sqliteDB.Close(); err != nil {
				slog.Error("sqlite db close", "err", err)
			}
		}
		if otelShutdown != nil {
			if err := otelShutdown(cleanupCtx); err != nil {
				slog.Error("otel shutdown", "err", err)
			}
		}
	}()

	// Configure Postgres opt-in and select the store(s) (R2: sqlite default,
	// postgres opt-in; see selectStore).
	enablePostgres, err := config.ParseBoolEnv("ENABLE_POSTGRES")
	if err != nil {
		log.Fatal(err) //nolint:gocritic // log.Fatal on a startup config error exits before any writer buffers data, so the skipped deferred sink Close is harmless
	}
	choice := selectStore(enablePostgres, os.Getenv("SQLITE_PATH"))

	// Configure Prometheus + SQLite writers (UDP mode only)
	token := os.Getenv("TOKEN")
	if token == "" {
		enablePushgateway, err := config.ParseBoolEnv("ENABLE_PROMETHEUS_PUSHGATEWAY")
		if err != nil {
			log.Fatal(err) //nolint:gocritic // log.Fatal on a startup config error exits before any writer buffers data, so the skipped deferred sink Close is harmless
		}
		if enablePushgateway {
			pushURL := os.Getenv("PROMETHEUS_PUSHGATEWAY_URL")
			if pushURL == "" {
				log.Fatal("PROMETHEUS_PUSHGATEWAY_URL is required when ENABLE_PROMETHEUS_PUSHGATEWAY is true") //nolint:gocritic // log.Fatal on a startup config error exits before any writer buffers data, so the skipped deferred sink Close is harmless
			}
			jobName := os.Getenv("JOB_NAME")
			if jobName == "" {
				jobName = "tempest"
			}
			promWriter := prometheus.NewPrometheusWriter(pushURL, jobName)
			metricsSink.AddWriter(promWriter)
		}

		// Configure Prometheus metrics server (scrape endpoint)
		enableMetrics, err := config.ParseBoolEnv("ENABLE_PROMETHEUS_METRICS")
		if err != nil {
			log.Fatal(err)
		}
		if enableMetrics {
			port := os.Getenv("PROMETHEUS_METRICS_PORT")
			if port == "" {
				port = "9000"
			}
			metricsServer := prometheus.NewMetricsServer(port)
			if err := metricsServer.Start(); err != nil {
				log.Fatalf("failed to start metrics server: %v", err)
			}
			metricsSink.AddWriter(metricsServer)
		}

		// Configure SQLite writer. UDP-mode only: SQLite.WriteMetrics is a
		// no-op (design §10 / operational-modes table routes API-export to
		// Postgres/gz, not sqlite), so registering it in API-export mode
		// would spuriously satisfy requireWriters while silently writing
		// nothing. selectStore itself stays mode-agnostic; only this
		// registration is UDP-gated.
		if choice.sqlite {
			sqliteCfg := sqlite.LoadConfig(os.Getenv)
			db, err := sqlite.Open(ctx, choice.sqlitePath, sqliteCfg)
			if err != nil {
				log.Fatalf("failed to open sqlite: %v", err)
			}
			sqliteDB = db
			rdb, err := sqlite.OpenReadOnly(ctx, choice.sqlitePath, sqliteCfg)
			if err != nil {
				log.Fatalf("failed to open sqlite read handle: %v", err)
			}
			sqliteRDB = rdb
			sw = sqlite.NewWriter(ctx, db, sqliteCfg, sqlite.WithReadDB(rdb))
			metricsSink.AddWriter(sw)
		}
	}

	// Configure Postgres writer (both modes)
	if choice.postgres {
		dbConfig, err := config.GetDatabaseConfig()
		if err != nil {
			log.Fatalf("database configuration error: %v", err)
		}
		if dbConfig == "" {
			log.Fatal("POSTGRES_URL or POSTGRES_HOST is required when ENABLE_POSTGRES is true")
		}
		pgWriter, err := postgres.NewPostgresWriter(ctx, dbConfig)
		if err != nil {
			log.Fatalf("failed to initialize postgres: %v", err)
		}
		metricsSink.AddWriter(pgWriter)
	}

	// Configure OTel (both modes): Setup registers the meter/tracer/logger
	// providers as OTel globals, the sink metrics writer is registered like
	// any other writer, and slog's default logger is redirected to the OTel
	// log bridge (Task 6.4) so internal/sink's slog calls flow to OTel too.
	enableOTEL, err := config.ParseBoolEnv("ENABLE_OTEL")
	if err != nil {
		log.Fatal(err)
	}
	if enableOTEL {
		endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if endpoint == "" {
			log.Fatal("OTEL_EXPORTER_OTLP_ENDPOINT is required when ENABLE_OTEL is true")
		}
		otelCfg := otel.Config{
			Endpoint:       endpoint,
			ServiceVersion: version,
			Serial:         os.Getenv("TEMPEST_SERIAL"),
		}
		shutdown, err := otel.Setup(ctx, otelCfg)
		if err != nil {
			log.Fatalf("failed to initialize otel: %v", err)
		}
		otelShutdown = shutdown

		otelWriter, err := otel.NewWriter(otelapi.GetMeterProvider())
		if err != nil {
			log.Fatalf("failed to create otel writer: %v", err)
		}
		metricsSink.AddWriter(otelWriter)

		// slog.SetDefault redirects stdlib log's own default output (see
		// teeHandler's doc comment) — so the default handler fans out to
		// BOTH the OTel bridge and a plain stderr handler, preserving the
		// existing log.Printf/log.Fatal container-log visibility instead of
		// silently replacing it with OTel-only export.
		otelHandler := otel.NewSlogHandler(otellogglobal.GetLoggerProvider())
		stderrHandler := slog.NewTextHandler(os.Stderr, nil)
		slog.SetDefault(slog.New(newTeeHandler(otelHandler, stderrHandler)))
	}

	// Require at least one writer (relaxed for gzip-only API-export mode; see requireWriters)
	mode := ModeUDP
	if token != "" {
		mode = ModeAPIExport
	}
	keepFiles, err := config.ParseBoolEnv("KEEP_EXPORT_FILES")
	if err != nil {
		log.Fatal(err)
	}
	if err := requireWriters(mode, metricsSink.WriterCount(), keepFiles); err != nil {
		log.Fatal(err)
	}

	// Start the UI/JSON-API HTTP server (UDP mode only): API-export mode is a
	// batch job that runs to completion and exits (exportWithSink, below), so
	// a long-running server has no place there. deps.Observations is left nil
	// when sqlite is disabled (postgres-only edge case) -- assigning sw
	// directly would wrap a nil *sqlite.Writer in a non-nil ObservationReader
	// interface, defeating the handlers' nil guard, so it is only set when sw
	// is actually non-nil.
	//
	// TOKEN-in-UDP-mode limitation (flagged, not fixed here): token is always
	// empty in this branch (a non-empty TOKEN switches to ModeAPIExport
	// above), so the WeatherFlow proxy (/api/forecast|almanac|station) has no
	// credential to authenticate with and degrades to an upstream 401. Wiring
	// a separate token source for UDP mode is a design decision for a
	// follow-up task, not this one -- see the task report.
	if mode == ModeUDP {
		deps := httpserver.Deps{
			StaticFS:    web.DistFS(),
			WeatherFlow: tempestapi.NewClient(token),
		}
		if sw != nil {
			deps.Observations = sw
		}
		enableRadar, err := config.ParseBoolEnv("ENABLE_RADAR")
		if err != nil {
			log.Fatal(err)
		}
		if enableRadar {
			sidecarURL := cmp.Or(os.Getenv("RADAR_SIDECAR_URL"), "http://radar-sidecar:8081")
			deps.Radar = radar.NewProxy(sidecarURL)
		}
		srv = httpserver.New(deps)
		srv.Addr = cmp.Or(os.Getenv("HTTP_ADDR"), ":8080")
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server", "err", err)
			}
		}()
		slog.Info("http server listening", "addr", srv.Addr)
	}

	// Choose operational mode
	if token != "" {
		exportWithSink(ctx, token, metricsSink)
	} else {
		listenAndPushWithSink(ctx, metricsSink)
	}
}

func listenAndPushWithSink(ctx context.Context, metricsSink *sink.MetricsSink) {
	logUDP, err := config.ParseBoolEnv("LOG_UDP")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("starting UDP listener mode")

	if err := listen(ctx, func(b []byte, addr *net.UDPAddr) error {
		if logUDP {
			log.Printf("UDP in: %s", string(b))
		}

		// TracedIngest wraps parse + send-to-all-writers in a
		// udp.receive -> report.parse -> sink.write span chain. A parse
		// error is recorded on its span and skips the send entirely
		// (preserving the prior log-and-continue behavior below); any
		// error is logged, never fatal.
		if err := otel.TracedIngest(ctx, b, tempestudp.ParseReport, metricsSink.SendReport); err != nil {
			log.Printf("error processing report from %s: %v", addr, err)
		}

		return nil
	}); err != nil {
		log.Fatal(err)
	}
}

// Old listenAndPush implementation removed - now using listenAndPushWithSink

func listen(ctx context.Context, rx func([]byte, *net.UDPAddr) error) error {
	sock, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   nil,
		Port: 50222,
	})
	if err != nil {
		return err
	}
	defer sock.Close() //nolint:errcheck // UDP listener teardown revisited under graceful shutdown in Task 0.8
	log.Printf("listening on UDP :50222")

	readErr := make(chan error, 1)

	// Start reading in the background
	go func() {
		buffer := make([]byte, 1500)
		for {
			n, addr, err := sock.ReadFromUDP(buffer)
			if err != nil {
				readErr <- err
				break
			}
			err = rx(buffer[:n], addr)
			if err != nil {
				readErr <- err
				break
			}
		}
		close(readErr)
	}()

	// Wait for reading to finish, or for our context to finish
	select {
	case err := <-readErr:
		return err

	case <-ctx.Done():
		return nil
	}
}

func exportWithSink(ctx context.Context, token string, metricsSink *sink.MetricsSink) {
	client := tempestapi.NewClient(token)
	stations, err := client.ListStations(ctx)
	if err != nil {
		log.Fatalf("error listing stations: %v", err)
	}

	if len(stations) == 0 {
		log.Fatalf("no stations found")
	}

	log.Printf("found stations:")
	var startAt time.Time
	for _, station := range stations {
		log.Printf("  - %s (station #%d)", station.Name, station.StationID)
		if startAt.IsZero() || startAt.Before(station.CreatedAt) {
			startAt = station.CreatedAt
		}
	}

	keepFiles, err := config.ParseBoolEnv("KEEP_EXPORT_FILES")
	if err != nil {
		log.Fatal(err)
	}
	fileNum := 1

	var next time.Time
	cur := startAt
	for {
		var metrics []promclient.Metric

		for ; cur.Before(time.Now()) && len(metrics) < 200_000; cur = next {
			next = cur.AddDate(0, 0, 1)

			for _, station := range stations {
				log.Printf("fetching %s starting %s", station.Name, cur.Format(time.RFC3339))
				stationMetrics, err := client.GetObservations(ctx, station, cur, next)
				if err != nil {
					log.Fatalf("error fetching %#v for %d-%d: %v", station, cur.Unix(), next.Unix(), err) //nolint:gosec // pre-existing: station/error data logged unsanitized; deferred as follow-up hardening, not owned by a current task
				}
				metrics = append(metrics, stationMetrics...)
			}
		}

		if len(metrics) == 0 {
			break
		}

		// Send to sink (Postgres), wrapped in an export.batch span so
		// each batch send shows up in the trace backend the same way
		// UDP ingest's sink.write does.
		log.Printf("sending %d metrics to sink", len(metrics)) //nolint:gosec // pre-existing: logs a count derived from tainted input, not raw content; deferred as follow-up hardening, not owned by a current task
		if err := otel.TraceExportBatch(ctx, func(ctx context.Context) error {
			return metricsSink.SendMetrics(ctx, metrics)
		}); err != nil {
			log.Printf("error sending metrics: %v", err)
		}

		// Optionally write to .gz files
		if keepFiles {
			filename := fmt.Sprintf("tempest_%03d.txt.gz", fileNum)
			if err := writeMetricsToFile(filename, metrics); err != nil {
				log.Fatalf("error writing file: %v", err)
			}
			fileNum++
		}
	}

	log.Printf("export complete")
}

func writeMetricsToFile(filename string, metrics []promclient.Metric) error {
	// Create collector for metrics
	collector := &staticCollector{metrics: metrics}

	r := promclient.NewRegistry()
	r.MustRegister(collector)
	families, err := r.Gather()
	if err != nil {
		return fmt.Errorf("gather metrics: %w", err)
	}

	log.Printf("writing %s", filename)
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // G302/G304: 0o644 perms and export filename are intentional, not user-controlled in a way that risks traversal
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close() //nolint:errcheck // Close handling for export writers revisited in Task 0.11

	gzw := gzip.NewWriter(f)
	defer gzw.Close() //nolint:errcheck // Close handling for export writers revisited in Task 0.11

	enc := expfmt.NewEncoder(gzw, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := enc.Encode(family); err != nil {
			return fmt.Errorf("encode metrics: %w", err)
		}
	}

	if c, ok := enc.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return fmt.Errorf("close encoder: %w", err)
		}
	}

	return nil
}

// staticCollector holds a static list of metrics
type staticCollector struct {
	metrics []promclient.Metric
}

func (c *staticCollector) Describe(descs chan<- *promclient.Desc) {
	// Not needed
}

func (c *staticCollector) Collect(metrics chan<- promclient.Metric) {
	for _, m := range c.metrics {
		metrics <- m
	}
}
