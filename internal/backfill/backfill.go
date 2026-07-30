package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"tempestwx-utilities/internal/tempestapi"
	"tempestwx-utilities/internal/weather"
)

const (
	// insertBatchSize bounds one insert transaction.
	//
	// The realistic invocation is `docker exec <running container>
	// tempestwx-utilities backfill` against a LIVE database. A long backfill
	// transaction contends with ingest writes; busy_timeout defaults to 5s
	// and ingest's error path only LOGS (sqlite/writer.go:646), so an
	// unbounded transaction could cause live observations to be silently lost
	// while repairing historical ones. Short transactions are the mitigation.
	insertBatchSize = 200

	// maxAttempts bounds retries per window, including the first try.
	maxAttempts = 4
	// baseBackoff is doubled per attempt: 1s, 2s, 4s.
	baseBackoff = time.Second
)

// ErrSerialMismatch is returned when the API's serials and a non-empty
// store's serials are DISJOINT — no API serial appears in the store at all.
//
// NOT "some API serial is absent from the store": that rule is wrong and
// bricks the tool for a newly added station, or for any station this host
// never hears over UDP. See preflight.
var ErrSerialMismatch = errors.New("station serials disjoint from store")

// Config is the backfill run's parameters. It holds no I/O handles and no
// clock — both are passed to Run explicitly so the core is testable.
type Config struct {
	// From and To bound the work explicitly. Both zero means auto-detect.
	//
	// The override exists to BOUND API WORK, not to avoid a table scan
	// (idx_obs_serial_time already covers detection): because permanent holes
	// are accepted and never recorded, auto-detect re-requests every
	// known-empty window on every run. An operator repairing a known outage
	// can name it and skip that cost.
	From time.Time
	To   time.Time

	// MinGap is the smallest interval that counts as a hole, keeping ordinary
	// reporting jitter from registering.
	MinGap time.Duration

	// DryRun detects and plans only: Run makes zero observation fetches and
	// zero writes.
	//
	// Note the shell still calls ListDevices before invoking Run, so the
	// COMMAND does make one API call in dry-run and therefore does validate
	// the token. Do not document it as "zero API calls".
	DryRun bool
}

// Stats is what the run did, in aggregate.
type Stats struct {
	Gaps int // holes detected
	// Returned counts observations the API actually handed back, AFTER
	// malformed tuples were dropped. It is not "rows requested" — the closed
	// gap interval and the shared chunk-window endpoints both mean a few
	// observations are fetched more than once.
	Returned int
	Inserted int // rows actually new (0 across runs => a permanent hole)
	Failed   int // gaps that failed after retries
}

// NOTE: dropped-tuple counts are deliberately NOT here. They are logged at
// WARN by the decode itself (Task 3), which is where the information exists.
// Threading a diagnostic counter up through ObservationSource would widen the
// seam between backfill and the REST client to carry a reporting nicety, and
// the log stream is already the machine-readable surface this design chose
// when it cut the bespoke summary line.

// ObservationSource is the REST client, narrowed to what backfill needs.
// *tempestapi.Client satisfies it.
type ObservationSource interface {
	Observations(ctx context.Context, station tempestapi.Station, start, end time.Time) ([]weather.Observation, error)
}

// Store is the persistence side, narrowed to what backfill needs. Two
// concrete implementors exist on day one — SQLite and Postgres — so this is
// an earned interface, not a speculative one. It is declared here, in the
// consumer, per Go convention; the adapters that bind a *sql.DB or
// *pgxpool.Pool to it live in the command shell.
type Store interface {
	// DistinctSerials is UNWINDOWED — the whole table. It exists only for the
	// pre-flight check and must NOT be replaced by SeriesBounds' key set:
	// SeriesBounds is windowed, so a station that was simply quiet during the
	// requested window would look absent from the store and trip the check.
	DistinctSerials(ctx context.Context) ([]string, error)
	SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error)
	FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
	InsertObservations(ctx context.Context, obs []weather.Observation) (int, error)
}

