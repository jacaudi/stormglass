// Package sqlite provides the embedded schema and migration path for the
// SQLite-backed weather data store.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

// Migrate applies any unapplied schema migrations to db, recording the
// applied version in the schema_version table. It is idempotent: calling it
// again once all migrations are applied is a no-op.
func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	current, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("determine current schema version: %w", err)
	}

	type migration struct {
		name    string
		version int
	}
	migrations := make([]migration, 0, len(entries))
	highest := 0
	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return fmt.Errorf("parse migration version from %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{name: entry.Name(), version: version})
		if version > highest {
			highest = version
		}
	}

	// A database newer than this binary must fail loudly. The loop below
	// skips any migration whose version is <= current, so an older binary
	// pointed at a newer database would apply nothing and report success --
	// and would go on silently skipping every future migration numbered at
	// or below the version the database already records.
	if current > highest {
		return fmt.Errorf(
			"database schema version %d exceeds the highest migration bundled in this binary (%d): "+
				"the database was written by a newer build, so this one cannot safely open it — "+
				"upgrade the binary rather than downgrading the database",
			current, highest)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}

		content, err := migrationsFS.ReadFile(migrationsDir + "/" + m.name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", m.name, err)
		}

		if err := applyMigration(ctx, db, m.version, string(content)); err != nil {
			return fmt.Errorf("apply migration %q: %w", m.name, err)
		}
	}

	return nil
}

// currentSchemaVersion returns the highest applied migration version, or 0
// if the schema_version table does not exist yet (i.e. no migration has ever
// been applied).
func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("check for schema_version table: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}

	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return int(version.Int64), nil
}

// applyMigration executes every statement in content inside a single
// transaction, then records version in schema_version.
func applyMigration(ctx context.Context, db *sql.DB, version int, content string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, stmt := range splitStatements(content) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("execute statement: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		return fmt.Errorf("record schema version %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// splitStatements splits a migration file's SQL content into individual
// statements on ";". This DDL contains no semicolons inside string literals
// or comments, so a literal split is sufficient.
func splitStatements(content string) []string {
	var stmts []string
	for raw := range strings.SplitSeq(content, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		stmts = append(stmts, stmt)
	}
	return stmts
}

// migrationVersion parses the numeric version prefix from a migration
// filename (e.g. "0002_init.sql" -> 2). Version prefixes must be
// zero-padded consistently so that lexicographic filename ordering (used by
// fs.ReadDir) matches numeric version ordering.
func migrationVersion(filename string) (int, error) {
	prefix, _, ok := strings.Cut(filename, "_")
	if !ok {
		return 0, fmt.Errorf("filename %q missing version prefix", filename)
	}
	return strconv.Atoi(prefix)
}
