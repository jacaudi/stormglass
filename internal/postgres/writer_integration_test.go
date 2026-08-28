//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jacaudi/stormglass/internal/tempestudp"
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

// TestPostgresWriter_DeviceStatus_Integration covers #196's Postgres half
// against a live database: the report the writer used to drop becomes a row,
// absent radio/firmware persist as NULL (never 0), and a report queued
// immediately before Close survives the drain.
//
// The drain half matters disproportionately here: device_status adds a FIFTH
// batch goroutine, and #111 / #154 / C-H1 / D-H1 are all shutdown-drain
// data-loss fixes in this exact file.
func TestPostgresWriter_DeviceStatus_Integration(t *testing.T) {
	dsn := requirePostgresURL(t)
	ctx := t.Context()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	serial := "ITEST-DEV-" + uuid.Must(uuid.NewV7()).String()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM stormglass_device_status WHERE serial_number = $1`, serial)
	})

	workerCtx, cancelWorkerCtx := context.WithCancel(context.Background())
	w, err := NewPostgresWriter(workerCtx, dsn)
	if err != nil {
		t.Fatalf("NewPostgresWriter: %v", err)
	}

	fw, rssi := 156, -82
	full := &tempestudp.DeviceStatusReport{
		SerialNumber: serial, Timestamp: 1700001000, Uptime: 63807156, Voltage: 2.792,
		FirmwareRevision: &fw, Rssi: &rssi, HubRssi: -78, SensorStatus: 0,
	}
	absent := &tempestudp.DeviceStatusReport{
		SerialNumber: serial, Timestamp: 1700002000, Voltage: 2.7,
		// FirmwareRevision and Rssi deliberately nil.
	}
	for _, r := range []*tempestudp.DeviceStatusReport{full, absent} {
		if err := w.WriteReport(ctx, r); err != nil {
			t.Fatalf("WriteReport: %v", err)
		}
	}

	// Mirror production shutdown ordering: worker ctx dies first, Close runs
	// with its own live ctx. Nothing was flushed by size or ticker, so every
	// row here is persisted by the drain or not at all.
	cancelWorkerCtx()
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClose()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM stormglass_device_status WHERE serial_number = $1`, serial).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("persisted %d rows, want 2 -- the drain dropped device_status", n)
	}

	var (
		gotFirmware *string
		gotRssi     *int
		gotHubRssi  int
	)
	if err := pool.QueryRow(context.Background(),
		`SELECT firmware_revision, rssi, hub_rssi FROM stormglass_device_status
		   WHERE serial_number = $1 AND timestamp = to_timestamp(1700001000)`, serial,
	).Scan(&gotFirmware, &gotRssi, &gotHubRssi); err != nil {
		t.Fatalf("query full row: %v", err)
	}
	if gotFirmware == nil || *gotFirmware != "156" {
		t.Errorf("firmware_revision = %v, want \"156\"", gotFirmware)
	}
	if gotRssi == nil || *gotRssi != -82 {
		t.Errorf("rssi = %v, want -82", gotRssi)
	}
	if gotHubRssi != -78 {
		t.Errorf("hub_rssi = %d, want -78", gotHubRssi)
	}

	if err := pool.QueryRow(context.Background(),
		`SELECT firmware_revision, rssi FROM stormglass_device_status
		   WHERE serial_number = $1 AND timestamp = to_timestamp(1700002000)`, serial,
	).Scan(&gotFirmware, &gotRssi); err != nil {
		t.Fatalf("query absent-fields row: %v", err)
	}
	if gotFirmware != nil {
		t.Errorf("firmware_revision = %q, want NULL -- absent must not become a reading", *gotFirmware)
	}
	if gotRssi != nil {
		t.Errorf("rssi = %d, want NULL -- absent must not become a reading", *gotRssi)
	}
}
