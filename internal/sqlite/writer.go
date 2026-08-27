package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// rowChanCap bounds the single writer's inbound queue. 1000 mirrors the
	// postgres writer's per-channel buffer — at ~1 obs/min plus 3s rapid-wind,
	// 1000 is minutes of headroom before backpressure engages.
	rowChanCap = 1000
	// eventBlockTimeout is how long a DISCRETE event (lightning/rain-start)
	// will block when the channel is full before it is logged-and-dropped.
	// Chosen shorter than the 30s shutdown budget yet long enough for the
	// single writer to complete at least one batch flush; a discrete event is
	// rare, so blocking briefly is correct, not silent loss.
	eventBlockTimeout = 5 * time.Second

	// maxHistoryPoints caps the rows HistoryPoints returns for an unbounded
	// or wide [from, to] range. Without a cap, a request spanning the whole
	// table would scan and marshal every row through the single writer
	// connection (SetMaxOpenConns(1)), serializing behind and starving the
	// writer goroutine. 10000 is generous headroom over any chart the UI
	// currently renders (the /history endpoint has no UI consumer yet, so
	// truncation risk today is low) while still bounding worst-case response
	// size and query cost (SGE review I1).
	maxHistoryPoints = 10000
)

// rowKind discriminates the payload carried by a rowEnvelope.
type rowKind int

const (
	kindObservation rowKind = iota
	kindRapidWind
	kindHubStatus
	kindEvent
	// kindFlush carries a flushRequest rather than a table row (see Flush).
	// It is sent through the same w.rows channel as every other envelope so
	// it is FIFO-ordered relative to rows enqueued before it — sending a
	// flush signal on a SEPARATE channel would race with w.rows: Go's select
	// gives no ordering guarantee across two different channels, so the
	// writer goroutine could process a flush request before a row sent
	// microseconds earlier, silently flushing an empty batch while the real
	// row was still unprocessed.
	kindFlush
)

// String implements fmt.Stringer for use in log fields.
func (k rowKind) String() string {
	switch k {
	case kindObservation:
		return "observation"
	case kindRapidWind:
		return "rapid_wind"
	case kindHubStatus:
		return "hub_status"
	case kindEvent:
		return "event"
	case kindFlush:
		return "flush"
	default:
		return "unknown"
	}
}

// rowEnvelope carries one row destined for one of the four tables through
// the single writer's inbound channel. kind discriminates the concrete type
// held in payload (observationRow, rapidWindRow, hubStatusRow, or eventRow).
type rowEnvelope struct {
	kind    rowKind
	payload any
}

// observationRow mirrors stormglass_observations. Nullable columns (the "if
// len(ob) >= N" fields in the UDP report) use pointers so a nil is stored as
// SQL NULL rather than a zero value — see internal/postgres/writer.go's
// observationRow, which this mirrors field-for-field except that
// precip_type here is *int64 rather than *float64 -- it is a categorical
// enum, INTEGER in both stores -- and timestamp is a raw unix-epoch int64
// rather than time.Time (design §12: SQLite stores epoch integers, not
// TIMESTAMPTZ). The three measurement columns match Postgres exactly.
type observationRow struct {
	id                   uuid.UUID
	serialNumber         string
	timestamp            int64
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
	precipType           *int64
	lightningDistance    *float64
	lightningStrikeCount *float64
	battery              *float64
	reportInterval       *float64
}

type rapidWindRow struct {
	id            uuid.UUID
	serialNumber  string
	timestamp     int64
	windSpeed     float64
	windDirection float64
}

type hubStatusRow struct {
	id           uuid.UUID
	serialNumber string
	timestamp    int64
	uptime       int64
	rssi         float64
	rebootCount  int64
	busErrors    int64
}

type eventRow struct {
	id           uuid.UUID
	serialNumber string
	timestamp    int64
	eventType    string
	distanceKm   *float64
	energy       *float64
}

// flushRequest is the payload of a kindFlush rowEnvelope: it asks the writer
// goroutine to flush its currently-pending local batches synchronously; done
// is closed once the flush completes so Flush can block until it's actually
// on disk.
type flushRequest struct {
	ctx  context.Context
	done chan struct{}
}

