package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"tempestwx-utilities/internal/tempestudp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

// Row types for each table
type observationRow struct {
	id                   uuid.UUID
	serialNumber         string
	timestamp            time.Time
	windLull             float64
	windAvg              float64
	windGust             float64
	windDirection        float64
	windSampleInterval   *float64
	pressure             float64
	tempAir              float64
	tempWetbulb          *float64 // nil (SQL NULL) when WetBulbTemperatureC is non-convergent (NaN)
	humidity             float64
	illuminance          float64
	uvIndex              float64
	irradiance           float64
	rainRate             float64
	precipType           *int
	lightningDistance    *float64
	lightningStrikeCount *float64
	battery              *float64
	reportInterval       *float64
}

type rapidWindRow struct {
	id            uuid.UUID
	serialNumber  string
	timestamp     time.Time
	windSpeed     float64
	windDirection float64
}

type hubStatusRow struct {
	id           uuid.UUID
	serialNumber string
	timestamp    time.Time
	uptime       float64
	rssi         float64
	rebootCount  float64
	busErrors    float64
}

type eventRow struct {
	id           uuid.UUID
	serialNumber string
	timestamp    time.Time
	eventType    string
	distanceKm   *float64
	energy       *float64
}

// obsInserter abstracts the observation batch-insert path so the
// Close(ctx)-time drain (TestPostgresWriter_DrainOnClose) can be exercised
// with a fake in place of a live database connection. Scoped to observations
// only: it is the row type the drain test needs to assert on, and no second
// present consumer exists yet for the other three tables.
type obsInserter interface {
	insertObservations(ctx context.Context, batch []observationRow) error
}

// windInserter abstracts the rapid-wind batch-insert path, mirroring
// obsInserter exactly. Needed so
// TestPostgresWriter_ShutdownFlushesLocalBatchUnderCanceledWctx can assert on
// batchRapidWind's local-accumulator shutdown flush without a live database
// connection: unlike observations/events, rapidWind (and hubStatus) hold rows
// in a local slice across select iterations, which is the exact codepath the
// C-H1 shutdown-drain bug lives in.
type windInserter interface {
	insertRapidWind(ctx context.Context, batch []rapidWindRow) error
}

// PostgresWriter writes metrics to PostgreSQL with batching and retry logic.
// The name stutters (postgres.PostgresWriter) but is an established,
// widely-referenced identifier (main.go, backfill_cmd.go,
// internal/sqlite/writer.go); renaming is a cross-file rename out of scope
// for this lint-debt pass, not a doc-comment fix.
//
//nolint:revive // established name; see doc comment above
type PostgresWriter struct {
	pool *pgxpool.Pool

	// Batch channels per table
	obsBatch   chan observationRow
	windBatch  chan rapidWindRow
	hubBatch   chan hubStatusRow
	eventBatch chan eventRow

	// Configuration
	batchSize     int
	flushInterval time.Duration
	maxRetries    int

	// obsInserter defaults to the writer itself (see NewPostgresWriter);
	// tests may substitute a fake.
	obsInserter obsInserter

	// windInserter defaults to the writer itself (see NewPostgresWriter);
	// tests may substitute a fake.
	windInserter windInserter

	ctx context.Context
	wg  sync.WaitGroup

	// done is the sole shutdown signal: closing it tells every producer
	// send and every batch worker that Close is in progress. The batch
	// channels themselves are never closed (see Close), which is what
	// keeps a concurrent producer send from ever panicking on a
	// send-on-closed-channel (D-H1).
	done      chan struct{}
	closeOnce sync.Once

	// shutdownCtx is the live ctx a batch worker must use to flush its local
	// in-flight batch on shutdown — never w.ctx, which SIGTERM cancels
	// before Close ever runs in production (that was the C-H1 residual bug:
	// flushing with an already-canceled ctx made the flush fail as
	// non-retryable, silently dropping the batch). Close sets shutdownCtx
	// *before* close(w.done); a worker only ever reads shutdownCtx after its
	// own `<-w.done` case fires, and the Go memory model guarantees a send
	// happens-before the corresponding receive on a closed channel — so this
	// plain field write is safely visible to every worker goroutine without
	// a mutex or atomic.
	shutdownCtx context.Context
}

