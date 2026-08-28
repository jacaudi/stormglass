// Package main runs the stormglass binary. With no subcommand it
// operates in one of two modes selected by environment: a UDP listener that
// writes live observations to SQLite/PostgreSQL/Prometheus (default), or a
// one-shot historical API export when TOKEN is set. Two explicit
// subcommands bypass that mode selection entirely: "backfill" (this file)
// repairs gaps in stored history via the REST API, and "healthcheck" probes
// a running instance's /healthz endpoint. See docs/configuration.md for the full
// operational-mode matrix.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jacaudi/stormglass/internal/backfill"
	"github.com/jacaudi/stormglass/internal/config"
	"github.com/jacaudi/stormglass/internal/postgres"
	"github.com/jacaudi/stormglass/internal/sqlite"
	"github.com/jacaudi/stormglass/internal/tempestapi"
	"github.com/jacaudi/stormglass/internal/weather"

	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
)

// backfillAPI is the REST surface runBackfill is allowed to touch, and it is
// deliberately narrow.
//
// ListDevices and ListStations have identical signatures, and ListStations
// collapses each station to a single ST device (its device loop has no
// break, so the last one wins). Calling the wrong one on a two-sensor
// station would leave one sensor's gaps permanently unrepaired and unlogged,
// while compiling and passing every test — the maintainer runs a single
// Tempest, so it would never surface locally (#110).
//
// Narrowing the type is what fixes that: ListStations is not a member here,
// so the swap does not build. This is a type-level guarantee, not an
// assertion some future test refactor could delete.
type backfillAPI interface {
	ListDevices(ctx context.Context) ([]tempestapi.Station, error)
	backfill.ObservationSource
}

// newBackfillAPI is the injection point. It returns the narrow interface
// rather than *tempestapi.Client so the concrete client — and with it
// ListStations — never enters runBackfill's scope.
var newBackfillAPI = func(token string) backfillAPI { return tempestapi.NewClient(token) }

// parseBackfillFlags reads the backfill subcommand's own FlagSet.
//
// Each subcommand owning its FlagSet (parsed from os.Args[2:]) is the seam a
// future subcommand slots into: a new subcommand is a new file plus one
// dispatch line, with no sibling touched.
//
// --from/--to are RFC3339 interpreted as UTC. The store is UTC epoch and the
// API takes epoch seconds; an ambiguous local-time parse would be a quiet
// wrong-window bug, so a non-RFC3339 value is rejected rather than guessed at.
func parseBackfillFlags(args []string) (backfill.Config, string, error) {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fromStr := fs.String("from", "", "start of the window to repair, RFC3339 UTC (default: auto-detect)")
	toStr := fs.String("to", "", "end of the window to repair, RFC3339 UTC (default: auto-detect)")
	minGap := fs.Duration("min-gap", 30*time.Minute, "smallest interval that counts as a gap")
	dryRun := fs.Bool("dry-run", false, "detect and plan only: no observation fetches, no writes")

	store := fs.String("store", "", "which store to repair: sqlite or postgres (required when both are configured)")

	if err := fs.Parse(args); err != nil {
		return backfill.Config{}, "", err
	}

	cfg := backfill.Config{MinGap: *minGap, DryRun: *dryRun}

	// A non-positive --min-gap is a silent misconfiguration, not a usable
	// setting: zero makes every consecutive one-minute observation pair a
	// "gap", and a negative value pushes detectTo (now - minGap) into the
	// future.
	if *minGap <= 0 {
		return cfg, *store, usageErr(fs, "--min-gap must be positive, got %s", minGap.String())
	}

	// --store is returned separately, not folded into backfill.Config: it
	// selects which handle the SHELL opens, and Run has no use for it. A core
	// config struct carrying a field the core ignores is a trap.
	switch *store {
	case "", "sqlite", "postgres":
	default:
		return cfg, *store, usageErr(fs, "--store must be sqlite or postgres, got %q", *store)
	}

	if (*fromStr == "") != (*toStr == "") {
		return cfg, *store, usageErr(fs, "--from and --to must be given together, or neither")
	}
	if *fromStr != "" {
		from, err := time.Parse(time.RFC3339, *fromStr)
		if err != nil {
			return cfg, *store, usageErr(fs, "--from must be RFC3339 (e.g. 2026-01-02T03:04:05Z): %w", err)
		}
		to, err := time.Parse(time.RFC3339, *toStr)
		if err != nil {
			return cfg, *store, usageErr(fs, "--to must be RFC3339 (e.g. 2026-01-02T03:04:05Z): %w", err)
		}
		cfg.From, cfg.To = from.UTC(), to.UTC()
		if !cfg.To.After(cfg.From) {
			return cfg, *store, usageErr(fs, "--to must be after --from")
		}
	}
	return cfg, *store, nil
}

