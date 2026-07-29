package postgres

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
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

	for {
		select {
		case row := <-w.obsBatch:
			// Immediate flush for observations
			w.flushObservations(w.ctx, []observationRow{row})

		case <-w.done:
			return
		}
	}
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
	w.flushWithRetry(func() error {
		return w.obsInserter.insertObservations(ctx, batch)
	}, "tempest_observations", len(batch))
}

func (w *PostgresWriter) insertObservations(ctx context.Context, batch []observationRow) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	b := &pgx.Batch{}

	for _, row := range batch {
		b.Queue(`
			INSERT INTO tempest_observations (
				id, serial_number, timestamp,
				wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
				pressure, temp_air, temp_wetbulb, humidity,
				illuminance, uv_index, irradiance, rain_rate, precip_type,
				lightning_distance, lightning_strike_count,
				battery, report_interval
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
			ON CONFLICT (serial_number, timestamp) DO NOTHING
		`, row.id, row.serialNumber, row.timestamp,
			row.windLull, row.windAvg, row.windGust, row.windDirection, row.windSampleInterval,
			row.pressure, row.tempAir, row.tempWetbulb, row.humidity,
			row.illuminance, row.uvIndex, row.irradiance, row.rainRate, row.precipType,
			row.lightningDistance, row.lightningStrikeCount,
			row.battery, row.reportInterval)
	}

	br := w.pool.SendBatch(ctx, b)
	defer closeBatchResults(br)

	for i := 0; i < len(batch); i++ {
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
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case row := <-w.windBatch:
			batch = append(batch, row)

			// Flush when batch is full
			if len(batch) >= w.batchSize {
				w.flushRapidWind(w.ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				w.flushRapidWind(w.ctx, batch)
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
	w.flushWithRetry(func() error {
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

	for i := 0; i < len(batch); i++ {
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
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case row := <-w.hubBatch:
			batch = append(batch, row)
			if len(batch) >= w.batchSize {
				w.flushHubStatus(w.ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				w.flushHubStatus(w.ctx, batch)
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
	w.flushWithRetry(func() error {
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

	for i := 0; i < len(batch); i++ {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("insert hub_status %d: %w", i, err)
		}
	}

	return nil
}

func (w *PostgresWriter) batchEvents() {
	defer w.wg.Done()

	for {
		select {
		case row := <-w.eventBatch:
			// Immediate flush for events (critical)
			w.flushEvents(w.ctx, []eventRow{row})

		case <-w.done:
			return
		}
	}
}

func (w *PostgresWriter) flushEvents(ctx context.Context, batch []eventRow) {
	w.flushWithRetry(func() error {
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

	for i := 0; i < len(batch); i++ {
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

// WriteMetrics implements MetricsWriter interface
// This is used by the API export mode to write historical data
func (w *PostgresWriter) WriteMetrics(ctx context.Context, metrics []prometheus.Metric) error {
	// Group metrics by timestamp and serial number to reconstruct observations
	type metricKey struct {
		serialNumber string
		timestamp    time.Time
	}

	observations := make(map[metricKey]*observationRow)

	for _, metric := range metrics {
		var dto io_prometheus_client.Metric
		if err := metric.Write(&dto); err != nil {
			log.Printf("postgres: failed to write metric: %v", err)
			continue
		}

		// Extract serial number from instance label
		var serialNumber string
		for _, label := range dto.GetLabel() {
			if label.GetName() == "instance" {
				serialNumber = label.GetValue()
				break
			}
		}
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
		desc := metric.Desc().String()
		switch {
		case strings.Contains(desc, "tempest_wind_ms"):
			// Check kind label
			for _, label := range dto.GetLabel() {
				if label.GetName() == "kind" {
					switch label.GetValue() {
					case "lull":
						obs.windLull = value
					case "avg":
						obs.windAvg = value
					case "gust":
						obs.windGust = value
					}
					break
				}
			}
		case strings.Contains(desc, "tempest_wind_direction_degrees"):
			obs.windDirection = value
		case strings.Contains(desc, "tempest_pressure_pa"):
			obs.pressure = value
		case strings.Contains(desc, "tempest_temperature_c"):
			// Check kind label
			for _, label := range dto.GetLabel() {
				if label.GetName() == "kind" {
					switch label.GetValue() {
					case "air":
						obs.tempAir = value
					case "wetbulb":
						obs.tempWetbulb = &value
					}
					break
				}
			}
		case strings.Contains(desc, "tempest_humidity_percent"):
			obs.humidity = value
		case strings.Contains(desc, "tempest_illuminance_lux"):
			obs.illuminance = value
		case strings.Contains(desc, "tempest_uv_index"):
			obs.uvIndex = value
		case strings.Contains(desc, "tempest_irradiance_w_m2"):
			obs.irradiance = value
		case strings.Contains(desc, "tempest_rain_rate_mm_min"):
			obs.rainRate = value
		case strings.Contains(desc, "tempest_lightning_distance_km"):
			obs.lightningDistance = &value
		case strings.Contains(desc, "tempest_lightning_strike_count"):
			obs.lightningStrikeCount = &value
		case strings.Contains(desc, "tempest_battery_volts"):
			obs.battery = &value
		case strings.Contains(desc, "tempest_report_interval_s"):
			obs.reportInterval = &value
		}
	}

	// Send all observations to the batch channel
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

// Flush implements MetricsWriter interface
func (w *PostgresWriter) Flush(ctx context.Context) error {
	// Flush is handled by the batch workers.
	// When Close() is called, done is closed and workers flush remaining data.
	return nil
}

// flushWithRetry implements exponential backoff retry logic
func (w *PostgresWriter) flushWithRetry(flushFn func() error, tableName string, batchSize int) {
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
		case <-w.ctx.Done():
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
		switch pgErr.Code {
		// Retryable: connection failures (Class 08)
		case "08000", "08003", "08006":
			return true
		// Retryable: deadlock (Class 40)
		case "40001", "40P01":
			return true
		// Not retryable: constraint violations (Class 23)
		case "23505", "23503", "23502":
			return false
		// Not retryable: undefined objects (Class 42)
		case "42P01", "42703":
			return false
		default:
			return true
		}
	}

	// Network errors
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") {
		return true
	}

	return true
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