// NewPostgresWriter creates a new PostgreSQL writer with connection pooling.
func NewPostgresWriter(ctx context.Context, databaseURL string) (*PostgresWriter, error) {
	pool, err := OpenPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}

	// Auto-create schema
	if err := CreateSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	log.Printf("postgres: connected, schema ready")

	tn := postgresTunables(os.Getenv)

	// Initialize writer
	w := &PostgresWriter{
		pool:          pool,
		obsBatch:      make(chan observationRow, 1000),
		windBatch:     make(chan rapidWindRow, 1000),
		hubBatch:      make(chan hubStatusRow, 1000),
		eventBatch:    make(chan eventRow, 1000),
		batchSize:     tn.batchSize,
		flushInterval: tn.flushInterval,
		maxRetries:    tn.maxRetries,
		ctx:           ctx,
		done:          make(chan struct{}),
	}
	w.obsInserter = w
	w.windInserter = w

	// Start background batch workers
	w.wg.Add(4)
	go w.batchObservations()
	go w.batchRapidWind()
	go w.batchHubStatus()
	go w.batchEvents()

	return w, nil
}

// batchObservations handles observation rows with immediate flush (1 row batches)
func (w *PostgresWriter) batchObservations() {
	defer w.wg.Done()

	flushCtx := steadyStateFlushCtx(w.ctx)

	for {
		select {
		case row := <-w.obsBatch:
			// Immediate flush for observations
			w.flushObservations(flushCtx, []observationRow{row})

		case <-w.done:
			return
		}
	}
}

