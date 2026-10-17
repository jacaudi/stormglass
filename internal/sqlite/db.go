package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jacaudi/stormglass/internal/config"

	// Register the pure-Go SQLite driver (CGO-free) under the name "sqlite".
	// This blank import MUST live in production code, not only in _test.go
	// files: without it the compiled binary panics at runtime with
	// `sql: unknown driver "sqlite"` even though tests pass (test files can
	// register it themselves). Do not remove.
	_ "modernc.org/sqlite"
)

const driverName = "sqlite" // modernc.org/sqlite registers this name

// Config carries the tunable knobs for the SQLite store, loaded from
// environment variables (see LoadConfig). BatchSize and FlushInterval are
// consumed by the writer (Task 3.3); Open itself only needs BusyTimeout.
type Config struct {
	BatchSize     int
	FlushInterval time.Duration
	BusyTimeout   time.Duration
}

// LoadConfig reads SQLITE_BATCH_SIZE, SQLITE_FLUSH_INTERVAL, and
// SQLITE_BUSY_TIMEOUT (milliseconds) via getenv. Unset or empty takes the
// default (100, 10s, 5000ms); a value that does not parse or is not positive
// is an error, and every offender is joined into the one returned. getenv is
// injected so callers can test without touching the real process environment.
func LoadConfig(getenv func(string) string) (Config, error) {
	batch, batchErr := config.PositiveIntEnv(getenv, "SQLITE_BATCH_SIZE", 100)
	flush, flushErr := config.PositiveDurationEnv(getenv, "SQLITE_FLUSH_INTERVAL", 10*time.Second)
	busyMS, busyErr := config.PositiveIntEnv(getenv, "SQLITE_BUSY_TIMEOUT", 5000)
	return Config{
		BatchSize:     batch,
		FlushInterval: flush,
		BusyTimeout:   time.Duration(busyMS) * time.Millisecond,
	}, errors.Join(batchErr, flushErr, busyErr)
}

// Open establishes a connection to the SQLite database at path with the
// exact PRAGMAs required by design §10: WAL journaling, a busy timeout,
// synchronous=NORMAL, and foreign key enforcement. It deliberately does NOT
// set wal_autocheckpoint — Litestream owns checkpointing, and overriding it
// here would silently break replication (design §10's top risk). Open pings
// the connection and applies any pending schema migrations before returning.
func Open(ctx context.Context, path string, cfg Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)",
		path, cfg.BusyTimeout.Milliseconds(),
	)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer — serializes writes, avoids SQLITE_BUSY (design §10)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenReadOnly opens a read-only handle to an existing SQLite database for
// query-side work (e.g. HTTP API reads). It must be opened AFTER Open, which
// creates/migrates the database and its WAL files. WAL journaling lets these
// read connections run concurrently with the single ingest writer without
// SQLITE_BUSY. It deliberately does NOT set journal_mode — a read-only
// connection cannot run that write PRAGMA, and the database is already WAL
// from Open.
func OpenReadOnly(ctx context.Context, path string, cfg Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?mode=ro&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)",
		path, cfg.BusyTimeout.Milliseconds(),
	)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite read-only: %w", err)
	}
	db.SetMaxOpenConns(4) // concurrent readers alongside the single writer (WAL)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite read-only: %w", err)
	}
	return db, nil
}