// usageErr reports a validation failure the way flag reports a parse failure:
// message plus usage, on stderr. Centralizing it means runBackfill prints
// nothing itself, so flag's message is never duplicated.
func usageErr(fs *flag.FlagSet, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	_, _ = fmt.Fprintln(fs.Output(), err)
	fs.Usage()
	return err
}

// resolveStore decides which single store this run repairs.
//
// Backfill repairs ONE store per run. selectStore returns BOTH when
// ENABLE_POSTGRES=true and SQLITE_PATH is set — a documented fan-out the
// daemon honors by writing every observation to both (main.go:302-336). In
// that configuration backfill cannot infer the target, and guessing is the
// worst option available: repairing Postgres while leaving the
// Litestream-replicated SQLite database still holed, then exiting 0, tells
// the operator the history is fixed when it is not.
func resolveStore(choice storeChoice, flagValue string) (string, error) {
	var configured []string
	if choice.sqlite {
		configured = append(configured, "sqlite")
	}
	if choice.postgres {
		configured = append(configured, "postgres")
	}

	switch len(configured) {
	case 0:
		return "", errors.New("backfill: no store configured; set SQLITE_PATH, or ENABLE_POSTGRES=true with POSTGRES_URL")
	case 1:
		if flagValue != "" && flagValue != configured[0] {
			return "", fmt.Errorf("backfill: --store=%s, but only %s is configured", flagValue, configured[0])
		}
		return configured[0], nil
	default:
		if flagValue == "" {
			return "", fmt.Errorf(
				"backfill: both %s are configured; pass --store=sqlite or --store=postgres. "+
					"Backfill repairs one store per run and will not guess — silently repairing "+
					"only one while reporting success would leave the other permanently holed",
				strings.Join(configured, " and "))
		}
		return flagValue, nil
	}
}

// sqliteStore adapts the package-level SQLite functions to backfill.Store.
//
// backfill.Store has FOUR methods. If either adapter fails to compile with
// "does not implement backfill.Store (missing method DistinctSerials)", the
// fix is to ADD the method here. Do NOT remove DistinctSerials from the
// interface — that silently reverts the pre-flight fix, because SeriesBounds
// is windowed and would false-positive on any station quiet during the
// queried window.
type sqliteStore struct{ db *sql.DB }

func (s sqliteStore) DistinctSerials(ctx context.Context) ([]string, error) {
	return sqlite.DistinctSerials(ctx, s.db)
}

func (s sqliteStore) SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error) {
	return sqlite.SeriesBounds(ctx, s.db, from, to)
}

func (s sqliteStore) FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	return sqlite.FindObservationGaps(ctx, s.db, from, to, minGap)
}

func (s sqliteStore) InsertObservations(ctx context.Context, obs []weather.Observation) (int, error) {
	return sqlite.InsertObservations(ctx, s.db, obs)
}

// postgresStore adapts the package-level Postgres functions to backfill.Store.
// See sqliteStore's comment: four methods, and DistinctSerials is not optional.
type postgresStore struct{ pool *pgxpool.Pool }

func (s postgresStore) DistinctSerials(ctx context.Context) ([]string, error) {
	return postgres.DistinctSerials(ctx, s.pool)
}

func (s postgresStore) SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error) {
	return postgres.SeriesBounds(ctx, s.pool, from, to)
}

func (s postgresStore) FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error) {
	return postgres.FindObservationGaps(ctx, s.pool, from, to, minGap)
}

func (s postgresStore) InsertObservations(ctx context.Context, obs []weather.Observation) (int, error) {
	return postgres.InsertObservations(ctx, s.pool, obs)
}

