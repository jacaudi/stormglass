//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requirePostgresURL returns the POSTGRES_URL used to reach a live database
// for integration tests, skipping the test if it isn't set.
func requirePostgresURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL not set, skipping integration test")
	}
	return dsn
}

// TestPostgresWriter_DrainOnClose_Integration is the real-DB counterpart to
// TestPostgresWriter_DrainOnClose: it exercises the actual connection pool
// (not the fake obsInserter) to prove buffered observation rows are
// persisted by Close(ctx) even when the writer's own ctx has already been
// canceled, matching the real SIGTERM shutdown sequence (C-H1).
//
// Run with a live Postgres instance:
//
//	POSTGRES_URL=postgres://user:pass@localhost:5432/weather?sslmode=disable \
//	  go test -tags integration ./internal/postgres/ -run DrainOnClose_Integration
func TestPostgresWriter_DrainOnClose_Integration(t *testing.T) {
	dsn := requirePostgresURL(t)

	// A separate, cancelable context stands in for the shared context
	// SIGTERM cancels in production; canceling it before Close mirrors the
	// real shutdown ordering (signal fires -> shared ctx canceled -> Close
	// runs with its own, still-live cleanup ctx).
	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	w, err := NewPostgresWriter(workerCtx, dsn)
	if err != nil {
		t.Fatalf("NewPostgresWriter: %v", err)
	}

	serial := "INTEGRATION-DRAIN-" + uuid.Must(uuid.NewV7()).String()
	const wantRows = 50
	base := time.Now().Add(-time.Hour)
	for i := range wantRows {
		w.obsBatch <- observationRow{
			id:           uuid.Must(uuid.NewV7()),
			serialNumber: serial,
			timestamp:    base.Add(time.Duration(i) * time.Second),
		}
	}

	cancelWorkerCtx()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClose()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	verifyPool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect for verification: %v", err)
	}
	defer verifyPool.Close()

	var got int
	err = verifyPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM stormglass_observations WHERE serial_number = $1`, serial).Scan(&got)
	if err != nil {
		t.Fatalf("verify persisted count: %v", err)
	}
	if got != wantRows {
		t.Fatalf("expected %d rows persisted by Close, got %d", wantRows, got)
	}
}
