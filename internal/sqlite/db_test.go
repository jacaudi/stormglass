package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpen_SetsPragmas(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := Config{BusyTimeout: 5000 * time.Millisecond}

	db, err := Open(ctx, dbPath, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	assertJournalMode(t, db, "wal")
	assertPragmaInt(t, db, "busy_timeout", 5000)
	assertPragmaInt(t, db, "synchronous", 1) // NORMAL
	assertPragmaInt(t, db, "foreign_keys", 1)
	// Litestream owns checkpointing (design §10): must NOT override
	// wal_autocheckpoint down to something aggressive. The SQLite default is
	// 1000 pages.
	assertPragmaInt(t, db, "wal_autocheckpoint", 1000)
}

func assertJournalMode(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&got); err != nil {
		t.Fatalf("query PRAGMA journal_mode: %v", err)
	}
	if got != want {
		t.Errorf("PRAGMA journal_mode = %q, want %q", got, want)
	}
}

func assertPragmaInt(t *testing.T, db *sql.DB, pragma string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`PRAGMA ` + pragma).Scan(&got); err != nil {
		t.Fatalf("query PRAGMA %s: %v", pragma, err)
	}
	if got != want {
		t.Errorf("PRAGMA %s = %d, want %d", pragma, got, want)
	}
}

func TestOpenReadOnly_ReadsWriterData(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	cfg := Config{BusyTimeout: 5000 * time.Millisecond, BatchSize: 1, FlushInterval: time.Second}

	wdb, err := Open(ctx, path, cfg) // runs migrations, creates WAL
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := wdb.Close(); err != nil {
			t.Errorf("close write handle: %v", err)
		}
	})

	// seed one observation row directly
	_, err = wdb.ExecContext(ctx, `INSERT INTO stormglass_observations
		(id, serial_number, timestamp, temp_air) VALUES ('a','ST-1',100,21.5)`)
	if err != nil {
		t.Fatal(err)
	}

	rdb, err := OpenReadOnly(ctx, path, cfg)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Errorf("close read handle: %v", err)
		}
	})

	var got float64
	if err := rdb.QueryRowContext(ctx, `SELECT temp_air FROM stormglass_observations WHERE id='a'`).Scan(&got); err != nil {
		t.Fatalf("read via read-only handle: %v", err)
	}
	if got != 21.5 {
		t.Fatalf("got %v want 21.5", got)
	}
}

func TestLoadConfig(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"SQLITE_BATCH_SIZE":     "7",
			"SQLITE_FLUSH_INTERVAL": "2s",
			"SQLITE_BUSY_TIMEOUT":   "1500",
		}[k]
	}
	cfg := LoadConfig(getenv)
	if cfg.BatchSize != 7 || cfg.FlushInterval != 2*time.Second || cfg.BusyTimeout != 1500*time.Millisecond {
		t.Fatalf("overrides not applied: %+v", cfg)
	}

	def := LoadConfig(func(string) string { return "" })
	if def.BatchSize != 100 || def.FlushInterval != 10*time.Second || def.BusyTimeout != 5000*time.Millisecond {
		t.Fatalf("defaults wrong: %+v", def)
	}

	bad := func(k string) string {
		return map[string]string{
			"SQLITE_BATCH_SIZE":     "0",
			"SQLITE_FLUSH_INTERVAL": "abc",
			"SQLITE_BUSY_TIMEOUT":   "-1",
		}[k]
	}
	badCfg := LoadConfig(bad)
	if badCfg.BatchSize != 100 || badCfg.FlushInterval != 10*time.Second || badCfg.BusyTimeout != 5000*time.Millisecond {
		t.Fatalf("non-positive/unparseable values should fall back to defaults: %+v", badCfg)
	}
}