// openBackfillStore opens the write handle for target ("postgres" or
// "sqlite"), creating the schema first for postgres, and returns a cleanup
// func the caller must defer to release it. cleanup is returned rather than
// deferred here: opening the handle and releasing it happen in different
// stack frames (this function returns long before runBackfill is done using
// the store), so the release must run at runBackfill's return, not this
// function's.
//
// On failure the specific error is already logged via slog.Error before
// returning, matching runBackfill's original inline error handling exactly
// — callers only need to check err != nil and return the exit code.
func openBackfillStore(ctx context.Context, target string, choice storeChoice) (backfill.Store, func(), error) {
	switch target {
	case "postgres":
		dbConfig, err := config.GetDatabaseConfig()
		if err != nil {
			slog.Error("backfill: database configuration", "error", err)
			return nil, func() {}, err
		}
		if dbConfig == "" {
			err := errors.New("backfill: POSTGRES_URL or POSTGRES_HOST is required when ENABLE_POSTGRES is true")
			slog.Error(err.Error())
			return nil, func() {}, err
		}
		// Backfill opens the WRITE path: it must create the schema and
		// insert. OpenPool starts no batch-worker goroutines, unlike
		// NewPostgresWriter's four (pgxpool.NewWithConfig itself starts its
		// own internal goroutine for idle-connection maintenance regardless
		// of caller — that's pgx's concern, not the contrast being drawn
		// here).
		pool, err := postgres.OpenPool(ctx, dbConfig)
		if err != nil {
			slog.Error("backfill: open postgres", "error", err)
			return nil, func() {}, err
		}
		if err := postgres.CreateSchema(ctx, pool); err != nil {
			slog.Error("backfill: create postgres schema", "error", err)
			// pool was opened successfully, so it must still be released on
			// this failure path — matches the original's `defer pool.Close()`
			// (registered right after OpenPool succeeded) still firing when
			// CreateSchema fails and runBackfill returns.
			pool.Close()
			return nil, func() {}, err
		}
		return postgresStore{pool: pool}, pool.Close, nil
	case "sqlite":
		// The write handle, not OpenReadOnly: read-only fails when the file
		// does not exist and cannot migrate, and its ingest-contention
		// rationale does not apply to a separate one-shot process.
		db, err := sqlite.Open(ctx, choice.sqlitePath, sqlite.LoadConfig(os.Getenv))
		if err != nil {
			slog.Error("backfill: open sqlite", "path", choice.sqlitePath, "error", err)
			return nil, func() {}, err
		}
		cleanup := func() {
			if err := db.Close(); err != nil {
				slog.Error("backfill: close sqlite", "error", err)
			}
		}
		return sqliteStore{db: db}, cleanup, nil
	default:
		// Unreachable today — resolveStore returns only "sqlite" or
		// "postgres" — but a nil Store interface would panic deep inside Run,
		// far from the cause.
		slog.Error("backfill: unknown store target", "target", target)
		return nil, func() {}, fmt.Errorf("unknown store target %q", target)
	}
}

// runBackfill is the backfill subcommand's shell: parse, read env, open
// handles, wire dependencies, return an exit code.
//
// It performs ALL cleanup via internal defers. Copying the healthcheck shape
// (os.Exit at the dispatch site, main.go:189-191) would skip db.Close() and
// the pgx pool drain.
//
// Exit codes: 0 success (including permanent holes), 1 a gap failed or a
// runtime error, 2 a usage error.
func runBackfill(ctx context.Context, args []string) int {
	cfg, storeFlag, err := parseBackfillFlags(args)
	if err != nil {
		// --help is NOT a usage error. flag prints the usage itself and
		// returns ErrHelp; exiting 2 here would break every CI smoke test
		// that runs `cmd --help`.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		// parseBackfillFlags owns the FlagSet's output and has already
		// reported the error, so nothing is printed here — printing again
		// would duplicate flag's own message.
		return 2
	}

	// TOKEN is validated BEFORE any store handle is opened, so the failure
	// costs no I/O and leaves nothing to close.
	token := os.Getenv("TOKEN")
	if token == "" {
		slog.Error("backfill: TOKEN is required to reach the Tempest REST API")
		return 1
	}

	// Signal wiring lives in the subcommand — the healthcheck path has none
	// to inherit.
	ctx, stop := signalContext(ctx, signal.NotifyContext)
	defer stop()

	enablePostgres, err := config.ParseBoolEnv("ENABLE_POSTGRES")
	if err != nil {
		slog.Error("backfill: bad ENABLE_POSTGRES", "error", err)
		return 1
	}
	choice := selectStore(enablePostgres, os.Getenv("SQLITE_PATH"))

	// Backfill repairs ONE store per run, and must never guess which. The
	// daemon fans out to both when ENABLE_POSTGRES and SQLITE_PATH are both
	// set (main.go:147-154, :302-336); silently repairing Postgres while
	// leaving the SQLite database — the one Litestream replicates to S3 —
	// still holed, then reporting success, is the worst available outcome.
	target, err := resolveStore(choice, storeFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	store, cleanupStore, err := openBackfillStore(ctx, target, choice)
	if err != nil {
		// openBackfillStore has already logged the specific cause via
		// slog.Error before returning.
		return 1
	}
	defer cleanupStore()
	slog.Info("backfill: store selected", "store", target)

	// The narrow seam, not *tempestapi.Client: ListStations is unreachable
	// from here, so the identical-signature swap that would drop a sensor is
	// a compile error rather than something a test has to notice.
	api := newBackfillAPI(token)
	devices, err := api.ListDevices(ctx)
	if err != nil {
		slog.Error("backfill: list devices", "error", err)
		return 1
	}
	if len(devices) == 0 {
		slog.Error("backfill: the API reported no ST devices for this token")
		return 1
	}
	slog.Info("backfill: devices discovered", "count", len(devices))

	stats, err := backfill.Run(ctx, cfg, api, store, devices, time.Now().UTC())
	slog.Info("backfill: complete",
		"gaps", stats.Gaps, "returned", stats.Returned,
		"inserted", stats.Inserted, "failed", stats.Failed, "dry_run", cfg.DryRun)
	if err != nil {
		slog.Error("backfill: finished with failures", "error", err)
		return 1
	}
	return 0
}