// Run detects gaps and fills them.
//
// now is injected: nothing below the command shell calls time.Now(), so
// detectTo (now - MinGap) is deterministic under test.
//
// A failed gap logs and continues — partial progress must be preserved — and
// the per-gap errors are joined into the returned error, which the shell
// turns into a non-zero exit. Run never calls log.Fatal.
func Run(
	ctx context.Context,
	cfg Config,
	src ObservationSource,
	store Store,
	stations []tempestapi.Station,
	now time.Time,
) (Stats, error) {
	var stats Stats

	detectFrom, detectTo := detectionRange(cfg, stations, now)

	storedSerials, err := store.DistinctSerials(ctx)
	if err != nil {
		return stats, fmt.Errorf("distinct serials: %w", err)
	}
	if err := preflight(stations, storedSerials); err != nil {
		return stats, err
	}

	// SeriesBounds is only needed by the auto-detect path — an explicit
	// --from/--to names the window outright and never reads it. Querying it
	// unconditionally would be a wasted round-trip on every explicit run.
	var bounds []weather.Bounds
	if cfg.From.IsZero() || cfg.To.IsZero() {
		bounds, err = store.SeriesBounds(ctx, detectFrom, detectTo)
		if err != nil {
			return stats, fmt.Errorf("series bounds: %w", err)
		}
	}

	gaps, err := plannedGaps(ctx, cfg, store, stations, bounds, detectFrom, detectTo)
	if err != nil {
		return stats, err
	}
	stats.Gaps = len(gaps)

	if cfg.DryRun {
		for _, g := range gaps {
			slog.Info("backfill: planned gap (dry run)",
				"serial", g.SerialNumber, "from", g.From, "to", g.To, "duration", g.Duration())
		}
		return stats, nil
	}

	byserial := make(map[string]tempestapi.Station, len(stations))
	for _, s := range stations {
		byserial[s.SerialNumber] = s
	}

	var failures []error
	for _, g := range gaps {
		returned, inserted, err := fillGap(ctx, src, store, byserial[g.SerialNumber], g)
		stats.Returned += returned
		stats.Inserted += inserted

		if err != nil {
			if ctx.Err() != nil {
				// Cancellation is not a gap failure. Inserted rows stay
				// intact; idempotency makes re-running safe.
				return stats, ctx.Err()
			}
			stats.Failed++
			failures = append(failures, fmt.Errorf("gap %s [%s, %s]: %w",
				g.SerialNumber, g.From.Format(time.RFC3339), g.To.Format(time.RFC3339), err))
			slog.Error("backfill: gap failed",
				"serial", g.SerialNumber, "from", g.From, "to", g.To, "error", err)
			continue
		}

		// returned vs inserted is what makes the permanent-hole tradeoff
		// visible: if the station was genuinely offline, the API has no data
		// either and inserted stays 0 across runs. Structured attrs are the
		// machine-readable surface — no bespoke summary format.
		slog.Info("backfill: gap filled",
			"serial", g.SerialNumber, "from", g.From, "to", g.To,
			"returned", returned, "inserted", inserted)
	}

	if len(failures) > 0 {
		return stats, errors.Join(failures...)
	}
	return stats, nil
}

// detectionRange resolves the window to work over. An explicit --from/--to
// wins; otherwise detection runs from the earliest station creation time to
// now-MinGap (trailing MinGap so the most recent, still-arriving interval is
// not mistaken for a hole).
func detectionRange(cfg Config, stations []tempestapi.Station, now time.Time) (time.Time, time.Time) {
	if !cfg.From.IsZero() && !cfg.To.IsZero() {
		return cfg.From, cfg.To
	}
	detectTo := now.Add(-cfg.MinGap)
	detectFrom := detectTo
	for _, s := range stations {
		if s.CreatedAt.Before(detectFrom) {
			detectFrom = s.CreatedAt
		}
	}
	return detectFrom, detectTo
}

// plannedGaps is the whole detection domain: SQL's interior gaps plus the
// head, tail, and empty-store cases LAG cannot see. An explicit --from/--to
// still goes through the chunker, so it is expressed as one gap per station
// rather than bypassing the pipeline.
func plannedGaps(
	ctx context.Context,
	cfg Config,
	store Store,
	stations []tempestapi.Station,
	bounds []weather.Bounds,
	detectFrom, detectTo time.Time,
) ([]weather.Gap, error) {
	serials := make([]string, 0, len(stations))
	for _, s := range stations {
		serials = append(serials, s.SerialNumber)
	}

	if !cfg.From.IsZero() && !cfg.To.IsZero() {
		gaps := make([]weather.Gap, 0, len(serials))
		for _, serial := range serials {
			gaps = append(gaps, weather.Gap{SerialNumber: serial, From: cfg.From, To: cfg.To})
		}
		return gaps, nil
	}

	interior, err := store.FindObservationGaps(ctx, detectFrom, detectTo, cfg.MinGap)
	if err != nil {
		return nil, fmt.Errorf("find observation gaps: %w", err)
	}
	return assembleGaps(interior, bounds, serials, detectFrom, detectTo, cfg.MinGap), nil
}

