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

// dispatchSubcommand checks for a "backfill" or "healthcheck" subcommand as
// the first CLI argument and, if present, runs it and exits the process
// (never returning). If no subcommand is present -- no args, or the first
// arg looks like a flag -- it returns immediately and main continues into
// daemon mode. (Equivalent to main's original `if len(os.Args) > 1 &&
// !strings.HasPrefix(os.Args[1], "-") { ... }` guard, inverted into an
// early-return form.)
func dispatchSubcommand() {
	if len(os.Args) <= 1 || strings.HasPrefix(os.Args[1], "-") {
		return
	}
	// A non-flag first argument is a subcommand. An unknown one is a usage
	// error, never a silent fallthrough to daemon mode.
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

// cleanupResources runs main's shutdown sequence, in order: the HTTP server
// (so the API stops accepting reads before the writers it reads from start
// draining), the metrics sink (draining every registered writer), sqlite's
// read and write handles (after the sink has drained the sqlite writer),
// and finally the OTel providers (last, so any data emitted by the earlier
// Close calls still has a chance to flush before the providers shut down).
// Extracted verbatim from main's deferred cleanup closure; called from a
// zero-branch wrapper closure so main still reads srv/sqliteRDB/sqliteDB/
// otelShutdown at defer-EXECUTION time (their final values), not at the
// point the defer statement is registered (when they're all still nil).
func cleanupResources(srv *http.Server, metricsSink *sink.MetricsSink, sqliteRDB, sqliteDB *sql.DB, otelShutdown func(context.Context) error) {
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
}

// resolveStoreChoice reads ENABLE_POSTGRES and SQLITE_PATH and returns which
// store(s) main should configure (R2: sqlite default, postgres opt-in; see
// selectStore).
func resolveStoreChoice() storeChoice {
	enablePostgres, err := config.ParseBoolEnv("ENABLE_POSTGRES")
	if err != nil {
		log.Fatal(err)
	}
	return selectStore(enablePostgres, os.Getenv("SQLITE_PATH"))
}

// configurePrometheusWriters registers the Prometheus push-gateway and/or
// scrape-endpoint writers on metricsSink, if enabled via
// ENABLE_PROMETHEUS_PUSHGATEWAY / ENABLE_PROMETHEUS_METRICS. Called by main
// only in UDP mode (token == "").
func configurePrometheusWriters(metricsSink *sink.MetricsSink) {
	enablePushgateway, err := config.ParseBoolEnv("ENABLE_PROMETHEUS_PUSHGATEWAY")
	if err != nil {
		log.Fatal(err)
	}
	if enablePushgateway {
		pushURL := os.Getenv("PROMETHEUS_PUSHGATEWAY_URL")
		if pushURL == "" {
			log.Fatal("PROMETHEUS_PUSHGATEWAY_URL is required when ENABLE_PROMETHEUS_PUSHGATEWAY is true")
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
}

// configureSQLiteWriter opens the sqlite write and read-only handles and
// registers the writer on metricsSink, if choice.sqlite is set. Called by
// main only in UDP mode: SQLite.WriteMetrics is a no-op (design §10 /
// operational-modes table routes API-export to Postgres/gz, not sqlite), so
// registering it in API-export mode would spuriously satisfy requireWriters
// while silently writing nothing. selectStore itself stays mode-agnostic;
// only this registration is UDP-gated by main's caller.
func configureSQLiteWriter(ctx context.Context, metricsSink *sink.MetricsSink, choice storeChoice) (db, rdb *sql.DB, sw *sqlite.Writer) {
	if !choice.sqlite {
		return nil, nil, nil
	}
	sqliteCfg := sqlite.LoadConfig(os.Getenv)
	db, err := sqlite.Open(ctx, choice.sqlitePath, sqliteCfg)
	if err != nil {
		log.Fatalf("failed to open sqlite: %v", err)
	}
	rdb, err = sqlite.OpenReadOnly(ctx, choice.sqlitePath, sqliteCfg)
	if err != nil {
		log.Fatalf("failed to open sqlite read handle: %v", err)
	}
	sw = sqlite.NewWriter(ctx, db, sqliteCfg, sqlite.WithReadDB(rdb))
	metricsSink.AddWriter(sw)
	return db, rdb, sw
}

// configurePostgresWriter opens the Postgres pool and registers the writer
// on metricsSink, if choice.postgres is set. Called by main in both modes.
func configurePostgresWriter(ctx context.Context, metricsSink *sink.MetricsSink, choice storeChoice) {
	if !choice.postgres {
		return
	}
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

// configureOTel sets up OTel (called by main in both modes), if ENABLE_OTEL
// is set: Setup registers the meter/tracer/logger providers as OTel
// globals, the sink metrics writer is registered like any other writer, and
// slog's default logger is redirected to the OTel log bridge (Task 6.4) so
// internal/sink's slog calls flow to OTel too. Returns the shutdown func
// Setup returned, or nil if OTel is disabled.
func configureOTel(ctx context.Context, metricsSink *sink.MetricsSink) func(context.Context) error {
	enableOTEL, err := config.ParseBoolEnv("ENABLE_OTEL")
	if err != nil {
		log.Fatal(err)
	}
	if !enableOTEL {
		return nil
	}
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

	return shutdown
}

// resolveModeAndValidate determines the operational mode from token and
// validates that at least one writer is configured (relaxed for gzip-only
// API-export mode; see requireWriters), exiting fatally if not.
func resolveModeAndValidate(token string, writerCount int) Mode {
	mode := ModeUDP
	if token != "" {
		mode = ModeAPIExport
	}
	keepFiles, err := config.ParseBoolEnv("KEEP_EXPORT_FILES")
	if err != nil {
		log.Fatal(err)
	}
	if err := requireWriters(mode, writerCount, keepFiles); err != nil {
		log.Fatal(err)
	}
	return mode
}

// uiFlags are the three ENABLE_* opt-ins for the optional UI cards, already
// parsed. They are inputs to decideUI, not decisions.
type uiFlags struct {
	Forecast bool
	Almanac  bool
	Radar    bool
}

// uiDecision is what the server can actually serve, plus operator-facing
// diagnostics at two severities: a reason per enabled flag that cannot be
// honoured, and a warning per card that is served but degraded.
type uiDecision struct {
	Almanac bool
	Radar   bool
	// RadarSite is the site code to advertise on /api/station, or nil.
	// LoadStation decodes RADAR_SITE unconditionally because it is a pure
	// environment decoder; this is where the feature flag is applied.
	RadarSite *string
	// Reasons are logged at ERROR, one per unmet precondition. They are
	// never returned as an error, because there is no caller that should
	// treat them as fatal -- see decideUI's doc comment.
	Reasons []string
	// Warnings are logged at WARN: a card that IS mounted, but degraded by a
	// default the operator probably did not intend. Unlike Reasons, a warning
	// never gates route registration or the capability document -- the card
	// works, it is just not what they meant.
	Warnings []string
}

// unknownRadarSiteReason builds the ERROR reason for a RADAR_SITE that is not
// in the WSR-88D table. It names the offending value and, when the station's
// coordinates are known, the nearest site and the distance to it.
//
// It names the answer rather than the lookup table because the deployment this
// appliance ships as is a container: an operator cannot open
// internal/radar/sites.go (issue #169).
//
// The phrasing is deliberately neutral -- it states a fact, it does not say
// "try this". NearestSite has no distance ceiling, so for a station far from
// the NEXRAD network the nearest site is thousands of km away and useless; a
// London station is told about PLA in the Azores, 2540 km off. Stating the
// distance lets the operator draw that conclusion without decideUI having to
// rule on radar coverage physics, and without a threshold constant this
// project could not justify from a primary source.
//
// lat and lon are nil together or set together (config.LoadStation enforces
// it). The nil case is reachable and is why the guard lives here rather than
// at the call site: decideUI reports an unknown code and missing coordinates
// as two separate reasons, so this is called with no coordinates whenever both
// are wrong.
func unknownRadarSiteReason(site string, lat, lon *float64) string {
	const base = "ENABLE_RADAR is true but RADAR_SITE=%q is not a known WSR-88D site code. " +
		"Codes are three uppercase letters, usually not the ICAO form (TLX, not KTLX). "
	if lat == nil || lon == nil {
		return fmt.Sprintf(base+"The radar card will not be mounted.", site)
	}
	code, km := radar.NearestSite(*lat, *lon)
	return fmt.Sprintf(
		base+"The nearest site to your coordinates is %s, %.0f km away. "+
			"The radar card will not be mounted.",
		site, code, km)
}

// decideUI resolves which optional cards this process can serve and why any
// enabled card cannot be.
//
// It NEVER fails and never exits, and that is the whole point. startAPIServer
// runs before listenAndPushWithSink; log.Fatal exits past the deferred
// cleanupResources; and the compose deployment sets restart: unless-stopped.
// A fatal path for a flag that only decides whether a card renders would be a
// crash loop that stops UDP ingest into SQLite and Litestream -- the
// appliance's primary function. docker-compose.yml already ships RADAR_SITE
// empty, so under a fatal rule, flipping ENABLE_RADAR=true and nothing else
// would turn a dead card into a permanent data outage.
//
// Loud without being fatal, at two severities. An unmet precondition produces
// an ERROR naming the missing or invalid variables, leaves the route
// unregistered, and reports /api/capabilities false -- the mechanism issue
// #145 built, where one value gates both the routing and the document so they
// cannot disagree. A precondition that is MET but degraded by an unintended
// default produces a WARN instead: the route is registered and the capability
// is true, because the card works -- it is just not what the operator meant.
// STATION_TIMEZONE is the only such case today (issue #165).
//
// A MALFORMED value is a different matter and is already fatal, in
// config.LoadStation: absent means "the operator did not configure this
// feature", malformed means "the operator tried and got it wrong".
func decideUI(flags uiFlags, station config.StationConfig, hasStore bool) uiDecision {
	var d uiDecision
	hasCoords := station.Latitude != nil && station.Longitude != nil

	if flags.Forecast {
		d.Reasons = append(d.Reasons,
			"ENABLE_FORECAST is true but no forecast provider exists: the WeatherFlow proxy was removed "+
				"(issue #62, won't-do) and the tokenless NWS replacement is issue #81. "+
				"The forecast card will not be mounted.")
	}

	if flags.Almanac {
		switch {
		case !hasCoords:
			d.Reasons = append(d.Reasons,
				"ENABLE_ALMANAC is true but STATION_LATITUDE and STATION_LONGITUDE are not both set, "+
					"so sunrise and sunset cannot be computed. The almanac card will not be mounted.")
		case !hasStore:
			d.Reasons = append(d.Reasons,
				"ENABLE_ALMANAC is true but no observation store is configured, so the temperature "+
					"records have no source. SQLite is the default store; this is the Postgres-only "+
					"configuration. The almanac card will not be mounted.")
		default:
			d.Almanac = true
			if !station.TimezoneConfigured {
				d.Warnings = append(d.Warnings,
					"ENABLE_ALMANAC is true and coordinates are set, but STATION_TIMEZONE "+
						"is not: sunrise and sunset will render as UTC clock times, the "+
						"Today/This Week/This Month/This Year windows will use UTC calendar "+
						"boundaries, and the record date labels (\"Today\", \"Jan 2\") will be "+
						"UTC-dated. Set STATION_TIMEZONE to the station's IANA zone "+
						"(e.g. America/Denver).")
			}
		}
	}

	if flags.Radar {
		// Sequential checks, not a switch: an operator with a bad site code
		// AND no coordinates must learn both from one startup. A switch
		// selects exactly one arm, so either ordering costs that operator two
		// restarts to learn two things -- the property
		// TestDecideUI_ReportsEveryUnmetPrecondition asserts across the three
		// flags, now holding within this one too.
		radarOK := true
		switch {
		case station.RadarSite == nil:
			radarOK = false
			d.Reasons = append(d.Reasons,
				"ENABLE_RADAR is true but RADAR_SITE is not set, so no WSR-88D site can be requested. "+
					"The radar card will not be mounted.")
		case !radar.IsValidSite(*station.RadarSite):
			radarOK = false
			d.Reasons = append(d.Reasons,
				unknownRadarSiteReason(*station.RadarSite, station.Latitude, station.Longitude))
		}
		// Checked independently of the site, not as a third arm of the switch
		// above, because both must be reported in one startup. Note this is
		// NOT a nil-guard for the coordinates: they are passed to
		// unknownRadarSiteReason as pointers and nil-checked there, precisely
		// so that the reason can still be produced when they are absent.
		if !hasCoords {
			radarOK = false
			d.Reasons = append(d.Reasons,
				"ENABLE_RADAR is true but STATION_LATITUDE and STATION_LONGITUDE are not both set, "+
					"so the map has no centre. The radar card will not be mounted.")
		}
		if radarOK {
			d.Radar = true
			d.RadarSite = station.RadarSite
		}
	}

	return d
}

// startAPIServer starts the UI/JSON-API HTTP server for UDP mode and
// returns it; it returns nil when mode is not ModeUDP. API-export mode is a
// batch job that runs to completion and exits, so a long-running server has
// no place there.
//
// Every endpoint it serves is tokenless: observations and the almanac come
// from the local SQLite store, station identity from configuration, radar
// from the sidecar. There is no WeatherFlow credential in UDP mode and no
// code path that could use one -- issue #62 is closed as won't-do.
//
// deps.Observations is left nil when sqlite is disabled (the postgres-only
// edge case) -- assigning sw directly would wrap a nil *sqlite.Writer in a
// non-nil ObservationReader interface, defeating the handlers' nil guard.
//
// logger is injected rather than taken from slog.Default() so a test can
// observe the two diagnostic loops below without mutating a process global
// (slog.SetDefault also calls log.SetOutput and log.SetFlags(0), and restoring
// the previous logger undoes neither). main passes slog.Default(), which is
// equivalent to the previous implicit package-level calls for as long as
// nothing calls slog.SetDefault AFTER startAPIServer returns -- true today:
// the sole production SetDefault is in configureOTel, which main runs first.
func startAPIServer(mode Mode, station config.StationConfig, sw *sqlite.Writer, logger *slog.Logger) *http.Server {
	if mode != ModeUDP {
		return nil
	}

	// A malformed boolean stays fatal -- pre-existing behaviour, and the same
	// operator-error-versus-unconfigured-feature distinction LoadStation
	// draws. It is the flag VALUE being wrong, not a precondition being unmet.
	enableForecast, err := config.ParseBoolEnv("ENABLE_FORECAST")
	if err != nil {
		log.Fatal(err)
	}
	enableAlmanac, err := config.ParseBoolEnv("ENABLE_ALMANAC")
	if err != nil {
		log.Fatal(err)
	}
	enableRadar, err := config.ParseBoolEnv("ENABLE_RADAR")
	if err != nil {
		log.Fatal(err)
	}

	decision := decideUI(
		uiFlags{Forecast: enableForecast, Almanac: enableAlmanac, Radar: enableRadar},
		station, sw != nil,
	)
	for _, reason := range decision.Reasons {
		logger.Error("optional UI card not mounted", "reason", reason)
	}
	for _, warning := range decision.Warnings {
		logger.Warn("optional UI card degraded", "warning", warning)
	}

	// RADAR_SITE reaches the wire only when the radar card is actually
	// served, so /api/station never advertises a site for a card that is off.
	station.RadarSite = decision.RadarSite

	deps := httpserver.Deps{
		StaticFS: web.DistFS(),
		Station:  station,
		Almanac:  decision.Almanac,
	}
	if sw != nil {
		deps.Observations = sw
	}
	if decision.Radar {
		sidecarURL := cmp.Or(os.Getenv("RADAR_SIDECAR_URL"), "http://radar-sidecar:8081")
		deps.Radar = radar.NewProxy(sidecarURL)
	}

	srv := httpserver.New(deps)
	srv.Addr = cmp.Or(os.Getenv("HTTP_ADDR"), ":8080")
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
		}
	}()
	logger.Info("http server listening", "addr", srv.Addr)
	return srv
}

func main() {
	dispatchSubcommand()

	// Station identity is validated at the boundary, ahead of any resource
	// attachment (go-standards §15.3, 12-Factor III). Validating after the
	// store and OTel are open would mean a malformed value produces a partial
	// startup that then exits past the deferred cleanupResources.
	//
	// Runs in both modes. A malformed STATION_* value is an operator error
	// wherever it appears; absent values are never an error.
	stationCfg, err := config.LoadStation()
	if err != nil {
		log.Fatal(err)
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
		cleanupResources(srv, metricsSink, sqliteRDB, sqliteDB, otelShutdown)
	}()

	// Configure Postgres opt-in and select the store(s) (R2: sqlite default,
	// postgres opt-in; see selectStore).
	choice := resolveStoreChoice()

	// Configure Prometheus + SQLite writers (UDP mode only)
	token := os.Getenv("TOKEN")
	if token == "" {
		configurePrometheusWriters(metricsSink)
		sqliteDB, sqliteRDB, sw = configureSQLiteWriter(ctx, metricsSink, choice)
	}

	// Configure Postgres writer (both modes)
	configurePostgresWriter(ctx, metricsSink, choice)

	// Configure OTel (both modes)
	otelShutdown = configureOTel(ctx, metricsSink)

	// Require at least one writer (relaxed for gzip-only API-export mode; see requireWriters)
	mode := resolveModeAndValidate(token, metricsSink.WriterCount())

	// Start the UI/JSON-API HTTP server (UDP mode only)
	// slog.Default() rather than a locally built logger: configureOTel has
	// already run above, so when ENABLE_OTEL is set this is the teeHandler that
	// fans records to both stderr and the OTel log bridge.
	srv = startAPIServer(mode, stationCfg, sw, slog.Default())

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

// fetchStationsAndStartTime lists the account's stations (fatal if the call
// fails or none are found, matching exportWithSink's original inline
// behavior) and computes the earliest export start time: the latest
// CreatedAt across all stations, i.e. the newest station's creation date,
// exactly as the original loop computed startAt.
func fetchStationsAndStartTime(ctx context.Context, client *tempestapi.Client) ([]tempestapi.Station, time.Time) {
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
	return stations, startAt
}

// fetchDailyBatch accumulates observations across day-sized [cur, next)
// windows starting at cur, one window per station per iteration, until
// either the window catches up to time.Now() or 200,000 metrics have been
// collected. It returns the accumulated metrics and the cur value to resume
// from on the next call -- the same value the original inner for-loop's
// `cur = next` post-statement would have left cur at when the loop exited.
func fetchDailyBatch(ctx context.Context, client *tempestapi.Client, stations []tempestapi.Station, cur time.Time) ([]promclient.Metric, time.Time) {
	var metrics []promclient.Metric
	var next time.Time

	for ; cur.Before(time.Now()) && len(metrics) < 200_000; cur = next {
		next = cur.AddDate(0, 0, 1)

		for _, station := range stations {
			// station.Name originates from the WeatherFlow API (see the
			// slog.ErrorContext comment a few lines below for the same
			// provenance) -- unchanged from the original inline loop this
			// line was extracted from verbatim, which gosec's taint analysis
			// did not flag; the same text flags G706 only once it crosses
			// this function's parameter boundary. Not user input reachable
			// from outside the operator's own WeatherFlow account.
			log.Printf("fetching %s starting %s", station.Name, cur.Format(time.RFC3339)) //nolint:gosec // G706: log injection false positive, see comment above
			stationMetrics, err := client.GetObservations(ctx, station, cur, next)
			if err != nil {
				// station and err are derived from the WeatherFlow API response and
				// are passed as slog attribute values (not interpolated into the
				// format string) so the handler's quoting/escaping neutralizes any
				// embedded control characters. Exit behavior matches log.Fatalf
				// (log, then os.Exit(1); neither runs deferred functions).
				slog.ErrorContext(ctx, "error fetching observations", "station", station, "start_unix", cur.Unix(), "end_unix", next.Unix(), "err", err)
				os.Exit(1)
			}
			metrics = append(metrics, stationMetrics...)
		}
	}

	return metrics, cur
}

func exportWithSink(ctx context.Context, token string, metricsSink *sink.MetricsSink) {
	client := tempestapi.NewClient(token)
	stations, startAt := fetchStationsAndStartTime(ctx, client)

	keepFiles, err := config.ParseBoolEnv("KEEP_EXPORT_FILES")
	if err != nil {
		log.Fatal(err)
	}
	fileNum := 1

	cur := startAt
	for {
		var metrics []promclient.Metric
		metrics, cur = fetchDailyBatch(ctx, client, stations, cur)

		if len(metrics) == 0 {
			break
		}

		// Send to sink (Postgres), wrapped in an export.batch span so
		// each batch send shows up in the trace backend the same way
		// UDP ingest's sink.write does.
		slog.InfoContext(ctx, "sending metrics to sink", "count", len(metrics))
		if err := otel.TraceExportBatch(ctx, func(ctx context.Context) error {
			return metricsSink.SendMetrics(ctx, metrics)
		}); err != nil {
			log.Printf("error sending metrics: %v", err)
		}

		// Optionally write to .gz files
		if keepFiles {
			filename := fmt.Sprintf("stormglass_%03d.txt.gz", fileNum)
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
