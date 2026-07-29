package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolConfig is the single authoritative representation of this application's
// pgx pool tuning. Both NewPostgresWriter (the long-running daemon writer) and
// OpenPool (one-shot tools such as backfill) build from it, so a change to any
// value applies to both — which is exactly the shared-knowledge test that
// justifies extracting it rather than copying five lines.
func poolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 10 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	return config, nil
}

// OpenPool opens and verifies a connection pool.
//
// It starts no goroutines and does NOT create the schema — unlike
// NewPostgresWriter, which additionally runs CreateSchema and launches four
// background batch workers. A one-shot tool needs the pool without any of
// that, and calls CreateSchema explicitly so schema creation stays an
// observable step of the caller rather than a side effect of opening a pool.
//
// The caller owns the returned pool and must Close it.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := poolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