// steadyStateFlushCtx returns the context a batch worker must use for its
// ordinary (non-shutdown) flushes. It deliberately drops cancellation from
// w.ctx: SIGTERM cancels w.ctx *before* Close is called, and a row a worker
// has already taken off its channel is no longer visible to Close's
// drainChannel — so a flush that fails on the dead ctx loses that row
// outright, with no second chance to recover it (#111). That is the same
// failure the shutdownCtx field comment describes for the local in-flight
// batch; the C-H1 fix only ever covered the <-w.done branch, leaving every
// steady-state flush still bound to w.ctx.
//
// Detaching cancellation does not make a flush unbounded: every insert*
// method derives its own context.WithTimeout, and flushWithRetry caps
// attempts at w.maxRetries.
func steadyStateFlushCtx(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// closeBatchResults closes a pgx batch result set, logging any error.
// A close error here is not actionable by the caller (per-statement errors
// are already surfaced by br.Exec()), so it is logged rather than returned.
func closeBatchResults(br pgx.BatchResults) {
	if err := br.Close(); err != nil {
		log.Printf("postgres: batch close error: %v", err)
	}
}

func (w *PostgresWriter) flushObservations(ctx context.Context, batch []observationRow) {
	w.flushWithRetry(ctx, func() error {
		return w.obsInserter.insertObservations(ctx, batch)
	}, "tempest_observations", len(batch))
}

// insertObservationSQL is the single source of truth for the
// tempest_observations INSERT shape — the live daemon write path
// (insertObservations below) and the backfill path
// (InsertObservations in backfill.go) both bind against this constant.
// Add a column here and both write paths pick it up together; a second,
// independently-maintained copy is exactly how backfilled and live rows
// would silently diverge.
const insertObservationSQL = `
	INSERT INTO tempest_observations (
		id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

func (w *PostgresWriter) insertObservations(ctx context.Context, batch []observationRow) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b := &pgx.Batch{}

	for _, row := range batch {
		b.Queue(insertObservationSQL, row.id, row.serialNumber, row.timestamp,
			row.windLull, row.windAvg, row.windGust, row.windDirection, row.windSampleInterval,
			row.pressure, row.tempAir, row.tempWetbulb, row.humidity,
			row.illuminance, row.uvIndex, row.irradiance, row.rainRate, row.precipType,
			row.lightningDistance, row.lightningStrikeCount,
			row.battery, row.reportInterval)
	}

	br := w.pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	for i := range batch {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert observation %d: %w", i, err)
		}
	}

	return nil
}

func (w *PostgresWriter) batchRapidWind() {
	defer w.wg.Done()

	batch := make([]rapidWindRow, 0, w.batchSize)
	flushCtx := steadyStateFlushCtx(w.ctx)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case row := <-w.windBatch:
			batch = append(batch, row)

			// Flush when batch is full
			if len(batch) >= w.batchSize {
				w.flushRapidWind(flushCtx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				w.flushRapidWind(flushCtx, batch)
				batch = batch[:0]
			}

		case <-w.done:
			// Shutdown - flush the local in-flight batch using the live
			// shutdownCtx (not w.ctx, which SIGTERM may have already
			// canceled — see the shutdownCtx field comment and C-H1).
			// Whatever is still sitting in the channel is drained by Close
			// after wg.Wait().
			if len(batch) > 0 {
				w.flushRapidWind(w.shutdownCtx, batch)
			}
			return
		}
	}
}

func (w *PostgresWriter) flushRapidWind(ctx context.Context, batch []rapidWindRow) {
	w.flushWithRetry(ctx, func() error {
		return w.windInserter.insertRapidWind(ctx, batch)
	}, "tempest_rapid_wind", len(batch))
}

func (w *PostgresWriter) insertRapidWind(ctx context.Context, batch []rapidWindRow) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b := &pgx.Batch{}

	for _, row := range batch {
		b.Queue(`
			INSERT INTO tempest_rapid_wind (
				id, serial_number, timestamp, wind_speed, wind_direction
			) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (serial_number, timestamp) DO NOTHING
		`, row.id, row.serialNumber, row.timestamp, row.windSpeed, row.windDirection)
	}

	br := w.pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	for i := range batch {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert rapid_wind %d: %w", i, err)
		}
	}

	return nil
}

func (w *PostgresWriter) batchHubStatus() {
	defer w.wg.Done()

	batch := make([]hubStatusRow, 0, w.batchSize)
	flushCtx := steadyStateFlushCtx(w.ctx)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case row := <-w.hubBatch:
			batch = append(batch, row)
			if len(batch) >= w.batchSize {
				w.flushHubStatus(flushCtx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				w.flushHubStatus(flushCtx, batch)
				batch = batch[:0]
			}

		case <-w.done:
			// Shutdown - flush the local in-flight batch using the live
			// shutdownCtx (not w.ctx, which SIGTERM may have already
			// canceled — see the shutdownCtx field comment and C-H1).
			if len(batch) > 0 {
				w.flushHubStatus(w.shutdownCtx, batch)
			}
			return
		}
	}
}

func (w *PostgresWriter) flushHubStatus(ctx context.Context, batch []hubStatusRow) {
	w.flushWithRetry(ctx, func() error {
		return w.insertHubStatus(ctx, batch)
	}, "tempest_hub_status", len(batch))
}

func (w *PostgresWriter) insertHubStatus(ctx context.Context, batch []hubStatusRow) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b := &pgx.Batch{}

	for _, row := range batch {
		b.Queue(`
			INSERT INTO tempest_hub_status (
				id, serial_number, timestamp, uptime, rssi, reboot_count, bus_errors
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (serial_number, timestamp) DO NOTHING
		`, row.id, row.serialNumber, row.timestamp, row.uptime, row.rssi, row.rebootCount, row.busErrors)
	}

	br := w.pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	for i := range batch {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert hub_status %d: %w", i, err)
		}
	}

	return nil
}

func (w *PostgresWriter) batchEvents() {
	defer w.wg.Done()

	flushCtx := steadyStateFlushCtx(w.ctx)

	for {
		select {
		case row := <-w.eventBatch:
			// Immediate flush for events (critical)
			w.flushEvents(flushCtx, []eventRow{row})

		case <-w.done:
			return
		}
	}
}

func (w *PostgresWriter) flushEvents(ctx context.Context, batch []eventRow) {
	w.flushWithRetry(ctx, func() error {
		return w.insertEvents(ctx, batch)
	}, "tempest_events", len(batch))
}

func (w *PostgresWriter) insertEvents(ctx context.Context, batch []eventRow) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b := &pgx.Batch{}

	for _, row := range batch {
		b.Queue(`
			INSERT INTO tempest_events (
				id, serial_number, timestamp, event_type, distance_km, energy
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (serial_number, timestamp, event_type) DO NOTHING
		`, row.id, row.serialNumber, row.timestamp, row.eventType, row.distanceKm, row.energy)
	}

	br := w.pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	for i := range batch {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert event %d: %w", i, err)
		}
	}

	return nil
}

// WriteReport implements MetricsWriter interface
func (w *PostgresWriter) WriteReport(ctx context.Context, report tempestudp.Report) error {
	switch r := report.(type) {
	case *tempestudp.TempestObservationReport:
		return w.handleObservationReport(ctx, r)

	case *tempestudp.RapidWindReport:
		return w.handleRapidWindReport(ctx, r)

	case *tempestudp.HubStatusReport:
		return w.handleHubStatusReport(ctx, r)

	case *tempestudp.RainStartReport:
		return w.handleRainStartReport(ctx, r)

	case *tempestudp.LightningStrikeReport:
		return w.handleLightningStrikeReport(ctx, r)

	default:
		// Unknown report type (e.g., device_status) - not an error
		return nil
	}
}

func (w *PostgresWriter) handleObservationReport(ctx context.Context, r *tempestudp.TempestObservationReport) error {
	for _, ob := range r.Obs {
		if len(ob) < 13 {
			continue
		}

		ts := time.Unix(int64(ob[0]), 0)

		// Calculate wet bulb temperature (from tempestudp package)
		wetBulb := tempestudp.WetBulbTemperatureC(ob[7], ob[8], ob[6])

		row := observationRow{
			id:            uuid.Must(uuid.NewV7()), // Generate UUIDv7
			serialNumber:  r.SerialNumber,
			timestamp:     ts,
			windLull:      ob[1],
			windAvg:       ob[2],
			windGust:      ob[3],
			windDirection: ob[4],
			pressure:      ob[6], // Raw mb value (no conversion)
			tempAir:       ob[7],
			humidity:      ob[8],
			illuminance:   ob[9],
			uvIndex:       ob[10],
			irradiance:    ob[11],
			rainRate:      ob[12],
		}

		// WetBulbTemperatureC returns NaN for non-convergent inputs (e.g.
		// physically impossible humidity/pressure from a malformed report);
		// store SQL NULL rather than IEEE NaN (mirrors the same guard in
		// tempestudp/report.go's Prometheus metrics path).
		if !math.IsNaN(wetBulb) {
			row.tempWetbulb = &wetBulb
		}

		// Field 5: wind_sample_interval (seconds)
		if len(ob) >= 6 {
			interval := ob[5]
			row.windSampleInterval = &interval
		}

		// Field 13: precip_type (0=none, 1=rain, 2=hail, 3=rain+hail)
		if len(ob) >= 14 {
			precipType := int(ob[13])
			row.precipType = &precipType
		}

		// Lightning fields (14 and 15)
		if len(ob) >= 16 {
			distance := ob[14]
			count := ob[15]
			row.lightningDistance = &distance
			row.lightningStrikeCount = &count
		}

		// Field 16: battery
		if len(ob) >= 17 {
			battery := ob[16]
			row.battery = &battery
		}

		// Field 17: report_interval (minutes - raw value)
		if len(ob) >= 18 {
			interval := ob[17] // Raw minutes value (no conversion)
			row.reportInterval = &interval
		}

		select {
		case w.obsBatch <- row:
		case <-w.done: // Close in progress — stop producing, no send-on-closed
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (w *PostgresWriter) handleRapidWindReport(ctx context.Context, r *tempestudp.RapidWindReport) error {
	if len(r.Ob) != 3 {
		return nil // Invalid data
	}

	ts := time.Unix(int64(r.Ob[0]), 0)

	row := rapidWindRow{
		id:            uuid.Must(uuid.NewV7()), // Generate UUIDv7
		serialNumber:  r.SerialNumber,
		timestamp:     ts,
		windSpeed:     r.Ob[1],
		windDirection: r.Ob[2],
	}

	select {
	case w.windBatch <- row:
	case <-w.done: // Close in progress — stop producing, no send-on-closed
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (w *PostgresWriter) handleHubStatusReport(ctx context.Context, r *tempestudp.HubStatusReport) error {
	if len(r.RadioStats) < 3 {
		return nil // Invalid data
	}

	ts := time.Unix(r.Timestamp, 0)

	row := hubStatusRow{
		id:           uuid.Must(uuid.NewV7()), // Generate UUIDv7
		serialNumber: r.SerialNumber,
		timestamp:    ts,
		uptime:       r.Uptime,
		rssi:         r.Rssi,
		rebootCount:  r.RadioStats[1],
		busErrors:    r.RadioStats[2],
	}

	select {
	case w.hubBatch <- row:
	case <-w.done: // Close in progress — stop producing, no send-on-closed
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (w *PostgresWriter) handleRainStartReport(ctx context.Context, r *tempestudp.RainStartReport) error {
	if len(r.Evt) < 1 {
		return nil // Invalid data
	}

	ts := time.Unix(int64(r.Evt[0]), 0)

	row := eventRow{
		id:           uuid.Must(uuid.NewV7()), // Generate UUIDv7
		serialNumber: r.SerialNumber,
		timestamp:    ts,
		eventType:    "rain_start",
		distanceKm:   nil, // Not applicable for rain
		energy:       nil, // Not applicable for rain
	}

	select {
	case w.eventBatch <- row:
	case <-w.done: // Close in progress — stop producing, no send-on-closed
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (w *PostgresWriter) handleLightningStrikeReport(ctx context.Context, r *tempestudp.LightningStrikeReport) error {
	if len(r.Evt) < 3 {
		return nil // Invalid data
	}

	ts := time.Unix(int64(r.Evt[0]), 0)
	distance := r.Evt[1]
	energy := r.Evt[2]

	row := eventRow{
		id:           uuid.Must(uuid.NewV7()), // Generate UUIDv7
		serialNumber: r.SerialNumber,
		timestamp:    ts,
		eventType:    "lightning_strike",
		distanceKm:   &distance,
		energy:       &energy,
	}

	select {
	case w.eventBatch <- row:
	case <-w.done: // Close in progress — stop producing, no send-on-closed
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// metricKey groups a WriteMetrics input by the (serial, timestamp) pair that
// identifies which reconstructed observation row a metric belongs to.
type metricKey struct {
	serialNumber string
	timestamp    time.Time
}

// observationFieldMapper matches a metric descriptor substring to the
// observationRow field(s) it populates. substr and apply mirror
// WriteMetrics' original desc-matching switch exactly, one entry per case,
// in the same order (Contains matching, first match wins) -- every entry
// here must map the same descriptor substring to the same column(s) the
// original switch did.
type observationFieldMapper struct {
	substr string
	apply  func(obs *observationRow, value float64, kind string)
}

var observationFieldMappers = []observationFieldMapper{
	{"tempest_wind_ms", func(obs *observationRow, value float64, kind string) {
		switch kind {
		case "lull":
			obs.windLull = value
		case "avg":
			obs.windAvg = value
		case "gust":
			obs.windGust = value
		}
	}},
	{"tempest_wind_direction_degrees", func(obs *observationRow, value float64, _ string) {
		obs.windDirection = value
	}},
	{"tempest_pressure_pa", func(obs *observationRow, value float64, _ string) {
		obs.pressure = value
	}},
	{"tempest_temperature_c", func(obs *observationRow, value float64, kind string) {
		switch kind {
		case "air":
			obs.tempAir = value
		case "wetbulb":
			obs.tempWetbulb = &value
		}
	}},
	{"tempest_humidity_percent", func(obs *observationRow, value float64, _ string) {
		obs.humidity = value
	}},
	{"tempest_illuminance_lux", func(obs *observationRow, value float64, _ string) {
		obs.illuminance = value
	}},
	{"tempest_uv_index", func(obs *observationRow, value float64, _ string) {
		obs.uvIndex = value
	}},
	{"tempest_irradiance_w_m2", func(obs *observationRow, value float64, _ string) {
		obs.irradiance = value
	}},
	{"tempest_rain_rate_mm_min", func(obs *observationRow, value float64, _ string) {
		obs.rainRate = value
	}},
	{"tempest_lightning_distance_km", func(obs *observationRow, value float64, _ string) {
		obs.lightningDistance = &value
	}},
	{"tempest_lightning_strike_count", func(obs *observationRow, value float64, _ string) {
		obs.lightningStrikeCount = &value
	}},
	{"tempest_battery_volts", func(obs *observationRow, value float64, _ string) {
		obs.battery = &value
	}},
	{"tempest_report_interval_s", func(obs *observationRow, value float64, _ string) {
		obs.reportInterval = &value
	}},
}

// applyObservationField maps desc to the observationRow field(s) it
// populates via observationFieldMappers, matching the first substring found
// in desc (mirrors the original switch's top-to-bottom, first-match-wins
// evaluation). No match is a silent no-op, matching the original switch
// falling through with no case taken.
func applyObservationField(obs *observationRow, desc, kind string, value float64) {
	for _, fm := range observationFieldMappers {
		if strings.Contains(desc, fm.substr) {
			fm.apply(obs, value, kind)
			return
		}
	}
}

// labelValue returns the value of the named label pair, or "" if absent.
func labelValue(labels []*io_prometheus_client.LabelPair, name string) string {
	for _, label := range labels {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

// sendObservationBatch sends every accumulated observation row to the batch
// channel, respecting the same shutdown/cancellation short-circuits as every
// other producer send in this file.
func (w *PostgresWriter) sendObservationBatch(ctx context.Context, observations map[metricKey]*observationRow) error {
	for _, obs := range observations {
		select {
		case w.obsBatch <- *obs:
		case <-w.done: // Close in progress — stop producing, no send-on-closed
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// WriteMetrics implements MetricsWriter interface
// This is used by the API export mode to write historical data
func (w *PostgresWriter) WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	// Group metrics by timestamp and serial number to reconstruct observations
	observations := make(map[metricKey]*observationRow)

	for _, metric := range metrics {
		var dto io_prometheus_client.Metric
		if err := metric.Write(&dto); err != nil {
			log.Printf("postgres: failed to write metric: %v", err)
			continue
		}

		// Extract serial number from instance label
		serialNumber := labelValue(dto.GetLabel(), "instance")
		if serialNumber == "" {
			continue
		}

		// Extract timestamp
		ts := time.UnixMilli(dto.GetTimestampMs())
		key := metricKey{serialNumber: serialNumber, timestamp: ts}

		// Get or create observation row
		obs, exists := observations[key]
		if !exists {
			obs = &observationRow{
				id:           uuid.Must(uuid.NewV7()),
				serialNumber: serialNumber,
				timestamp:    ts,
			}
			observations[key] = obs
		}

		// Extract value
		var value float64
		if dto.GetGauge() != nil {
			value = dto.GetGauge().GetValue()
		} else if dto.GetCounter() != nil {
			value = dto.GetCounter().GetValue()
		}

		// Map metric to field based on descriptor
		applyObservationField(obs, metric.Desc().String(), labelValue(dto.GetLabel(), "kind"), value)
	}

	// Send all observations to the batch channel
	return w.sendObservationBatch(ctx, observations)
}

// Flush implements MetricsWriter interface
func (w *PostgresWriter) Flush(ctx context.Context) error {
	// Flush is handled by the batch workers.
	// When Close() is called, done is closed and workers flush remaining data.
	return nil
}

// flushWithRetry implements exponential backoff retry logic.
//
// ctx must be the same context the caller hands to flushFn's insert, and is
// deliberately NOT w.ctx: SIGTERM cancels w.ctx before Close runs, so gating
// the backoff sleep on it abandoned the first retryable failure that arrived
// during shutdown and dropped the batch (#153) — the moment durability
// matters most. Threading the caller's ctx makes the bound follow the flush:
// steady-state flushes carry the cancellation-detached ctx and back off
// normally, while a shutdown flush carries Close's ctx and so is bounded by
// the cleanup deadline instead of aborting instantly.
func (w *PostgresWriter) flushWithRetry(ctx context.Context, flushFn func() error, tableName string, batchSize int) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for attempt := 1; attempt <= w.maxRetries; attempt++ {
		err := flushFn()
		if err == nil {
			return
		}

		log.Printf("postgres: failed to write %d rows to %s (attempt %d/%d): %v",
			batchSize, tableName, attempt, w.maxRetries, err)

		if !isRetryable(err) {
			log.Printf("postgres: non-retryable error for %s, dropping batch: %v",
				tableName, err)
			return
		}

		if attempt == w.maxRetries {
			log.Printf("postgres: max retries exceeded for %s, dropping %d rows", tableName, batchSize)
			return
		}

		// Check if context is still valid before sleeping
		select {
		case <-ctx.Done():
			log.Printf("postgres: context cancelled during retry for %s, dropping batch", tableName)
			return
		case <-time.After(backoff):
			// Continue to next attempt
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for context errors (not retryable - parent cancelled)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for pgconn errors
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// isRetryable's default (below) is fail-closed: any SQLSTATE not
		// explicitly listed here as retryable gets zero retries and an
		// immediate batch drop, not the 1s/2s/4s backoff. So omitting a
		// genuinely-transient code from this switch is not "falls back to
		// retry" — it is "drop on first failure". Keep this list complete
		// for the transient classes actually seen in practice (connection
		// setup, deadlock, server restart/shutdown), and default-deny
		// everything else deliberately, not by oversight.
		switch pgErr.Code {
		// Retryable: connection problems (Class 08). 08007
		// (transaction_resolution_unknown) is included even though its
		// outcome is genuinely ambiguous — the in-flight statement may or
		// may not have committed — because every insert in this package is
		// `INSERT ... ON CONFLICT (serial_number, timestamp[, event_type])
		// DO NOTHING`, matching the tables' UNIQUE constraints (schema.go),
		// so a retried insert that already landed is a harmless no-op, not
		// a double-apply.
		case "08000", "08001", "08003", "08004", "08006", "08007":
			return true
		// Retryable: deadlock (Class 40)
		case "40001", "40P01":
			return true
		// Retryable: transient resource/availability conditions that clear
		// as load drops, connections close, or the server finishes
		// restarting (53300 too_many_connections, 57P01 admin_shutdown,
		// 57P02 crash_shutdown, 57P03 cannot_connect_now — the latter two
		// cover a routine Postgres restart/upgrade, which this project's
		// docker-compose/k8s deployment triggers regularly). 53400
		// (configuration_limit_exceeded) is deliberately excluded: it
		// signals a fixed server configuration limit (e.g. temp_file_limit,
		// per-role limits), not transient load — the identical batch hits
		// it identically on retry, so retrying only burns the retry budget.
		// 57014 (query_canceled) is excluded for the same reason: it most
		// often means a server-side statement_timeout or lock_timeout fired
		// for this exact statement, which will likely recur identically on
		// an unmodified retry rather than clear on its own.
		case "53300", "57P01", "57P02", "57P03":
			return true
		// Not retryable: constraint violations (Class 23)
		case "23505", "23503", "23502":
			return false
		// Not retryable: undefined objects (Class 42)
		case "42P01", "42703":
			return false
		default:
			// Unrecognized SQLSTATE: fail closed rather than burning the
			// retry budget on an error we can't classify.
			return false
		}
	}

	// Network errors: timeouts and connection-establishment failures are
	// transient; anything else is unknown and should fail closed.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// Close implements MetricsWriter interface. Idempotent — safe to call more
// than once (C-H3) — and never closes a batch channel, so a concurrent
// producer send can never panic on a send-on-closed-channel (D-H1). done is
// the sole shutdown signal (see the PostgresWriter.done field comment).
func (w *PostgresWriter) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		// Set shutdownCtx before closing done: this write happens-before
		// close(w.done), which happens-before any worker's `<-w.done` case
		// firing (Go memory model), so every worker is guaranteed to see
		// this live ctx once it starts its shutdown flush — see the
		// shutdownCtx field comment.
		w.shutdownCtx = ctx

		// Signal producers and workers that Close is in progress. There is
		// no longer a competing <-w.ctx.Done() case in the batch workers
		// (removed as the C-H1 fix): w.ctx canceling on SIGTERM no longer
		// races a worker into flushing its local batch with a dead ctx.
		// <-w.done is the workers' sole shutdown signal, and it always
		// carries a live ctx via shutdownCtx.
		close(w.done)

		// Wait for workers to finish; any worker that exited early left its
		// remaining buffered rows unread.
		w.wg.Wait()

		// Drain and flush whatever is still buffered, using the passed-in
		// ctx (not the writer's own, already-canceled ctx) so the insert
		// has a live deadline to work with (C-H1). The batch channels are
		// never closed, so this drain must be non-blocking (see
		// drainChannel) rather than a `range` over the channel.
		if batch := drainChannel(w.obsBatch); len(batch) > 0 {
			w.flushObservations(ctx, batch)
		}
		if batch := drainChannel(w.windBatch); len(batch) > 0 {
			w.flushRapidWind(ctx, batch)
		}
		if batch := drainChannel(w.hubBatch); len(batch) > 0 {
			w.flushHubStatus(ctx, batch)
		}
		if batch := drainChannel(w.eventBatch); len(batch) > 0 {
			w.flushEvents(ctx, batch)
		}

		// Close connection pool. Guarded against nil so tests can exercise
		// Close on a writer constructed without a live pool (see
		// writer_test.go).
		if w.pool != nil {
			w.pool.Close()
		}
		log.Printf("postgres: closed")
	})

	return nil
}

// drainChannel receives every value currently buffered in ch without
// blocking. The batch channels are never closed (Close signals shutdown via
// done, not by closing them — see D-H1), so a `range` over ch would block
// forever once it's empty; the non-blocking select below stops as soon as
// nothing more is immediately available. Callers must guarantee no other
// goroutine is still receiving from ch (in Close, w.wg.Wait() has already
// ensured every batch worker has returned) so that a single non-blocking
// pass is sufficient to catch everything buffered.
func drainChannel[T any](ch <-chan T) []T {
	var batch []T
	for {
		select {
		case v := <-ch:
			batch = append(batch, v)
		default:
			return batch
		}
	}
}
