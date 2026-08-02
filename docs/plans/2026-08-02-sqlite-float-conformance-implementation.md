# SQLite Float Conformance — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop three measurement columns being silently truncated to integers on the SQLite write
path, so SQLite and Postgres hold the same value for the same observation, and make the SQLite DDL
declare the type it actually stores.

**Architecture:** SQLite's `INTEGER` declaration was never the lossy part — Go was. Both write paths
convert `*float64` to `*int64` before binding. The fix removes those conversions, widens the read
model and the JSON contract to `float64`, and changes the three columns to `REAL`. Postgres already
stores these as `DOUBLE PRECISION` with `*float64` end to end and needs no schema change.

**Tech Stack:** Go 1.25 (toolchain go1.26.1), `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`),
`pgx/v5`, GitHub Actions.

**Design:** `docs/designs/2026-08-02-sqlite-float-conformance-design.md`

> **For Claude:** REQUIRED EXECUTION WORKFLOW (follow in order):
> 1. `superpowers:using-git-worktrees` — **already satisfied.** Work happens in the existing worktree
>    `/Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance`
>    on branch `worktree-schema-conformance`. Do **not** create another worktree.
> 2. `superpowers:subagent-driven-development` — fresh subagent per task
> 3. `superpowers:test-driven-development` — mandatory for every task
> 4. `superpowers:verification-before-completion` — verify per task
> 5. `superpowers:requesting-code-review` — review after each task (built in)
> 6. After all tasks: whole-diff review with `sr-eng-review`
> 7. `superpowers:finishing-a-development-branch`
>
> Skills carry their own model and effort settings. Do not override them.

## Global Constraints

- **Work only in** `/Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance`.
  It is a git worktree. Do **not** `cd` to the parent repo at
  `/Users/acaudill/Projects/github/tempestwx-exporter`. The shell's cwd resets between tool calls —
  prefix every command with `cd <worktree>` or use absolute paths.
- Every task brief must confirm `docs/designs/2026-08-02-sqlite-float-conformance-design.md` exists;
  if it does not, STOP and report `NEEDS_CONTEXT`.
- **Never bypass commit signing.** 1Password's SSH agent intermittently drops keys, producing
  `1Password: failed to fill whole buffer` / `fatal: failed to write commit object`. Retry the same
  `git commit` two or three times. **Never** use `--no-gpg-sign`.
- **Never use bare `git stash` / `git stash pop`** — the stash stack is shared across worktrees.
  Use a WIP commit instead.
- **Do not push, open a PR, or merge.** The human does that. Committing locally is expected.
- **`CGO_ENABLED=0` is load-bearing.** Never add a cgo dependency.
- **`precip_type` stays `INTEGER` in both stores and keeps truncating.** It is a categorical enum
  (`0 none, 1 rain, 2 hail, 3 rain+hail`), not a measurement. Any change that makes `precip_type`
  a float is wrong.
- **`rain_rate` is the precipitation amount and is already `REAL`/`DOUBLE PRECISION`.** Do not touch it.
- The branch point is `a1b07f1`. For branch-wide diffs use `a1b07f1..HEAD`, **never** `main...HEAD` —
  local `main` is stale at `8990b88` and reports 27 unrelated `.go` files.
- `timeout` does not exist on macOS (it is `gtimeout`). Use the tool's own timeout.
- The full gate is `go build ./...` (with `CGO_ENABLED=0`), `go vet ./...`, `gofmt -l .`,
  `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...`, `go test -race ./...`.
- **Known conflict:** PR #117 (`worktree-release-binaries`) also modifies
  `.github/workflows/on-push-main.yml` and `on-release.yml`. It removes `latest:` / `tag-strategy:`
  from the `docker` step; Task 4 adds a `services:` block to the `tests` job. Different jobs, so a
  clean auto-merge is expected — but if #117 merges first, rebase before Task 4 and do **not**
  re-add the lines it deleted.

## Background an implementer needs

Measured facts this plan depends on. Do not re-derive them; do not contradict them.

- SQLite `INTEGER` affinity does **not** truncate a fractional bind — it stores `1.5` as `REAL`.
- `database/sql` converts `float64` → `int64` via `strconv.FormatFloat(v, 'g', -1, 64)`, which
  switches to scientific notation at exponent ≥ 6. So a `sql.NullInt64` scan of a `REAL`-stored
  value **succeeds** below 1e6 (`5` → `5`) and **fails** at/above it (`1e+06` → `invalid syntax`).
- Therefore an integral value still scans into `NullInt64` from a `REAL` column. **Several existing
  tests keep passing after Task 1 for exactly this reason — that is expected, not a bug.**
- `SUM(...)` over a `REAL` column returns `REAL`, but `SUM(2,3)` = `5.0` still scans into
  `NullInt64` as `5`. Only a fractional sum (`5.5`) errors.
- Go marshals `float64(5)` as `5`, not `5.0`, so widening a JSON field from `int64` to `float64` is
  byte-identical on the wire for every integral value.

## File Structure

| File | Task | Change |
|---|---|---|
| `internal/sqlite/migrations/0001_init.sql` | 1 | three columns → `REAL`; fold in `idx_obs_time` |
| `internal/sqlite/migrations/0002_add_timestamp_index.sql` | 1 | delete |
| `internal/sqlite/schema.go` | 1 | fail-loud guard for a database newer than the bundled migrations |
| `internal/sqlite/schema_test.go` | 1 | version `2` → `1`; new column-type and guard tests |
| `internal/sqlite/writer.go` | 2, 3 | read model (2), write model (3) |
| `internal/sqlite/writer_test.go` | 2 | scan types |
| `internal/sqlite/litestream_test.go` | 2 | scan types |
| `internal/sqlite/summary_test.go` | 2 | `LightningTotal` type; new fractional-`SUM` test |
| `internal/httpserver/observations.go` | 2 | JSON contract |
| `internal/httpserver/observations_test.go` | 2 | fixture types |
| `internal/sqlite/backfill.go` | 3 | delete `asInt64`; `precip_type`-only helper |
| `internal/sqlite/backfill_test.go` | 3 | rework the truncation test |
| `.github/workflows/on-pull-request.yml` | 4 | Postgres service container |
| `.github/workflows/on-push-main.yml` | 4 | Postgres service container |
| `.github/workflows/on-release.yml` | 4 | Postgres service container |
| `.github/actions/tests/action.yml` | 4 | `POSTGRES_URL` pass-through |
| `internal/postgres/backfill.go` | 5 | explicit `precip_type` conversion |
| `internal/postgres/backfill_test.go` | 5 | fractional round-trip |