// Writer implements sink.MetricsWriter for the SQLite store. A single
// goroutine (run) owns all writes to db, consuming one buffered channel of
// rowEnvelope and batching per table by BatchSize/FlushInterval — SQLite is
// single-writer, so serializing every insert through one goroutine (backed
// by db.SetMaxOpenConns(1) in Open) is what avoids SQLITE_BUSY, and it makes
// the shutdown drain trivially correct (design §10).
type Writer struct {
	db *sql.DB

	// readDB serves the read methods (LatestObservation, LatestObservationAny,
	// HistoryPoints). It defaults to db when WithReadDB is not supplied, so
	// existing callers observe identical behavior. When set to a dedicated
	// read-only handle (via WithReadDB), heavy query-side scans run
	// concurrently with the single ingest writer instead of queuing behind it.
	readDB *sql.DB

	rows chan rowEnvelope

	batchSize     int
	flushInterval time.Duration

	wg sync.WaitGroup

	// done is the sole shutdown signal: closing it tells every producer send
	// (enqueue/enqueueEvent) and the writer goroutine that Close is in
	// progress. w.rows is never closed, so a concurrent producer send can
	// never panic on a send-on-closed-channel (mirrors the postgres writer's
	// D-H1 fix).
	done      chan struct{}
	closeOnce sync.Once

	// shutdownCtx is the live ctx the writer goroutine uses for its final
	// drain-and-flush on <-done — never a stored construction-time ctx, which
	// SIGTERM may have already canceled by the time Close runs in production
	// (mirrors the postgres writer's C-H1 fix and shutdownCtx field comment).
	// Close sets shutdownCtx before close(done); the Go memory model
	// guarantees that write happens-before the goroutine's <-done case fires,
	// so this plain field is safely visible without a mutex.
	shutdownCtx context.Context
}

// WriterOption configures a Writer at construction.
type WriterOption func(*Writer)

// WithReadDB routes read methods (LatestObservation, LatestObservationAny,
// HistoryPoints) through a dedicated read-only handle instead of the single
// write connection, so heavy reads never queue behind ingest. Pass a handle
// opened via OpenReadOnly.
func WithReadDB(db *sql.DB) WriterOption {
	return func(w *Writer) { w.readDB = db }
}

// NewWriter starts the single writer goroutine and returns a Writer backed
// by db (opened and migrated via Open). ctx bounds only the writer's normal
// (non-shutdown) database operations; Close(ctx) supplies the live context
// used for the final drain-and-flush. NewWriter does not take ownership of
// db — db was created by the caller (via Open), and Close does not close it;
// the caller remains responsible for db.Close() once every writer using it
// has been closed. Without WithReadDB, read methods use db like before;
// callers on a single connection keep identical behavior.
func NewWriter(ctx context.Context, db *sql.DB, cfg Config, opts ...WriterOption) *Writer {
	w := &Writer{
		db:            db,
		rows:          make(chan rowEnvelope, rowChanCap),
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	if w.readDB == nil {
		w.readDB = db
	}
	w.wg.Add(1)
	go w.run(ctx)
	return w
}

// flushBatches sends whichever of the four batches are non-empty to their
// table-specific insert helper, then resets each to length 0 (retaining
// capacity, so the caller's slice doesn't need reallocating on the next
// accumulation). Batches are passed by pointer since this resets the
// caller's own slice variables in place — the same effect as the local
// closure it replaces, extracted to a named method so its branches count
// against flushBatches's own complexity instead of run's.
func (w *Writer) flushBatches(
	fctx context.Context,
	obsBatch *[]observationRow, windBatch *[]rapidWindRow, hubBatch *[]hubStatusRow, evtBatch *[]eventRow,
) {
	if len(*obsBatch) > 0 {
		w.insertObservations(fctx, *obsBatch)
		*obsBatch = (*obsBatch)[:0]
	}
	if len(*windBatch) > 0 {
		w.insertRapidWind(fctx, *windBatch)
		*windBatch = (*windBatch)[:0]
	}
	if len(*hubBatch) > 0 {
		w.insertHubStatus(fctx, *hubBatch)
		*hubBatch = (*hubBatch)[:0]
	}
	if len(*evtBatch) > 0 {
		w.insertEvents(fctx, *evtBatch)
		*evtBatch = (*evtBatch)[:0]
	}
}

// run is the single writer goroutine: it owns all local batch state and is
// the only goroutine that ever touches db.
func (w *Writer) run(ctx context.Context) {
	defer w.wg.Done()

	var (
		obsBatch  []observationRow
		windBatch []rapidWindRow
		hubBatch  []hubStatusRow
		evtBatch  []eventRow
	)

	flush := func(fctx context.Context) {
		w.flushBatches(fctx, &obsBatch, &windBatch, &hubBatch, &evtBatch)
	}

	// The ordinary (non-shutdown) flushes below deliberately drop
	// cancellation from ctx. Shutdown cancels ctx *before* Close runs, and
	// flushBatches truncates each batch whether or not the insert succeeded —
	// the insert helpers return nothing and only log — so a flush landing in
	// that window destroys its rows outright, with no drain able to recover
	// them (#154; the same defect as the postgres writer's #111). The
	// <-w.done branch keeps using w.shutdownCtx, and an explicit Flush keeps
	// using the request's own ctx, so both remain bounded by a live deadline.
	steadyCtx := context.WithoutCancel(ctx)

	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case env := <-w.rows:
			if env.kind == kindFlush {
				// A control message, not a row: flush whatever is pending
				// using the request's own ctx, then signal completion. FIFO
				// order on this single channel guarantees every row enqueued
				// before this Flush call was already appended above.
				req := env.payload.(flushRequest)
				flush(req.ctx)
				close(req.done)
				continue
			}
			obsBatch, windBatch, hubBatch, evtBatch = appendEnvelope(env, obsBatch, windBatch, hubBatch, evtBatch)
			if len(obsBatch) >= w.batchSize || len(windBatch) >= w.batchSize ||
				len(hubBatch) >= w.batchSize || len(evtBatch) >= w.batchSize {
				flush(steadyCtx)
			}

		case <-ticker.C:
			flush(steadyCtx)

		case <-w.done:
			// Drain whatever is still buffered in w.rows (non-blocking): the
			// channel is never closed, so a concurrent producer may have sent
			// a row just before observing <-w.done, and that row must still
			// reach the DB rather than being silently lost (fixes the class
			// of C-H1 — the postgres writer's drain only covered a local
			// slice, not the channel buffer).
			for {
				var env rowEnvelope
				select {
				case env = <-w.rows:
				default:
					flush(w.shutdownCtx)
					return
				}
				if env.kind == kindFlush {
					// An in-flight Flush() call raced Close(): satisfy it
					// with the shutdown flush that's about to happen rather
					// than dropping it silently (Flush's own select also
					// falls back to <-w.done, so this isn't load-bearing for
					// correctness, but it's more honest than a silent drop).
					close(env.payload.(flushRequest).done)
					continue
				}
				obsBatch, windBatch, hubBatch, evtBatch = appendEnvelope(env, obsBatch, windBatch, hubBatch, evtBatch)
			}
		}
	}
}

