package postgres

import (
	"strings"
	"testing"
	"time"
)

// OpenPool must reject a malformed URL before it attempts any connection, so
// a bad POSTGRES_URL fails fast with a useful message rather than hanging.
func TestOpenPoolRejectsBadURL(t *testing.T) {
	pool, err := OpenPool(t.Context(), "://not-a-url")
	if err == nil {
		pool.Close()
		t.Fatal("OpenPool accepted a malformed URL")
	}
	if !strings.Contains(err.Error(), "parse database url") {
		t.Errorf("error = %q, want it to mention parse database url", err)
	}
}

// The pool tuning is a shared contract between OpenPool and
// NewPostgresWriter. This pins the values so a change has to be deliberate.
func TestPoolConfigValues(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	if cfg.MaxConns != 10 {
		t.Errorf("MaxConns = %d, want 10", cfg.MaxConns)
	}
	if cfg.MinConns != 2 {
		t.Errorf("MinConns = %d, want 2", cfg.MinConns)
	}
	if cfg.MaxConnLifetime != time.Hour {
		t.Errorf("MaxConnLifetime = %v, want 1h", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 10*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 10m", cfg.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != 30*time.Second {
		t.Errorf("HealthCheckPeriod = %v, want 30s", cfg.HealthCheckPeriod)
	}
}
