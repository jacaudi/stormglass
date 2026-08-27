package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrate_CreatesTablesAndVersion(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	wantTables := []string{
		"stormglass_observations",
		"stormglass_rapid_wind",
		"stormglass_hub_status",
		"stormglass_events",
	}
	for _, table := range wantTables {
		assertTableExists(t, db, table)
	}
	assertIndexExists(t, db, "idx_obs_serial_time")
	// idx_obs_time leads with timestamp alone
	// so the read hot-path (LatestObservationAny's ORDER BY timestamp DESC
	// LIMIT 1 and HistoryPoints' WHERE timestamp BETWEEN ?) can use an index
	// instead of a full table scan + sort -- idx_obs_serial_time can't serve
	// either query since it leads with serial_number (SGE review I1).
	assertIndexExists(t, db, "idx_obs_time")
	assertSchemaVersion(t, db, 2)

	// Idempotent: running Migrate again must not fail and must leave the
	// schema at the same version.
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	assertSchemaVersion(t, db, 2)
}

func TestMigrateDeclaresMeasurementColumnsAsREAL(t *testing.T) {
	ctx := t.Context()
	db := newTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	// precip_type is deliberately excluded: it is a categorical enum
	// (0 none, 1 rain, 2 hail, 3 rain+hail), not a measurement, and stays
	// INTEGER so it matches internal/postgres/schema.go:55.
	want := map[string]string{
		"wind_sample_interval":   "REAL",
		"lightning_strike_count": "REAL",
		"report_interval":        "REAL",
		"precip_type":            "INTEGER",
	}
	for column, wantType := range want {
		var got string
		err := db.QueryRowContext(ctx,
			`SELECT type FROM pragma_table_info('stormglass_observations') WHERE name = ?`,
			column,
		).Scan(&got)
		if err != nil {
			t.Fatalf("pragma_table_info for %q: %v", column, err)
		}
		if got != wantType {
			t.Errorf("%s declared %s, want %s", column, got, wantType)
		}
	}
}

func TestMigrateRejectsDatabaseNewerThanBundledMigrations(t *testing.T) {
	ctx := t.Context()
	db := newTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}

	// Simulate a database written by a NEWER binary. Migrate skips any
	// migration whose version is <= current, so without a guard this would
	// silently apply nothing and report success -- and would keep skipping
	// every future migration numbered at or below 99.
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (99)`); err != nil {
		t.Fatalf("seed future schema version: %v", err)
	}

	err := Migrate(ctx, db)
	if err == nil {
		t.Fatal("Migrate() = nil, want an error for a database newer than the bundled migrations")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error %q does not name the database's version (99)", err)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master for table %q: %v", name, err)
	}
	if count != 1 {
		t.Errorf("table %q missing after Migrate()", name)
	}
}

func assertIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master for index %q: %v", name, err)
	}
	if count != 1 {
		t.Errorf("index %q missing after Migrate()", name)
	}
}

func assertSchemaVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&got)
	if err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if got != want {
		t.Errorf("schema_version = %d, want %d", got, want)
	}
}