// appendEnvelope routes env's payload into the batch slice matching its
// kind. The single switch here is the one place the kind -> slice mapping
// lives, shared by both run's normal receive case and its shutdown drain
// loop.
func appendEnvelope(
	env rowEnvelope,
	obsBatch []observationRow, windBatch []rapidWindRow, hubBatch []hubStatusRow, evtBatch []eventRow,
) ([]observationRow, []rapidWindRow, []hubStatusRow, []eventRow) {
	switch env.kind {
	case kindObservation:
		obsBatch = append(obsBatch, env.payload.(observationRow))
	case kindRapidWind:
		windBatch = append(windBatch, env.payload.(rapidWindRow))
	case kindHubStatus:
		hubBatch = append(hubBatch, env.payload.(hubStatusRow))
	case kindEvent:
		evtBatch = append(evtBatch, env.payload.(eventRow))
	}
	return obsBatch, windBatch, hubBatch, evtBatch
}

// enqueue sends a CONTINUOUS row (observation/rapid_wind/hub_status).
// Non-blocking: if the channel is momentarily saturated, the row is dropped
// with a warning rather than blocking the caller — a fresh observation
// supersedes a dropped one within the next report interval anyway.
func (w *Writer) enqueue(ctx context.Context, r rowEnvelope) error {
	select {
	case w.rows <- r:
		return nil
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		slog.Warn("sqlite: row channel full, dropping continuous row", "kind", r.kind)
		return nil
	}
}

// enqueueEvent sends a DISCRETE event row (lightning strike / rain start).
// Unlike enqueue, it blocks up to eventBlockTimeout rather than dropping
// immediately: a discrete event has no "next one supersedes it" replacement,
// so a rare period of saturation must not silently lose it (fixes C-MEDIUM).
func (w *Writer) enqueueEvent(ctx context.Context, r rowEnvelope) error {
	t := time.NewTimer(eventBlockTimeout)
	defer t.Stop()
	select {
	case w.rows <- r:
		return nil
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		slog.Warn("sqlite: event channel full after block timeout, dropping event", "kind", r.kind)
		return nil
	}
}

// WriteReport implements sink.MetricsWriter.
func (w *Writer) WriteReport(ctx context.Context, report tempestudp.Report) error {
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
		// Unknown report type (e.g. device_status) - not an error.
		return nil
	}
}