// preflight refuses to run when the API's serials and the store's serials are
// DISJOINT — no API serial appears in the store at all.
//
// Dedupe, gap closure, and convergence all require that the serial backfill
// writes exactly matches the serial UDP ingest writes. If the two formats
// diverge, backfill writes a PARALLEL SERIES under a second serial: UNIQUE
// never fires, rows double, and the gap never closes — silently and
// cumulatively. So this is a hard stop, not a warning: warning-then-writing-
// anyway names an outcome as corrupting and then produces it.
//
// DISJOINT is the rule, and the distinction is load-bearing. The tempting
// version — "some API serial is absent from a non-empty store" — fires on two
// completely ordinary situations and would brick the tool for both:
//
//   - A second station on the account whose broadcasts this host never hears
//     (different VLAN/subnet). Its serial will NEVER enter the store, so
//     backfill would refuse to run, including for the healthy station.
//   - A newly added station, whose first backfill would exit non-zero having
//     written nothing — permanently, until the daemon happened to ingest a row.
//
// Neither is format divergence. Under the disjoint rule both proceed, and a
// serial the API knows but the store has not seen simply becomes a whole-range
// gap, which is exactly assembleGaps' job.
//
// storedSerials MUST come from DistinctSerials (unwindowed), never from
// SeriesBounds' key set — see the Store interface comment.
//
// An EMPTY store is not a mismatch: it is the first-run case.
func preflight(stations []tempestapi.Station, storedSerials []string) error {
	if len(storedSerials) == 0 {
		return nil
	}
	stored := make(map[string]struct{}, len(storedSerials))
	for _, s := range storedSerials {
		stored[s] = struct{}{}
	}
	for _, st := range stations {
		if _, ok := stored[st.SerialNumber]; ok {
			return nil // at least one serial matches: not divergence
		}
	}

	apiSerials := make([]string, 0, len(stations))
	for _, st := range stations {
		apiSerials = append(apiSerials, st.SerialNumber)
	}
	return fmt.Errorf("%w: API reports %v, store holds %v — no overlap at all, "+
		"which means the two are using different serial formats; backfilling would "+
		"create a parallel series that never dedupes",
		ErrSerialMismatch, apiSerials, storedSerials)
}

// fillGap fetches and inserts one gap, chunked and retried.
//
// A failed WINDOW does not abandon the gap. Returning here on the first bad
// window would discard every remaining window, and for a DETERMINISTIC
// per-window failure that loss is permanent across runs, not transient:
// once the earlier windows land, the head gap collapses and the hole
// reappears as an interior gap starting at the same bad window, which fails
// again — so the windows behind it are never requested on ANY run. A tool
// whose entire premise is convergence would silently stop converging, and the
// inserted=0 signal never even fires because those windows are never
// requested. So: log, accumulate, keep going, and fail the gap at the end.
//
// An INSERT error is different and does abort the gap: it means the store is
// unhealthy, and hammering it with the remaining windows helps nobody.
func fillGap(
	ctx context.Context,
	src ObservationSource,
	store Store,
	station tempestapi.Station,
	g weather.Gap,
) (returned, inserted int, err error) {
	var windowErrs []error

	for _, w := range chunkWindow(g.From, g.To, chunkSize) {
		if err := ctx.Err(); err != nil {
			return returned, inserted, err
		}

		obs, err := fetchWithRetry(ctx, src, station, w)
		if err != nil {
			if ctx.Err() != nil {
				// Bare ctx.Err(), not a join: Run owns cancellation
				// reporting and returns ctx.Err() itself the moment it sees
				// ctx.Err() != nil, so anything joined here is discarded.
				// Building a richer error only to have it thrown away reads
				// as a guarantee this code does not provide.
				return returned, inserted, ctx.Err()
			}
			slog.Error("backfill: window failed, continuing with the rest of the gap",
				"serial", station.SerialNumber, "from", w.from, "to", w.to, "error", err)
			windowErrs = append(windowErrs, fmt.Errorf("window [%s, %s]: %w",
				w.from.Format(time.RFC3339), w.to.Format(time.RFC3339), err))
			continue
		}
		returned += len(obs)

		for chunk := range slices.Chunk(obs, insertBatchSize) {
			n, err := store.InsertObservations(ctx, chunk)
			if err != nil {
				// Abort the gap — an unhealthy store will not be helped by
				// the remaining windows — but keep any window diagnostics
				// already accumulated rather than discarding them.
				return returned, inserted, errors.Join(append(windowErrs, fmt.Errorf("insert: %w", err))...)
			}
			inserted += n
		}
	}

	return returned, inserted, errors.Join(windowErrs...)
}

// fetchWithRetry applies bounded exponential backoff to transient failures.
// Context cancellation is checked between attempts as well as between
// windows.
func fetchWithRetry(
	ctx context.Context,
	src ObservationSource,
	station tempestapi.Station,
	w window,
) ([]weather.Observation, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			delay := baseBackoff << (attempt - 1)
			slog.Warn("backfill: retrying window",
				"serial", station.SerialNumber, "from", w.from, "to", w.to,
				"attempt", attempt+1, "delay", delay, "error", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		obs, err := src.Observations(ctx, station, w.from, w.to)
		if err == nil {
			return obs, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}