**Task order is load-bearing.** The read path must widen (Task 2) *before* the write path stops
truncating (Task 3). Reversed, a fractional value lands in the database while the read model is
still `NullInt64`, and the current-observation endpoint breaks between commits.

---

### Task 1: SQLite schema — fold `0002` into `0001`, widen three columns, fail loud on a newer database

**Files:**
- Modify: `internal/sqlite/migrations/0001_init.sql`
- Delete: `internal/sqlite/migrations/0002_add_timestamp_index.sql`
- Modify: `internal/sqlite/schema.go` (`Migrate`, `:23-54`)
- Modify: `internal/sqlite/schema_test.go` (`:37-50`)

**Interfaces:**
- Consumes: nothing.
- Produces: a schema where `wind_sample_interval`, `lightning_strike_count`, and `report_interval`
  are `REAL`; `schema_version` reaches `1`; `Migrate` returns a non-nil error when the database's
  version exceeds the highest bundled migration.

**Expected end state of this task:** the whole suite still passes. Existing tests scan integral
values, which still read back through `sql.NullInt64` from a `REAL` column. Do not "fix" them here.

- [ ] **Step 1: Write the failing tests**

Add to `internal/sqlite/schema_test.go`:

```go
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
			`SELECT type FROM pragma_table_info('tempest_observations') WHERE name = ?`,
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
```

Add `"strings"` to that file's imports.