// handleObservationReport mirrors internal/postgres/writer.go's
// handleObservationReport field-for-field (see that file's comment at
// handleObservationReport for the field-index table), except precip_type is
// converted to int64 (categorical enum, INTEGER in both stores) and timestamp
// is stored as a raw unix-epoch int64 (design §12).
func (w *Writer) handleObservationReport(ctx context.Context, r *tempestudp.TempestObservationReport) error {
	for _, ob := range r.Obs {
		if len(ob) < 13 {
			continue
		}

		wetBulb := tempestudp.WetBulbTemperatureC(ob[7], ob[8], ob[6])

		row := observationRow{
			id:            uuid.Must(uuid.NewV7()),
			serialNumber:  r.SerialNumber,
			timestamp:     int64(ob[0]),
			windLull:      ob[1],
			windAvg:       ob[2],
			windGust:      ob[3],
			windDirection: ob[4],
			pressure:      ob[6], // raw mb value (no conversion)
			tempAir:       ob[7],
			humidity:      ob[8],
			illuminance:   ob[9],
			uvIndex:       ob[10],
			irradiance:    ob[11],
			rainRate:      ob[12],
		}

		// WetBulbTemperatureC returns NaN for non-convergent inputs; store
		// SQL NULL rather than IEEE NaN (mirrors the same guard in
		// tempestudp/report.go and postgres's handleObservationReport).
		if !math.IsNaN(wetBulb) {
			row.tempWetbulb = &wetBulb
		}

		if len(ob) >= 6 {
			interval := ob[5]
			row.windSampleInterval = &interval
		}
		if len(ob) >= 14 {
			precipType := int64(ob[13])
			row.precipType = &precipType
		}
		if len(ob) >= 16 {
			distance := ob[14]
			count := ob[15]
			row.lightningDistance = &distance
			row.lightningStrikeCount = &count
		}
		if len(ob) >= 17 {
			battery := ob[16]
			row.battery = &battery
		}
		if len(ob) >= 18 {
			interval := ob[17]
			row.reportInterval = &interval
		}

		// Continuous row: non-blocking enqueue.
		if err := w.enqueue(ctx, rowEnvelope{kind: kindObservation, payload: row}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) handleRapidWindReport(ctx context.Context, r *tempestudp.RapidWindReport) error {
	if len(r.Ob) != 3 {
		return nil // invalid data
	}
	row := rapidWindRow{
		id:            uuid.Must(uuid.NewV7()),
		serialNumber:  r.SerialNumber,
		timestamp:     int64(r.Ob[0]),
		windSpeed:     r.Ob[1],
		windDirection: r.Ob[2],
	}
	return w.enqueue(ctx, rowEnvelope{kind: kindRapidWind, payload: row})
}

func (w *Writer) handleHubStatusReport(ctx context.Context, r *tempestudp.HubStatusReport) error {
	if len(r.RadioStats) < 3 {
		return nil // invalid data
	}
	row := hubStatusRow{
		id:           uuid.Must(uuid.NewV7()),
		serialNumber: r.SerialNumber,
		timestamp:    r.Timestamp,
		uptime:       int64(r.Uptime),
		rssi:         r.Rssi,
		rebootCount:  int64(r.RadioStats[1]),
		busErrors:    int64(r.RadioStats[2]),
	}
	return w.enqueue(ctx, rowEnvelope{kind: kindHubStatus, payload: row})
}

func (w *Writer) handleRainStartReport(ctx context.Context, r *tempestudp.RainStartReport) error {
	if len(r.Evt) < 1 {
		return nil // invalid data
	}
	row := eventRow{
		id:           uuid.Must(uuid.NewV7()),
		serialNumber: r.SerialNumber,
		timestamp:    int64(r.Evt[0]),
		eventType:    "rain_start",
	}
	// Discrete event: bounded-block enqueue (fixes C-MEDIUM).
	return w.enqueueEvent(ctx, rowEnvelope{kind: kindEvent, payload: row})
}

func (w *Writer) handleLightningStrikeReport(ctx context.Context, r *tempestudp.LightningStrikeReport) error {
	if len(r.Evt) < 3 {
		return nil // invalid data
	}
	distance := r.Evt[1]
	energy := r.Evt[2]
	row := eventRow{
		id:           uuid.Must(uuid.NewV7()),
		serialNumber: r.SerialNumber,
		timestamp:    int64(r.Evt[0]),
		eventType:    "lightning_strike",
		distanceKm:   &distance,
		energy:       &energy,
	}
	return w.enqueueEvent(ctx, rowEnvelope{kind: kindEvent, payload: row})
}

// WriteMetrics implements sink.MetricsWriter as a documented no-op. SQLite is
// the UDP-mode real-time store, not an API-export target: API export mode
// writes to Postgres/gz files (see CLAUDE.md's operational-modes table), so
// there is no present consumer for reconstructing observations from
// Prometheus metrics here (that logic already lives in
// postgres.PostgresWriter.WriteMetrics) — building it for SQLite would be
// YAGNI.
func (w *Writer) WriteMetrics(_ context.Context, _ []prometheus.Metric) error {
	return nil
}

// Flush forces the writer goroutine to flush its currently-pending local
// batches to db and blocks until that flush completes. It is sent as a
// kindFlush rowEnvelope through the SAME w.rows channel every row travels
// through (never a separate channel) so it is FIFO-ordered after every row
// enqueued before this call — see the kindFlush constant's comment for why a
// separate channel would race.
func (w *Writer) Flush(ctx context.Context) error {
	req := flushRequest{ctx: ctx, done: make(chan struct{})}
	select {
	case w.rows <- rowEnvelope{kind: kindFlush, payload: req}:
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-req.done:
		return nil
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close implements sink.MetricsWriter. Idempotent (sync.Once) and
// panic-free. It signals the writer goroutine to drain w.rows and flush its
// final batch using ctx, then waits for it to finish. Close does not close
// db — NewWriter did not create it, so ownership stays with whoever called
// Open.
func (w *Writer) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		// Set shutdownCtx before close(done): this write happens-before
		// close(w.done), which happens-before run's <-w.done case firing
		// (Go memory model), so run is guaranteed to see this live ctx.
		w.shutdownCtx = ctx
		close(w.done)
		w.wg.Wait()
	})
	return nil
}

// execBatch runs execRow for each element of batch inside a single
// transaction against db, using a prepared statement built from query. This
// is the one place the "batch insert in a transaction with a prepared
// statement" mechanics live — if that changed (e.g. added retry logic), all
// four insert*/table pairs below would need to change together, which is
// exactly the shared-knowledge test DRY asks for extraction to pass. The
// per-table SQL and argument binding stay in each insert* function, since
// those differ per table and are not shared knowledge.
func execBatch[T any](ctx context.Context, db *sql.DB, query string, batch []T, execRow func(stmt *sql.Stmt, row T) error) error {
	if len(batch) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() {
		_ = stmt.Close()
	}()

	for i, row := range batch {
		if err := execRow(stmt, row); err != nil {
			return fmt.Errorf("exec row %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}
	return nil
}

const insertObservationSQL = `
	INSERT INTO stormglass_observations (
		id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

func (w *Writer) insertObservations(ctx context.Context, batch []observationRow) {
	err := execBatch(ctx, w.db, insertObservationSQL, batch, func(stmt *sql.Stmt, row observationRow) error {
		_, err := stmt.ExecContext(ctx,
			row.id.String(), row.serialNumber, row.timestamp,
			row.windLull, row.windAvg, row.windGust, row.windDirection, row.windSampleInterval,
			row.pressure, row.tempAir, row.tempWetbulb, row.humidity,
			row.illuminance, row.uvIndex, row.irradiance, row.rainRate, row.precipType,
			row.lightningDistance, row.lightningStrikeCount,
			row.battery, row.reportInterval)
		return err
	})
	if err != nil {
		slog.Error("sqlite: insert observations failed", "error", err, "rows", len(batch))
	}
}

const insertRapidWindSQL = `
	INSERT INTO stormglass_rapid_wind (
		id, serial_number, timestamp, wind_speed, wind_direction
	) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

func (w *Writer) insertRapidWind(ctx context.Context, batch []rapidWindRow) {
	err := execBatch(ctx, w.db, insertRapidWindSQL, batch, func(stmt *sql.Stmt, row rapidWindRow) error {
		_, err := stmt.ExecContext(ctx, row.id.String(), row.serialNumber, row.timestamp, row.windSpeed, row.windDirection)
		return err
	})
	if err != nil {
		slog.Error("sqlite: insert rapid_wind failed", "error", err, "rows", len(batch))
	}
}

const insertHubStatusSQL = `
	INSERT INTO stormglass_hub_status (
		id, serial_number, timestamp, uptime, rssi, reboot_count, bus_errors
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (serial_number, timestamp) DO NOTHING
`

func (w *Writer) insertHubStatus(ctx context.Context, batch []hubStatusRow) {
	err := execBatch(ctx, w.db, insertHubStatusSQL, batch, func(stmt *sql.Stmt, row hubStatusRow) error {
		_, err := stmt.ExecContext(ctx, row.id.String(), row.serialNumber, row.timestamp, row.uptime, row.rssi, row.rebootCount, row.busErrors)
		return err
	})
	if err != nil {
		slog.Error("sqlite: insert hub_status failed", "error", err, "rows", len(batch))
	}
}

const insertEventSQL = `
	INSERT INTO stormglass_events (
		id, serial_number, timestamp, event_type, distance_km, energy
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT (serial_number, timestamp, event_type) DO NOTHING
`

func (w *Writer) insertEvents(ctx context.Context, batch []eventRow) {
	err := execBatch(ctx, w.db, insertEventSQL, batch, func(stmt *sql.Stmt, row eventRow) error {
		_, err := stmt.ExecContext(ctx, row.id.String(), row.serialNumber, row.timestamp, row.eventType, row.distanceKm, row.energy)
		return err
	})
	if err != nil {
		slog.Error("sqlite: insert events failed", "error", err, "rows", len(batch))
	}
}

// ErrObservationNotFound is returned by LatestObservation when serial has no
// rows in stormglass_observations.
var ErrObservationNotFound = errors.New("sqlite: no observation found for serial")

// Observation is a single stormglass_observations row exactly as stored: raw SI
// values, no derived fields. Contract C's GET /api/observations/current is
// fed by this struct, but computing feelsLike/dewPoint/wetBulbTemperature/
// heatIndex/windChill/pressureTrend from it is WS1's job (analytics), not
// this package's (storage) -- building that here would be YAGNI. Nullable
// columns (the same set observationRow treats as nullable, since only this
// writer ever populates the table) are pointers; nil means SQL NULL.
type Observation struct {
	ID                   string
	SerialNumber         string
	Timestamp            int64
	WindLull             float64
	WindAvg              float64
	WindGust             float64
	WindDirection        float64
	WindSampleInterval   *float64
	Pressure             float64
	TempAir              float64
	TempWetbulb          *float64
	Humidity             float64
	Illuminance          float64
	UVIndex              float64
	Irradiance           float64
	RainRate             float64
	PrecipType           *int64
	LightningDistance    *float64
	LightningStrikeCount *float64
	Battery              *float64
	ReportInterval       *float64
}

const selectLatestObservationSQL = `
	SELECT id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	FROM stormglass_observations
	WHERE serial_number = ?
	ORDER BY timestamp DESC
	LIMIT 1
`

// scanObservation scans a single stormglass_observations row (the exact column
// list both selectLatestObservationSQL and selectLatestObservationAnySQL
// select, in the same order) into an Observation, converting SQL NULL
// columns to nil pointers. This is the one place that column-order <->
// struct-field mapping lives: LatestObservation and LatestObservationAny
// select the identical column list (one scoped by serial, one not), so if the
// schema or column order ever changes, both callers must change together --
// that shared knowledge is exactly what this extraction single-sources.
func scanObservation(row *sql.Row) (Observation, error) {
	var (
		obs                                                      Observation
		precipType                                               sql.NullInt64
		windSampleInterval, lightningStrikeCount, reportInterval sql.NullFloat64
		tempWetbulb, lightningDistance, battery                  sql.NullFloat64
	)

	err := row.Scan(
		&obs.ID, &obs.SerialNumber, &obs.Timestamp,
		&obs.WindLull, &obs.WindAvg, &obs.WindGust, &obs.WindDirection, &windSampleInterval,
		&obs.Pressure, &obs.TempAir, &tempWetbulb, &obs.Humidity,
		&obs.Illuminance, &obs.UVIndex, &obs.Irradiance, &obs.RainRate, &precipType,
		&lightningDistance, &lightningStrikeCount,
		&battery, &reportInterval,
	)
	if err != nil {
		return Observation{}, err
	}

	if windSampleInterval.Valid {
		obs.WindSampleInterval = &windSampleInterval.Float64
	}
	if tempWetbulb.Valid {
		obs.TempWetbulb = &tempWetbulb.Float64
	}
	if precipType.Valid {
		obs.PrecipType = &precipType.Int64
	}
	if lightningDistance.Valid {
		obs.LightningDistance = &lightningDistance.Float64
	}
	if lightningStrikeCount.Valid {
		obs.LightningStrikeCount = &lightningStrikeCount.Float64
	}
	if battery.Valid {
		obs.Battery = &battery.Float64
	}
	if reportInterval.Valid {
		obs.ReportInterval = &reportInterval.Float64
	}

	return obs, nil
}

// LatestObservation returns the newest stormglass_observations row for serial
// (ORDER BY timestamp DESC LIMIT 1). Returns ErrObservationNotFound,
// checkable via errors.Is, when serial has no rows.
func (w *Writer) LatestObservation(ctx context.Context, serial string) (Observation, error) {
	row := w.readDB.QueryRowContext(ctx, selectLatestObservationSQL, serial)
	obs, err := scanObservation(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Observation{}, fmt.Errorf("%w: %s", ErrObservationNotFound, serial)
	case err != nil:
		return Observation{}, fmt.Errorf("query latest observation: %w", err)
	}
	return obs, nil
}

const selectLatestObservationAnySQL = `
	SELECT id, serial_number, timestamp,
		wind_lull, wind_avg, wind_gust, wind_direction, wind_sample_interval,
		pressure, temp_air, temp_wetbulb, humidity,
		illuminance, uv_index, irradiance, rain_rate, precip_type,
		lightning_distance, lightning_strike_count,
		battery, report_interval
	FROM stormglass_observations
	ORDER BY timestamp DESC
	LIMIT 1
`

// LatestObservationAny returns the newest stormglass_observations row across ALL
// serials (identical to LatestObservation but without the WHERE
// serial_number clause). GET /api/observations/current is a single-station
// appliance endpoint with no serial to scope by, so "newest observation
// overall" is the correct resolution -- see the httpserver package's
// registerObservations for how the API handler uses this. Returns
// ErrObservationNotFound, checkable via errors.Is, when the table is empty.
func (w *Writer) LatestObservationAny(ctx context.Context) (Observation, error) {
	row := w.readDB.QueryRowContext(ctx, selectLatestObservationAnySQL)
	obs, err := scanObservation(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Observation{}, fmt.Errorf("%w: no observations in table", ErrObservationNotFound)
	case err != nil:
		return Observation{}, fmt.Errorf("query latest observation (any serial): %w", err)
	}
	return obs, nil
}

// historyFieldColumns is the allowlist mapping an API-level history field
// name to its stormglass_observations column, covering the numeric columns the
// UI charts. HistoryPoints looks field up here BEFORE building any query --
// an unknown field returns an error and no query ever runs. The resolved
// column value (never the raw field argument) is the only thing formatted
// into the query text, which is what makes that safe from SQL injection.
var historyFieldColumns = map[string]string{
	"wind_lull":          "wind_lull",
	"wind_avg":           "wind_avg",
	"wind_gust":          "wind_gust",
	"wind_direction":     "wind_direction",
	"pressure":           "pressure",
	"temp_air":           "temp_air",
	"temp_wetbulb":       "temp_wetbulb",
	"humidity":           "humidity",
	"illuminance":        "illuminance",
	"uv_index":           "uv_index",
	"irradiance":         "irradiance",
	"rain_rate":          "rain_rate",
	"battery":            "battery",
	"lightning_distance": "lightning_distance",
}

// Point is a single (t, v) sample from HistoryPoints. Field names and JSON
// tags mirror Contract C's `{"points":[{"t":..,"v":..}]}` wire shape (design
// §11) so that shape is single-sourced here rather than redeclared by every
// consumer.
type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// HistoryPoints returns every stormglass_observations sample for field with a
// timestamp in [from, to], ordered by timestamp ascending, capped at
// maxHistoryPoints (an unbounded or wide range is truncated rather than
// returning the whole table -- SGE review I1). field must be a key of
// historyFieldColumns; an unknown field (including anything
// SQL-injection-shaped) is rejected before any query is built or executed.
// Rows where the column is SQL NULL (e.g. temp_wetbulb before wet-bulb
// convergence) are omitted rather than reported as a misleading 0.
func (w *Writer) HistoryPoints(ctx context.Context, field string, from, to int64) ([]Point, error) {
	column, ok := historyFieldColumns[field]
	if !ok {
		return nil, fmt.Errorf("sqlite: unknown history field %q", field)
	}

	query := fmt.Sprintf(
		`SELECT timestamp, %s FROM stormglass_observations WHERE timestamp BETWEEN ? AND ? ORDER BY timestamp LIMIT %d`,
		column, maxHistoryPoints,
	)

	rows, err := w.readDB.QueryContext(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("query history points: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	// Initialized non-nil so a query with zero matches marshals to JSON `[]`
	// rather than `null` -- Contract C's {"points":[...]} shape (design §11).
	points := []Point{}
	for rows.Next() {
		var (
			t int64
			v sql.NullFloat64
		)
		if err := rows.Scan(&t, &v); err != nil {
			return nil, fmt.Errorf("scan history point: %w", err)
		}
		if !v.Valid {
			continue
		}
		points = append(points, Point{T: t, V: v.Float64})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history points: %w", err)
	}
	return points, nil
}

// Summary is the windowed aggregate over stormglass_observations in [from, to].
// Every aggregate is nullable: MIN/MAX/SUM over zero rows (or an all-NULL
// column) return SQL NULL, not 0, so each is scanned into a sql.Null* rather
// than silently reporting a misleading zero value. Count == 0 is the
// authoritative empty-window signal -- callers must check it before reading
// any other field.
type Summary struct {
	Count          int64
	CoveredFrom    sql.NullInt64
	CoveredTo      sql.NullInt64
	TempMax        sql.NullFloat64
	TempMin        sql.NullFloat64
	HumidityMax    sql.NullFloat64
	HumidityMin    sql.NullFloat64
	PressureMax    sql.NullFloat64
	PressureMin    sql.NullFloat64
	WindMax        sql.NullFloat64
	GustMax        sql.NullFloat64
	RainTotal      sql.NullFloat64
	LightningTotal sql.NullFloat64
}

const summarizeObservationsSQL = `
	SELECT
	  COUNT(*),
	  MIN(timestamp), MAX(timestamp),
	  MAX(temp_air),  MIN(temp_air),
	  MAX(humidity),  MIN(humidity),
	  MAX(pressure),  MIN(pressure),
	  MAX(wind_avg),  MAX(wind_gust),
	  SUM(rain_rate),                 -- rain_rate = obs_st[12], per-interval mm accumulation -> SUM = total mm
	  SUM(lightning_strike_count)
	FROM stormglass_observations
	WHERE timestamp BETWEEN ? AND ?
`

// SummarizeObservations aggregates the stormglass_observations rows in [from,
// to] into one Summary. Aggregates over 0 rows (or all-NULL columns) are SQL
// NULL, surfaced via sql.Null*; Count == 0 signals an empty window. Runs on
// the read-only handle (w.readDB) so a wide scan never queues behind the
// single ingest writer connection.
func (w *Writer) SummarizeObservations(ctx context.Context, from, to int64) (Summary, error) {
	var s Summary
	err := w.readDB.QueryRowContext(ctx, summarizeObservationsSQL, from, to).Scan(
		&s.Count, &s.CoveredFrom, &s.CoveredTo,
		&s.TempMax, &s.TempMin, &s.HumidityMax, &s.HumidityMin,
		&s.PressureMax, &s.PressureMin, &s.WindMax, &s.GustMax,
		&s.RainTotal, &s.LightningTotal,
	)
	if err != nil {
		return Summary{}, fmt.Errorf("summarize observations: %w", err)
	}
	return s, nil
}

// TempExtremes carries the max and min air temperature over a window with
// the timestamp at which each occurred. Every field is nullable: a window
// with no non-NULL temp_air row -- which includes both an empty window and a
// window whose every temp_air is NULL -- leaves all four invalid. A value
// and its timestamp are always valid together or invalid together; the
// queries cannot produce a mismatched pair.
type TempExtremes struct {
	Max   sql.NullFloat64
	MaxAt sql.NullInt64
	Min   sql.NullFloat64
	MinAt sql.NullInt64
}

// The two extreme queries are written out in full rather than built from a
// shared template with an interpolated direction: they express different
// knowledge (which extreme), the duplication is two characters, and no
// direction string is ever formatted into SQL text.
//
// `temp_air ASC|DESC` selects the extreme; `timestamp ASC` breaks ties
// toward the EARLIEST occurrence; and `temp_air IS NOT NULL` both keeps a
// NULL row from ranking first on the ascending query and makes an empty or
// all-NULL window return zero rows rather than a NULL/timestamp mismatch.
//
// Note this is NOT an aggregate query. `SELECT max(temp_air), timestamp ...
// ORDER BY timestamp` looks equivalent and is not: an aggregate with no
// GROUP BY returns one row, so its ORDER BY is a no-op and SQLite documents
// the bare column as arbitrary among ties (sqlite.org/lang_select §2.5).
const (
	temperatureMaxSQL = `
	SELECT temp_air, timestamp FROM stormglass_observations
	 WHERE timestamp BETWEEN ? AND ? AND temp_air IS NOT NULL
	 ORDER BY temp_air DESC, timestamp ASC
	 LIMIT 1
`
	temperatureMinSQL = `
	SELECT temp_air, timestamp FROM stormglass_observations
	 WHERE timestamp BETWEEN ? AND ? AND temp_air IS NOT NULL
	 ORDER BY temp_air ASC, timestamp ASC
	 LIMIT 1
`
)

// TemperatureExtremes returns the max and min air temperature in [from, to]
// with the timestamp at which each occurred.
//
// Max/Min are invalid when the window contains no row with a non-NULL
// temp_air. Ties resolve to the EARLIEST occurrence. Runs on the read-only
// handle so a wide scan never queues behind the single ingest writer.
func (w *Writer) TemperatureExtremes(ctx context.Context, from, to int64) (TempExtremes, error) {
	var te TempExtremes

	maxV, maxAt, err := w.temperatureExtreme(ctx, temperatureMaxSQL, from, to)
	if err != nil {
		return TempExtremes{}, err
	}
	minV, minAt, err := w.temperatureExtreme(ctx, temperatureMinSQL, from, to)
	if err != nil {
		return TempExtremes{}, err
	}

	te.Max, te.MaxAt, te.Min, te.MinAt = maxV, maxAt, minV, minAt
	return te, nil
}

// temperatureExtreme runs one of the two extreme queries. sql.ErrNoRows is
// the empty/all-NULL window and is NOT an error: it yields an invalid value
// and an invalid timestamp, together.
func (w *Writer) temperatureExtreme(ctx context.Context, query string, from, to int64) (sql.NullFloat64, sql.NullInt64, error) {
	var value sql.NullFloat64
	var at sql.NullInt64

	err := w.readDB.QueryRowContext(ctx, query, from, to).Scan(&value, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullFloat64{}, sql.NullInt64{}, nil
	}
	if err != nil {
		return sql.NullFloat64{}, sql.NullInt64{}, fmt.Errorf("temperature extreme: %w", err)
	}
	return value, at, nil
}