- [ ] **Step 2: Run the tests and watch them fail**

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance
go test ./internal/sqlite/ -run 'TestMigrateDeclaresMeasurementColumnsAsREAL|TestMigrateRejectsDatabaseNewerThanBundledMigrations' -v 2>&1 | tail -30
```

Expected: `TestMigrateDeclaresMeasurementColumnsAsREAL` FAILs with
`wind_sample_interval declared INTEGER, want REAL` (and the same for the other two).
`TestMigrateRejectsDatabaseNewerThanBundledMigrations` FAILs with
`Migrate() = nil, want an error…`.

If either passes at this point, stop and report — the change is already applied.

- [ ] **Step 3: Rewrite `internal/sqlite/migrations/0001_init.sql`**

Replace the file's **complete** contents with:

```sql
-- All timestamps stored as INTEGER unix-epoch seconds (UTC). UUIDv7 text PKs generated in Go.
CREATE TABLE IF NOT EXISTS tempest_observations (
  id TEXT PRIMARY KEY,                 -- UUIDv7
  serial_number TEXT NOT NULL,
  timestamp INTEGER NOT NULL,          -- ob[0] epoch
  wind_lull REAL, wind_avg REAL, wind_gust REAL, wind_direction REAL,
  wind_sample_interval REAL,
  pressure REAL, temp_air REAL, temp_wetbulb REAL, humidity REAL,
  illuminance REAL, uv_index REAL, irradiance REAL, rain_rate REAL,
  precip_type INTEGER,                 -- categorical enum (0 none, 1 rain, 2 hail, 3 rain+hail), not a measurement
  lightning_distance REAL, lightning_strike_count REAL,
  battery REAL, report_interval REAL,
  UNIQUE(serial_number, timestamp)     -- backs ON CONFLICT DO NOTHING
);
CREATE TABLE IF NOT EXISTS tempest_rapid_wind (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  wind_speed REAL, wind_direction REAL, UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_hub_status (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  uptime INTEGER, rssi REAL, reboot_count INTEGER, bus_errors INTEGER,
  UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_events (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  event_type TEXT NOT NULL, distance_km REAL, energy REAL,
  UNIQUE(serial_number, timestamp, event_type)
);
CREATE INDEX IF NOT EXISTS idx_obs_serial_time ON tempest_observations(serial_number, timestamp);
-- The read hot-path (LatestObservationAny's ORDER BY timestamp DESC LIMIT 1,
-- HistoryPoints' WHERE timestamp BETWEEN ?) filters/sorts on timestamp alone.
-- idx_obs_serial_time leads with serial_number, so neither query can use it and
-- both fall back to a full table scan + sort, serialized against the single
-- writer connection (SGE review I1). This index leads with timestamp so both
-- queries can use it directly.
CREATE INDEX IF NOT EXISTS idx_obs_time ON tempest_observations(timestamp);
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
```

Three changes from the original: `wind_sample_interval`, `lightning_strike_count`, and
`report_interval` are now `REAL`; the `-- INTEGER not float (fix B-LOW)` comment is gone;
`idx_obs_time` and its rationale are folded in from `0002`. Everything else is byte-identical.

- [ ] **Step 4: Delete the folded migration**

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance
git rm internal/sqlite/migrations/0002_add_timestamp_index.sql
ls internal/sqlite/migrations/
```

Expected: `rm 'internal/sqlite/migrations/0002_add_timestamp_index.sql'`, then only
`0001_init.sql` listed.

- [ ] **Step 5: Add the fail-loud guard to `internal/sqlite/schema.go`**

Replace the body of `Migrate` (`:23-54`) with:

```go
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
			"database schema version %d is newer than the highest bundled migration (%d): "+
				"this binary is older than the database it was pointed at",
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
```

- [ ] **Step 6: Update the version assertions in `internal/sqlite/schema_test.go`**

Two sites, `:43` and `:50`, both currently `assertSchemaVersion(t, db, 2)`. Change both to:

```go
	assertSchemaVersion(t, db, 1)
```

Also update the comment at `:37-41`, which references the deleted file. Replace:

```go
	// idx_obs_time (0002_add_timestamp_index.sql) leads with timestamp alone
```

with:

```go
	// idx_obs_time leads with timestamp alone
```

Leave the rest of that comment and the `assertIndexExists(t, db, "idx_obs_time")` call untouched —
that assertion is what proves the fold did not drop the index.

- [ ] **Step 7: Run the tests and watch them pass**

```bash
go test ./internal/sqlite/ -run 'TestMigrate' -v 2>&1 | tail -30
```

Expected: PASS for both new tests and the existing migration tests.

- [ ] **Step 8: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test -race ./... 2>&1 | tail -20
```

Expected: `gofmt -l .` prints nothing; everything passes. **Every existing test still passes** —
integral values scan into `sql.NullInt64` from a `REAL` column without error. That is expected.

- [ ] **Step 9: Commit**

```bash
git add internal/sqlite/migrations internal/sqlite/schema.go internal/sqlite/schema_test.go
git status --porcelain
git commit -m "fix(sqlite): declare the three measurement columns REAL; fold 0002 into 0001

wind_sample_interval, lightning_strike_count, and report_interval were
declared INTEGER in SQLite but DOUBLE PRECISION in Postgres. The DDL was
never the lossy part -- INTEGER affinity stores 1.5 as REAL rather than
truncating -- but the declaration should say what the column holds.

precip_type stays INTEGER in both stores: it is a categorical enum, not
a measurement.

0002_add_timestamp_index.sql folds into 0001 rather than a 0003 rebuild,
because nothing is deployed and SQLite cannot ALTER COLUMN TYPE. Its
rationale comment moves with it so idx_obs_time is not later deleted as
redundant.

Migrate now fails loudly when the database's schema version exceeds the
highest bundled migration. Without it, an older binary meeting a newer
database applies nothing and reports success, then silently skips every
future migration numbered at or below the recorded version.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

`git status --porcelain` must show exactly four lines, all with the status code in **column 1**:

```
M  internal/sqlite/migrations/0001_init.sql
D  internal/sqlite/migrations/0002_add_timestamp_index.sql
M  internal/sqlite/schema.go
M  internal/sqlite/schema_test.go
```

---

### Task 2: Widen the read path — `Observation`, `scanObservation`, `Summary`, and the JSON contract

**Files:**
- Modify: `internal/sqlite/writer.go` (`:705-800`, `:930-950`)
- Modify: `internal/sqlite/writer_test.go` (`:74-100`)
- Modify: `internal/sqlite/litestream_test.go` (`:46-85`)
- Modify: `internal/sqlite/summary_test.go`
- Modify: `internal/httpserver/observations.go` (`:70`, `:80`, `:82`, `:130`, `:280`)
- Modify: `internal/httpserver/observations_test.go`

**Interfaces:**
- Consumes: Task 1's `REAL` columns.
- Produces: `sqlite.Observation.WindSampleInterval`, `.LightningStrikeCount`, `.ReportInterval` are
  `*float64`; `.PrecipType` stays `*int64`. `sqlite.Summary.LightningTotal` is `sql.NullFloat64`.
  `currentObservation.WindSampleInterval`, `.LightningStrikeCount`, `.ReportInterval` are `float64`;
  `.PrecipitationType` stays `int64`. `summaryResponse.LightningTotal` is `*float64`.

**Why this task comes before Task 3:** it makes reads tolerant of fractional values *before* writes
start producing them. Reversed, the current-observation endpoint breaks between commits.

- [ ] **Step 1: Write the failing test**

Add to `internal/sqlite/summary_test.go`. This is the one test that catches a missed
`LightningTotal` — the existing summary test seeds `2` and `3`, whose `REAL` sum is `5.0` and still
scans into `sql.NullInt64` as `5`, so it passes either way.

```go
func TestSummarizeObservationsPreservesFractionalLightningTotal(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	// A fractional sum is the only thing that distinguishes a NullFloat64
	// LightningTotal from a NullInt64 one: SUM(2,3) over a REAL column is
	// 5.0, which a NullInt64 scan still accepts. SUM(2.5,3) is 5.5, which
	// it rejects with "converting driver.Value type float64 to a int64".
	for i, v := range []float64{2.5, 3} {
		_, err := w.db.ExecContext(ctx, `
			INSERT INTO tempest_observations (id, serial_number, timestamp, lightning_strike_count)
			VALUES (?, ?, ?, ?)`,
			uuid.Must(uuid.NewV7()).String(), "ST-A", int64(100+i), v)
		if err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	s, err := w.SummarizeObservations(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("SummarizeObservations: %v", err)
	}
	if !s.LightningTotal.Valid {
		t.Fatal("LightningTotal is NULL, want 5.5")
	}
	if s.LightningTotal.Float64 != 5.5 {
		t.Errorf("LightningTotal = %v, want 5.5", s.LightningTotal.Float64)
	}
}
```

`newTestWriter` is defined in `internal/sqlite/writer_test.go:35` and is available to every test in
the package; it migrates the database and leaves the flush ticker dormant. `w.db` is the package's
own field, reachable from an in-package test. If `summary_test.go` does not already import
`github.com/google/uuid`, add it.

- [ ] **Step 2: Run it and watch it fail**

```bash
go test ./internal/sqlite/ -run TestSummarizeObservationsPreservesFractionalLightningTotal -v 2>&1 | tail -20
```

Expected: FAIL with a compile error on `s.LightningTotal.Float64` (the field is `sql.NullInt64`).
A compile failure is acceptable as the red state **only for this step**, because the type itself is
what the test is asserting. Confirm the message names `Float64` / `NullInt64`.

- [ ] **Step 3: Widen the read model in `internal/sqlite/writer.go`**

In `type Observation struct` (`:712-734`), change three fields — leave `PrecipType` alone:

```go
	WindSampleInterval   *float64
	...
	LightningStrikeCount *float64
	...
	ReportInterval       *float64
```

In `scanObservation` (`:757-800`), change the declaration block:

```go
	var (
		obs                                                          Observation
		precipType                                                   sql.NullInt64
		windSampleInterval, lightningStrikeCount, reportInterval     sql.NullFloat64
		tempWetbulb, lightningDistance, battery                      sql.NullFloat64
	)
```

and the three assignments:

```go
	if windSampleInterval.Valid {
		obs.WindSampleInterval = &windSampleInterval.Float64
	}
	...
	if lightningStrikeCount.Valid {
		obs.LightningStrikeCount = &lightningStrikeCount.Float64
	}
	...
	if reportInterval.Valid {
		obs.ReportInterval = &reportInterval.Float64
	}
```

`precipType` keeps `&precipType.Int64`. Run `gofmt -w internal/sqlite/writer.go` afterwards — the
`var` block alignment will change.

In `type Summary struct` (`:936-950`):

```go
	LightningTotal sql.NullFloat64
```

- [ ] **Step 4: Update the read model's doc comment**

`internal/sqlite/writer.go:87-95` describes `observationRow` as diverging from Postgres because the
INTEGER-typed columns are `*int64`. That is still true of `observationRow` until Task 3, so **leave
it alone in this task.** Task 3 owns it.

- [ ] **Step 5: Fix the scanning tests**

`internal/sqlite/writer_test.go:76,81,83` — these are a **silent survivor**: they declare the three
columns as plain `int64` and raw-scan them, and the seeded values `3,4,5` still scan fine from a
`REAL` column, so the test stays green while asserting the wrong type. Change:

```go
			windSampleInterval                         float64
			...
			lightningStrikeCount                       float64
			...
			reportInterval                             float64
```

Leave `precipType int64`. Adjust any `%d` verbs for those three to `%v`, and any integer literal
comparisons remain valid (`3 == 3.0`).

`internal/sqlite/litestream_test.go:46-50` — change the declaration block:

```go
		var (
			obs                                                      Observation
			precipType                                               sql.NullInt64
			windSampleInterval, lightningStrikeCount, reportInterval sql.NullFloat64
			tempWetbulb, lightningDistance, battery                  sql.NullFloat64
		)
```

and the corresponding `&x.Int64` assignments at `:63,75,81` to `&x.Float64` for those three only.

`internal/sqlite/summary_test.go:22,31,32,33` — the `seed` helper takes a `sql.NullInt64` lightning
argument. Change its parameter type to `sql.NullFloat64` and the three call sites to
`sql.NullFloat64{Float64: 2, Valid: true}`, `sql.NullFloat64{}`, and
`sql.NullFloat64{Float64: 3, Valid: true}`. At `:51-52` change `s.LightningTotal.Int64 != 5` to
`s.LightningTotal.Float64 != 5` and the `%d` verb to `%v`.

- [ ] **Step 6: Widen the JSON contract in `internal/httpserver/observations.go`**

Exactly four edits. **`deref` at `:342` needs no change — it is generic.** **`i64` at `:154` must be
kept** — it still serves `CoveredFrom`/`CoveredTo` at `:272-273`, which are `MIN/MAX(timestamp)`
over an `INTEGER` column.

`:70`, `:80`, `:82` — three fields in `currentObservation`. `PrecipitationType` stays `int64`:

```go
	WindSampleInterval         float64 `json:"windSampleInterval"`
	...
	LightningStrikeCount       float64 `json:"lightningStrikeCount"`
	...
	ReportInterval             float64 `json:"reportInterval"`
```

`:130` in `summaryResponse`:

```go
	LightningTotal *float64      `json:"lightningTotal"`
```

`:280` — `f64` already exists at `:145` and serves `TempMax`, `RainTotal`, and the rest. Reuse it:

```go
		LightningTotal: f64(s.LightningTotal),
```

`toCurrentObservation` at `:312,322,324` needs no edit — `deref` is generic and infers the new type.

- [ ] **Step 7: Fix the httpserver test fixtures**

`internal/httpserver/observations_test.go:70,72,82,92,94` construct `int64` locals and assign them
into `sqlite.Observation`. Change those three to `float64` — e.g. `lightningCount := 4.0`,
`reportInterval := 5.0`. `:325` has a `sql.NullInt64{...}` literal for `LightningTotal`; change it
to `sql.NullFloat64{Float64: <same value>, Valid: true}`.

Read the surrounding lines before editing: match whatever the existing values are rather than
assuming, and leave any `precipType` fixture as `int64`.

- [ ] **Step 8: Run the tests and watch them pass**

```bash
go test ./internal/sqlite/ ./internal/httpserver/ 2>&1 | tail -20
```

Expected: all pass, including the new fractional-`SUM` test.

- [ ] **Step 9: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./... && \
  go test -race ./... 2>&1 | tail -20
```

Expected: `gofmt -l .` prints nothing; lint clean; all tests pass.

- [ ] **Step 10: Commit**

```bash
git add internal/sqlite internal/httpserver
git status --porcelain
git commit -m "fix(sqlite): read the three measurement columns as float64

Widens the read model and the JSON contract ahead of the write-path
change, so a fractional value can be read back before one can be
written. Reversed, the current-observation endpoint would break between
commits.

Summary.LightningTotal is the site that fails silently: SUM over a REAL
column returns REAL, but SUM(2,3) = 5.0 still scans into NullInt64 as 5,
so every existing summary test passes either way. Only a fractional sum
errors -- hence the new test seeding 2.5.

writer_test.go was a silent survivor of the same kind: it declared the
columns as plain int64 and its integral fixtures kept scanning fine from
a REAL column, so it stayed green while asserting the wrong type.

The wire change is byte-identical for integral values -- Go marshals
float64(5) as 5, not 5.0. precip_type stays an integer end to end.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

---

### Task 3: Stop truncating on the write path

**Files:**
- Modify: `internal/sqlite/writer.go` (`:87-95`, `:96-118`, `:399-403`, `:436-457`)
- Modify: `internal/sqlite/backfill.go` (`:161-208`)
- Modify: `internal/sqlite/backfill_test.go` (`:350-410`, and the comments at `:241-275`)

**Interfaces:**
- Consumes: Task 2's float-tolerant read model.
- Produces: `observationRow.windSampleInterval`, `.lightningStrikeCount`, `.reportInterval` are
  `*float64`; `.precipType` stays `*int64`. `asInt64` is gone, replaced by a `precip_type`-only
  helper.

- [ ] **Step 1: Rework the truncation test into a fidelity test**

`internal/sqlite/backfill_test.go:350-410` currently asserts the truncation. Replace the whole
function **and its doc comment** with:

```go
// TestInsertObservationsPreservesFractionalMeasurements pins the cross-store
// conformance fix: wind_sample_interval, lightning_strike_count, and
// report_interval are REAL in SQLite and DOUBLE PRECISION in Postgres, so a
// fractional API value must survive the SQLite write path unchanged rather
// than being truncated in Go. precip_type is the deliberate exception -- a
// categorical enum (0 none, 1 rain, 2 hail, 3 rain+hail), INTEGER in both
// stores, where a fractional value is corrupt input and truncation is the
// intended coercion.
func TestInsertObservationsPreservesFractionalMeasurements(t *testing.T) {
	db := newTestDB(t)
	obs := []weather.Observation{
		{
			SerialNumber:         "ST-A",
			Timestamp:            ts(1000),
			WindSampleInterval:   f(1.5),
			PrecipType:           f(1.9),
			LightningStrikeCount: f(2.5),
			ReportInterval:       f(5.9),
		},
	}
	if _, err := InsertObservations(t.Context(), db, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var windSampleInterval, lightningStrikeCount, reportInterval sql.NullFloat64
	var precipType sql.NullInt64
	row := db.QueryRowContext(t.Context(), `
		SELECT wind_sample_interval, precip_type, lightning_strike_count, report_interval
		FROM tempest_observations WHERE serial_number = ? AND timestamp = ?`,
		"ST-A", ts(1000).Unix())
	if err := row.Scan(&windSampleInterval, &precipType, &lightningStrikeCount, &reportInterval); err != nil {
		t.Fatalf("scan: %v", err)
	}

	measurements := []struct {
		column string
		got    sql.NullFloat64
		want   float64
	}{
		{"wind_sample_interval", windSampleInterval, 1.5},
		{"lightning_strike_count", lightningStrikeCount, 2.5},
		{"report_interval", reportInterval, 5.9},
	}
	for _, c := range measurements {
		if !c.got.Valid {
			t.Errorf("%s = NULL, want %v", c.column, c.want)
			continue
		}
		if c.got.Float64 != c.want {
			t.Errorf("%s = %v, want %v (fractional API value must not be truncated)", c.column, c.got.Float64, c.want)
		}
	}

	if !precipType.Valid {
		t.Fatal("precip_type = NULL, want 1")
	}
	if precipType.Int64 != 1 {
		t.Errorf("precip_type = %d, want 1 (categorical enum: still truncated by design)", precipType.Int64)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
go test ./internal/sqlite/ -run TestInsertObservationsPreservesFractionalMeasurements -v 2>&1 | tail -20
```

Expected: FAIL with `wind_sample_interval = 1, want 1.5` (and `2, want 2.5`, `5, want 5.9`) — the
values `asInt64` truncated. The `precip_type` assertion should already pass.

**This is the genuine red state for this task.** If it passes, stop and report.

- [ ] **Step 3: Widen `observationRow` in `internal/sqlite/writer.go`**

At `:96-118`, change three fields — `precipType` stays `*int64`:

```go
	windSampleInterval   *float64
	...
	precipType           *int64
	...
	lightningStrikeCount *float64
	...
	reportInterval       *float64
```

- [ ] **Step 4: Update the two now-false doc comments**

`:87-95` — replace:

```go
// INTEGER-typed columns here (wind_sample_interval, precip_type,
// lightning_strike_count, and report_interval) are *int64, not *float64 (fix
// B-LOW: the SQLite DDL declares these INTEGER), and timestamp is a raw
// unix-epoch int64 rather than time.Time (design §12: SQLite stores epoch
// integers, not TIMESTAMPTZ).
```

with:

```go
// precip_type here is *int64 rather than *float64 -- it is a categorical
// enum, INTEGER in both stores -- and timestamp is a raw unix-epoch int64
// rather than time.Time (design §12: SQLite stores epoch integers, not
// TIMESTAMPTZ). The three measurement columns match Postgres exactly.
```

`:399-403` — replace:

```go
// handleObservationReport for the field-index table), except INTEGER-typed
// columns are converted to int64 (fix B-LOW) and timestamp is stored as a
// raw unix-epoch int64 (design §12).
```

with:

```go
// handleObservationReport for the field-index table), except precip_type is
// converted to int64 (categorical enum, INTEGER in both stores) and timestamp
// is stored as a raw unix-epoch int64 (design §12).
```

- [ ] **Step 5: Drop the three `int64(...)` conversions in `handleObservationReport`**

At `:436-457`, three of the four blocks change. **`precipType` at `:441` is unchanged:**

```go
		if len(ob) >= 6 {
			interval := ob[5]
			row.windSampleInterval = &interval
		}
		if len(ob) >= 14 {
			precipType := int64(ob[13])
			row.precipType = &precipType
		}
		if len(ob) >= 16 {
			distance := ob[14]
			count := ob[15]
			row.lightningDistance = &distance
			row.lightningStrikeCount = &count
		}
		if len(ob) >= 17 {
			battery := ob[16]
			row.battery = &battery
		}
		if len(ob) >= 18 {
			interval := ob[17]
			row.reportInterval = &interval
		}
```

- [ ] **Step 6: Replace `asInt64` in `internal/sqlite/backfill.go`**

Delete `asInt64` (`:194-208`) and add in its place:

```go
// asPrecipType converts a possibly-nil *float64 into a possibly-nil *int64
// for precip_type, which is INTEGER in both stores. Unlike the three
// measurement columns, precip_type is a categorical enum (0 none, 1 rain,
// 2 hail, 3 rain + hail), so a fractional value is corrupt input and
// int64(...) truncation -- matching the UDP path at writer.go's
// handleObservationReport -- is the intended coercion, not data loss. Nil
// maps to SQL NULL.
func asPrecipType(p *float64) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}
```

Then change the bind at `:171-177` — three `asInt64` calls become direct binds, one becomes
`asPrecipType`:

```go
		res, err := stmt.ExecContext(ctx,
			uuid.Must(uuid.NewV7()).String(), o.SerialNumber, o.Timestamp.Unix(),
			o.WindLull, o.WindAvg, o.WindGust, o.WindDirection, o.WindSampleInterval,
			o.Pressure, o.TempAir, wetBulb(o), o.Humidity,
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, asPrecipType(o.PrecipType),
			o.LightningDistance, o.LightningStrikeCount,
			o.Battery, o.ReportInterval)
```

**Do not touch the `InsertObservations` doc comment at `:161-163`.** Its rationale — that
`observationRow`'s *leading* fields are non-pointer `float64`, so routing through it would coerce a
JSON null to `0.0` — is still true and correct (`writer.go:100-103` are unchanged).

- [ ] **Step 7: Update the stale comments in `internal/sqlite/backfill_test.go`**

`:241-253` and the inline notes at `:263,271,273,275` describe these as INTEGER columns. Read them
and correct the wording: the three measurement columns are `REAL` and preserve fractional values;
`precip_type` is `INTEGER` and still truncates. Do not change any assertion values in those tests
unless the change makes one false — if it does, report which and why before editing.

- [ ] **Step 8: Run the tests and watch them pass**

```bash
go test ./internal/sqlite/ -v 2>&1 | tail -30
```

Expected: all pass, including `TestInsertObservationsPreservesFractionalMeasurements`.

- [ ] **Step 9: Add a UDP-path round-trip test**

The REST path is now covered; the UDP path is not. Add to `internal/sqlite/writer_test.go`:

```go
// TestWriter_PreservesFractionalMeasurements is the UDP-path counterpart to
// internal/sqlite's TestInsertObservationsPreservesFractionalMeasurements.
// obs_st indices: 0 epoch, 5 wind sample interval, 13 precip type, 15
// lightning strike count, 17 report interval. The three measurements must
// survive fractionally; precip_type must still truncate.
func TestWriter_PreservesFractionalMeasurements(t *testing.T) {
	w := newTestWriter(t)
	ctx := t.Context()

	report := &tempestudp.TempestObservationReport{
		SerialNumber: "ST-FRAC",
		Obs: [][]float64{
			//  0           1    2    3    4    5     6        7     8     9      10  11   12   13   14   15   16   17
			{1700000100, 1.5, 2.0, 2.5, 180, 1.5, 1013.25, 20.5, 55.0, 50000, 3, 500, 0.5, 1.9, 2.1, 2.5, 3.6, 5.9},
		},
	}

	if err := w.WriteReport(ctx, report); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var (
		windSampleInterval, lightningStrikeCount, reportInterval float64
		precipType                                               int64
	)
	err := w.db.QueryRowContext(ctx, `
		SELECT wind_sample_interval, precip_type, lightning_strike_count, report_interval
		FROM tempest_observations WHERE serial_number = ?`, "ST-FRAC",
	).Scan(&windSampleInterval, &precipType, &lightningStrikeCount, &reportInterval)
	if err != nil {
		t.Fatalf("scan observation row: %v", err)
	}

	if windSampleInterval != 1.5 {
		t.Errorf("wind_sample_interval = %v, want 1.5", windSampleInterval)
	}
	if lightningStrikeCount != 2.5 {
		t.Errorf("lightning_strike_count = %v, want 2.5", lightningStrikeCount)
	}
	if reportInterval != 5.9 {
		t.Errorf("report_interval = %v, want 5.9", reportInterval)
	}
	if precipType != 1 {
		t.Errorf("precip_type = %d, want 1 (categorical enum: truncated by design)", precipType)
	}
}
```

The `Obs` row above is the same 18-element shape as the existing
`TestWriter_InsertsObservation` fixture at `writer_test.go:57-60`, with indices 5, 13, 15, and 17
changed to fractional values. `tempestudp` and `uuid` are already imported by this file.

Run it and watch it pass:

```bash
go test ./internal/sqlite/ -run TestWriter_PreservesFractionalMeasurements -v 2>&1 | tail -20
```

If you run this **before** Steps 3–5, it fails with `wind_sample_interval = 1, want 1.5` — that is
the same red state Step 2 already demonstrated for the REST path, and running it early is a
legitimate way to prove this test bites too.

- [ ] **Step 10: Run the full gate**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./... && \
  go test -race ./... 2>&1 | tail -20
```

- [ ] **Step 11: Commit**

```bash
git add internal/sqlite
git status --porcelain
git commit -m "fix(sqlite): stop truncating measurement columns on the write path

Both write paths converted *float64 to *int64 before binding -- the UDP
path with int64(ob[5]) and friends, the REST path with asInt64 -- so the
same observation was stored as 1.5 in Postgres and 1 in SQLite. That
truncation, not the DDL, was the actual defect in #107: SQLite INTEGER
affinity stores 1.5 as REAL rather than truncating it.

precip_type keeps its conversion, now in a helper named for that one
column. It is a categorical enum, INTEGER in both stores, where a
fractional value is corrupt input.

The reworked test previously asserted the truncation; it now asserts
fidelity for the three measurements and truncation for precip_type.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

---

### Task 4: Run the Postgres suite in CI

**Files:**
- Modify: `.github/workflows/on-pull-request.yml` (`tests` job, `:15-22`)
- Modify: `.github/workflows/on-push-main.yml` (`tests` job, `:21-28`)
- Modify: `.github/workflows/on-release.yml` (`tests` job)
- Modify: `.github/actions/tests/action.yml`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `POSTGRES_URL` set during `go test`, so `internal/postgres`'s untagged
  `POSTGRES_URL`-guarded tests run instead of skipping.

**Scope:** the standard, untagged suite only. Do **not** add `-tags integration` — that would
enable `internal/postgres/writer_integration_test.go`, which contains the known-failing
`TestPostgresWriter_DrainOnClose_Integration` tracked as issue #111, and CI would go red.

**Why the service block goes in the workflows and not the action:** `services:` is a job-level
workflow key. `.github/actions/tests` is a *composite* action, and composite actions cannot declare
services. The block is therefore duplicated three times; that is accepted so the image tag stays
visible to Renovate, which keeps the three copies in step.

- [ ] **Step 1: Confirm the failing state**

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance
go test ./internal/postgres/ -run TestBackfillPostgresIntegration -v 2>&1 | tail -10
grep -rn 'services:' .github/workflows/; echo "exit=$?"
```

Expected: the test reports `SKIP` with `POSTGRES_URL not set`; the grep prints nothing and
`exit=1`. This is the state the task removes.

- [ ] **Step 2: Add the service container to `.github/workflows/on-pull-request.yml`**

Replace the `tests` job (currently `:15-22`) with:

```yaml
  tests:
    runs-on: ubuntu-latest
    services:
      # The Postgres-backed tests in internal/postgres are guarded on
      # POSTGRES_URL and skip without it, so before this they never ran in CI
      # and the Postgres half of the store was asserted by nothing. Only the
      # standard untagged suite runs -- `-tags integration` is deliberately not
      # enabled, because it holds the known-failing drain-on-close test (#111).
      postgres:
        image: postgres:16
        env:
          POSTGRES_PASSWORD: postgres
          POSTGRES_DB: weather
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    steps:
      - name: Checkout
        uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3

      - name: Tests
        uses: ./.github/actions/tests
        with:
          postgres-url: "postgres://postgres:postgres@localhost:5432/weather?sslmode=disable"
```

- [ ] **Step 3: Apply the identical change to the other two workflows**

`.github/workflows/on-push-main.yml` — its `tests` job at `:21-28`. `.github/workflows/on-release.yml`
— its `tests` job. Both get the same `services:` block and the same `with: postgres-url:` argument.
Copy the block verbatim, including the comment, so Renovate updates all three identically.

Leave every other job in those files untouched. In particular, if PR #117 has already merged, do
**not** re-add the `latest:` / `tag-strategy:` lines it removed from the `docker` steps.

- [ ] **Step 4: Add the input to `.github/actions/tests/action.yml`**

After the `description:` line at the top, add:

```yaml
inputs:
  postgres-url:
    description: >-
      Connection string for a Postgres instance the test suite can use. When
      empty, internal/postgres's integration tests skip themselves, which is
      the correct behaviour for a local run without a database.
    required: false
    default: ""
```

Then add `env:` to the "Run all Tests" step (currently `:83-94`), leaving the rest of that step
unchanged:

```yaml
    - name: Run all Tests
      shell: bash
      working-directory: ${{ github.workspace }}
      env:
        POSTGRES_URL: ${{ inputs.postgres-url }}
      run: |
```

- [ ] **Step 5: Verify the workflows are valid**

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"
```

Expected: no output, `exit=0`. If it reports `input "postgres-url" is not defined in action "Tests"`,
Step 4 did not land — actionlint validates the caller→declaration contract for local composite
actions.

- [ ] **Step 6: Verify the tests actually run with a database**

```bash
docker run --rm -d --name pg-conformance -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=weather -p 55432:5432 postgres:16
until docker exec pg-conformance pg_isready -U postgres; do sleep 1; done
POSTGRES_URL='postgres://postgres:postgres@localhost:55432/weather?sslmode=disable' \
  go test ./internal/postgres/ ./internal/config/ -count=1 -v 2>&1 | tail -30
docker rm -f pg-conformance
```

Expected: `TestBackfillPostgresIntegration` runs and PASSes rather than skipping. `internal/config`
is included deliberately: its tests read and write `POSTGRES_URL` via `t.Setenv`, and this confirms
a globally-set value does not disturb them. If any `internal/config` test fails only when
`POSTGRES_URL` is set, stop and report — that is a real interaction the design flagged as needing
verification.

- [ ] **Step 7: Run the full gate and commit**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && go test -race ./... 2>&1 | tail -10
git add .github
git status --porcelain
git commit -m "ci: run the Postgres suite against a real database

internal/postgres's tests are guarded on POSTGRES_URL and skip without
it, so CI has never exercised the Postgres store at all. Adds a Postgres
service container to the three workflows that call the tests action, and
passes the connection string through as an input.

services: is a job-level key and the tests action is composite, so the
block cannot live in the action and is duplicated across the three
callers. That is accepted so the image tag stays visible to Renovate,
which is what keeps the three copies in step.

Standard untagged suite only. -tags integration stays off: it holds
TestPostgresWriter_DrainOnClose_Integration, which fails against a real
database on unmodified main (#111).

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

---

### Task 5: Postgres — explicit `precip_type` conversion and a fractional round-trip test

**Files:**
- Modify: `internal/postgres/backfill.go` (`:154-162`)
- Modify: `internal/postgres/backfill_test.go`

**Interfaces:**
- Consumes: Task 4's CI database, so the new test is real coverage rather than a skip.
- Produces: nothing later tasks depend on.

**This task changes no stored value.** pgx already truncates a `*float64` bound into an `INTEGER`
column (`pgtype/builtin_wrappers.go`, `int64(w)` with no error), so `1.9` is already stored as `1`.
The change makes that coercion explicit, matching Postgres's own daemon path
(`internal/postgres/writer.go:541`, `precipType := int(ob[13])`) and SQLite's new `asPrecipType`.

- [ ] **Step 1: Write the failing test**

Add to `internal/postgres/backfill_test.go`, following the `POSTGRES_URL` skip idiom already used at
`:89-91`:

```go
// TestInsertObservationsPreservesFractionalMeasurements is the Postgres half
// of the cross-store conformance pinned on the SQLite side by
// TestInsertObservationsPreservesFractionalMeasurements in internal/sqlite.
// The three measurement columns are DOUBLE PRECISION here and REAL there, so
// a fractional API value must survive both. precip_type is INTEGER in both
// and truncates in both.
func TestInsertObservationsPreservesFractionalMeasurements(t *testing.T) {
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL not set; skipping Postgres integration test")
	}

	ctx := t.Context()
	pool, err := OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := CreateSchema(ctx, pool); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	serial := "ST-FRAC"
	stamp := time.Unix(1_900_000_000, 0).UTC()
	if _, err := pool.Exec(ctx,
		`DELETE FROM tempest_observations WHERE serial_number = $1`, serial); err != nil {
		t.Fatalf("clean: %v", err)
	}

	obs := []weather.Observation{{
		SerialNumber:         serial,
		Timestamp:            stamp,
		WindSampleInterval:   f(1.5),
		PrecipType:           f(1.9),
		LightningStrikeCount: f(2.5),
		ReportInterval:       f(5.9),
	}}
	if _, err := InsertObservations(ctx, pool, obs); err != nil {
		t.Fatalf("InsertObservations: %v", err)
	}

	var windSampleInterval, lightningStrikeCount, reportInterval float64
	var precipType int
	err = pool.QueryRow(ctx, `
		SELECT wind_sample_interval, precip_type, lightning_strike_count, report_interval
		FROM tempest_observations WHERE serial_number = $1 AND timestamp = $2`,
		serial, stamp,
	).Scan(&windSampleInterval, &precipType, &lightningStrikeCount, &reportInterval)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if windSampleInterval != 1.5 {
		t.Errorf("wind_sample_interval = %v, want 1.5", windSampleInterval)
	}
	if lightningStrikeCount != 2.5 {
		t.Errorf("lightning_strike_count = %v, want 2.5", lightningStrikeCount)
	}
	if reportInterval != 5.9 {
		t.Errorf("report_interval = %v, want 5.9", reportInterval)
	}
	if precipType != 1 {
		t.Errorf("precip_type = %d, want 1 (categorical enum: truncated by design)", precipType)
	}
}
```

Check the file's existing imports and helpers first — `f`, `os`, `time`, and `weather` should
already be present. Match the existing cleanup idiom if the file has one rather than the `DELETE`
above.

- [ ] **Step 2: Run it against a real database and watch the result**

```bash
docker run --rm -d --name pg-conformance -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=weather -p 55432:5432 postgres:16
until docker exec pg-conformance pg_isready -U postgres; do sleep 1; done
POSTGRES_URL='postgres://postgres:postgres@localhost:55432/weather?sslmode=disable' \
  go test ./internal/postgres/ -run TestInsertObservationsPreservesFractionalMeasurements -count=1 -v 2>&1 | tail -20
```

**Expected: this test PASSES before the Step 3 change.** The three measurement columns are already
`DOUBLE PRECISION` bound from `*float64`, and pgx already truncates `precip_type`. That is the
point — this test is a regression pin for behaviour that is already correct, and Step 3 is a
readability change that must not alter it.

**If it fails, stop and report** — that would mean the design's premise about the Postgres side is
wrong, and Task 5 needs rethinking rather than patching. A `SKIP` means the database is not
reachable; fix that before continuing.

- [ ] **Step 3: Make the `precip_type` conversion explicit**

In `internal/postgres/backfill.go`, add above `InsertObservations`:

```go
// asPrecipType converts a possibly-nil *float64 into a possibly-nil *int for
// precip_type, which is INTEGER here and in SQLite. pgx already truncates a
// float bind into an integer column silently; doing it here makes the
// coercion visible and matches the daemon path (writer.go's
// handleObservationReport) and internal/sqlite's helper of the same name.
// Nil maps to SQL NULL.
func asPrecipType(p *float64) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
```

Then change the bind at `:159`:

```go
			o.Illuminance, o.UVIndex, o.Irradiance, o.RainRate, asPrecipType(o.PrecipType),
```

- [ ] **Step 4: Re-run and confirm the behaviour is unchanged**

```bash
POSTGRES_URL='postgres://postgres:postgres@localhost:55432/weather?sslmode=disable' \
  go test ./internal/postgres/ -count=1 2>&1 | tail -20
docker rm -f pg-conformance
```

Expected: still PASS, with the same `precip_type = 1`. A behaviour change here is a bug.

- [ ] **Step 5: Run the full gate and commit**

```bash
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./... && \
  go test -race ./... 2>&1 | tail -10
git add internal/postgres
git status --porcelain
git commit -m "refactor(postgres): make the precip_type truncation explicit

The backfill path bound o.PrecipType -- a *float64 -- straight into
precip_type INTEGER, leaving pgx to truncate it silently. The stored
value was already correct and already agreed with SQLite, so this
changes no behaviour; it makes the coercion visible and matches both the
daemon path and internal/sqlite's asPrecipType.

Also pins the Postgres half of the cross-store fractional round-trip,
which #107 asks for through both stores and which now runs in CI rather
than skipping.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S2gxnijhMiDjc7hC1PVB9D"
```

---

## Final verification (after all tasks)

Run from the worktree root. All must hold simultaneously.

```bash
cd /Users/acaudill/Projects/github/tempestwx-exporter/.claude/worktrees/schema-conformance

# 1. The three columns are REAL; precip_type is not
go test ./internal/sqlite/ -run TestMigrateDeclaresMeasurementColumnsAsREAL -v 2>&1 | tail -5

# 2. No truncating helper survives under its old name
git grep -n 'asInt64' -- ':!docs/'; echo "exit=$?"          # expect exit=1, no output

# 3. The folded migration is gone and only 0001 remains
git ls-files internal/sqlite/migrations/                     # expect only 0001_init.sql

# 4. No stale reference to the deleted migration outside docs
git grep -n '0002_add_timestamp_index' -- ':!docs/'; echo "exit=$?"   # expect exit=1

# 5. Workflows valid
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"   # expect exit=0

# 6. Full gate, no database
CGO_ENABLED=0 go build ./... && go vet ./... && gofmt -l . && \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./... && \
  go test -race ./...

# 7. Full gate, with a database -- the Postgres tests must RUN, not skip
docker run --rm -d --name pg-final -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=weather -p 55432:5432 postgres:16
until docker exec pg-final pg_isready -U postgres; do sleep 1; done
POSTGRES_URL='postgres://postgres:postgres@localhost:55432/weather?sslmode=disable' \
  go test -race ./... -count=1 2>&1 | tail -20
docker rm -f pg-final

# 8. Nothing outside the intended surface changed
git diff --name-only a1b07f1..HEAD
```

Step 8 must list only: `docs/designs/2026-08-02-sqlite-float-conformance-design.md`,
`docs/plans/2026-08-02-sqlite-float-conformance-implementation.md`, the four `.github` files, and
files under `internal/sqlite/`, `internal/httpserver/`, `internal/postgres/`.

**Not verifiable before merge:** that a real WeatherFlow response containing a fractional value
round-trips end to end. No token is available. The tests pin the behaviour with synthetic
fractional values, which is the strongest available pre-merge evidence.

## Out of scope — do not do these

- **Do not enable `-tags integration` anywhere.** It holds #111's known-failing test.
- **Do not fix #111.** Separate issue.
- **Do not change `precip_type` to a float** in either store, or remove its truncation.
- **Do not touch `rain_rate`** — it is the precipitation amount, already `REAL`/`DOUBLE PRECISION`.
- **Do not add a `0003` migration** or restore `0002`.
- **Do not touch `web/`.** `web/src/types/weather.ts` already types these fields as `number`.
- **Do not touch `internal/postgres/schema.go`** — the three columns are already `DOUBLE PRECISION`.
- **Do not push, open a PR, or merge.**
