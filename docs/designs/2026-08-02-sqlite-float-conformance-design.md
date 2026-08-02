# SQLite float conformance — design

**Issue:** [#107](https://github.com/jacaudi/tempestwx-utilities/issues/107)
**Date:** 2026-08-02
**Status:** proposed (revised after Gate 1 review)
**Branch point:** `a1b07f1`

## Goal

Stop three measurement columns from being silently truncated to integers on the
SQLite write path, so the SQLite and Postgres stores hold the same value for the
same observation, and make the SQLite DDL declare the type it actually stores.

## What #107 gets wrong, and what it gets right

#107 describes the problem as a *read* failure: a fractional value is stored with
`REAL` affinity and the `*int64` read model then fails for the whole query, so
"the current-observation endpoint breaks for that station."

**The mechanism is real; the reachability claim is not.** No fractional value can
reach these columns from either production write path, because both truncate in
Go before binding:

| Path | Site | Conversion |
|---|---|---|
| UDP ingest | `internal/sqlite/writer.go:437,446,455` | `int64(ob[5])`, `int64(ob[15])`, `int64(ob[17])` |
| REST backfill | `internal/sqlite/backfill.go:173,176,177` | `asInt64(...)` (`:202`) |

`asInt64`'s own doc comment names the exact crash #107 describes and exists to
prevent it. Those are the only two writers to `tempest_observations` — the sole
callers of `insertObservationSQL` are `writer.go:635` and `backfill.go:170`.

**This does not change the fix.** #107's Scope items 2 and 3 are the Go changes
below, near-identically. What the reframing changes is the *severity story* (an
unreachable crash versus a live cross-store divergence) and, separately, the
migration strategy — and that changed for an unrelated reason, the scope decision
below, not because of the diagnosis.

### What is actually broken

Silent truncation in Go. The same observation is persisted as `1.5` in Postgres
(`DOUBLE PRECISION`, `*float64` end to end) and as `1` in SQLite. That is a
cross-store data divergence, reachable today through the normal backfill path.

## SQLite affinity — measured, with the correction

Probed against `modernc.org/sqlite`, in-memory, one table declared `INTEGER`
(`ti`) and one `REAL` (`tr`):

```
ti bind 1.5          -> typeof=real     value=1.5      (NOT truncated)
ti bind 5            -> typeof=integer  NullInt64=5
tr bind 5            -> typeof=real     NullInt64=5
ti bind 999999       -> typeof=integer  NullInt64=999999
tr bind 999999       -> typeof=real     NullInt64=999999
ti bind 1e+06        -> typeof=integer  NullInt64=1000000
tr bind 1e+06        -> typeof=real     NullInt64 ERROR ("1e+06": invalid syntax)
tr bind 8.388608e+06 -> typeof=real     NullInt64 ERROR ("8.388608e+06": invalid syntax)
```

1. **`INTEGER` affinity was never lossy.** It stores `1.5` as `REAL` rather than
   truncating. The DDL is not where data was being lost — Go is.
2. **The declared type matters, and it favours `INTEGER` for `NullInt64` reads.**
   `INTEGER` affinity converts a lossless integral float to integer storage, so a
   `NullInt64` scan works at any magnitude. `REAL` affinity keeps it a float, and
   `database/sql` converts float64→int64 via
   `strconv.FormatFloat(v, 'g', -1, 64)`, which switches to scientific notation at
   exponent ≥ 6 — so a `NullInt64` scan from a `REAL` column **fails at ≥ 1e6**.

> **Correction.** An earlier revision of this design stated conclusion 2 as "the
> declared type is nearly irrelevant to the crash; an integral value scans into
> `NullInt64` fine even in a `REAL` column." That was generalised from a probe of
> three small values (`1.5`, `5.0`, `2.6`) and is false at scale, in the opposite
> direction. Caught by Gate 1 review and re-verified.

**Practical reach for these three columns: nil.** A lightning strike count per
minute, a wind sample interval in seconds, and a report interval in minutes do
not approach 1e6. The magnitude hazard is why the *claim* had to be corrected,
not a live risk — but it does mean every `NullInt64` read of a now-`REAL` column
must be changed, because "it still passes today" is not evidence of correctness.

## Scope decision

Nothing is deployed and no database exists anywhere that this must remain
compatible with. The schema is therefore rewritten to what it should be, rather
than patched forward with a rebuild migration. `0002` is folded into `0001` and
deleted; no `0003` is created.

This is only sound because there are no databases to migrate. It is recorded here
so a future reader does not mistake it for a general licence to edit applied
migrations.

**The premise is load-bearing and unverifiable from here.** `0001_init.sql` and
`0002_add_timestamp_index.sql` both shipped in v3.0.0 and v3.1.0 (`git ls-tree`
confirms), so a database at `schema_version = 2` *could* exist. If one did, the
consequence is worse than keeping stale column types: `Migrate` skips any file
whose `version <= current` (`internal/sqlite/schema.go:39`), so the folded `0001`
is skipped **and so is every future migration numbered `0002`** — silently, with
no error and no log. See "Fail loud on a future database" below.

## Changes

### `internal/sqlite/migrations/0001_init.sql`

- `wind_sample_interval` `INTEGER` → `REAL`
- `lightning_strike_count` `INTEGER` → `REAL`
- `report_interval` `INTEGER` → `REAL`
- `precip_type` stays `INTEGER` — a categorical enum, not a measurement, and it
  already agrees with Postgres. Conformance means the two backends agree *per
  column*, not that all four share one type.
- Drop the now-false `-- INTEGER not float (fix B-LOW)` comment.
- Fold in `CREATE INDEX IF NOT EXISTS idx_obs_time ON tempest_observations(timestamp)`
  from `0002`, placed with `idx_obs_serial_time` after the `CREATE TABLE`
  statements (`:29`). Carry its rationale comment — that comment is the only
  record of why the index exists (`idx_obs_serial_time` leads with
  `serial_number`, so `LatestObservationAny` and `HistoryPoints` cannot use it);
  losing it invites a later reader to delete the index as redundant. **Drop the
  `(0001_init.sql)` parenthetical**, which becomes self-referential once folded.

### Delete `internal/sqlite/migrations/0002_add_timestamp_index.sql`

### Fail loud on a future database — `internal/sqlite/schema.go`

Add to `Migrate`: if `current` exceeds the highest version present in
`migrationsFS`, return an error naming both numbers instead of silently applying
nothing.

This does not reopen the fold decision — it converts the fold's one silent
failure mode into a startup failure, matching the project's existing posture
("If the SQLite database cannot be opened, the process exits on startup",
`CLAUDE.md`). It also covers the general case of a downgraded binary meeting a
newer database. Roughly two lines plus a test.

*This is an addition beyond #107's scope, included because the stated goal is a
production-ready schema. Drop it if unwanted.*

### `internal/sqlite/writer.go`

| Site | Change |
|---|---|
| `:104,115,117` | `observationRow` three fields `*int64` → `*float64` |
| `:113` | `precipType` stays `*int64` |
| `:437,446,455` | drop the `int64(...)` conversions |
| `:441` | `precipType` keeps `int64(ob[13])` |
| `:760-761` | three columns `sql.NullInt64` → `sql.NullFloat64`; `precipType` stays `NullInt64`. Note `:760` declares `windSampleInterval, precipType` on one line — the split is required, not optional |
| `:720,731,733` | public `Observation` three fields `*int64` → `*float64` |
| `:948` | `Summary.LightningTotal` `sql.NullInt64` → `sql.NullFloat64` |
| `:87-95` | `observationRow` doc comment — states the `*int64` divergence from Postgres as deliberate |
| `:399-403` | `handleObservationReport` doc comment — "INTEGER-typed columns are converted to int64 (fix B-LOW)" |

**`Summary.LightningTotal` is the one site that fails silently.** It scans
`SUM(lightning_strike_count)` (`:960`). The failure is *not* "the sum returns
REAL and the scan fails" — measured, `SUM(2,3)` over a `REAL` column returns
`typeof=real` and scans into `NullInt64` as `5` **successfully**. It fails only
on a fractional sum (`SUM(2.5,3)` → `5.5` → scan error) or at ≥ 1e6. So leaving
it as `NullInt64` compiles, passes every existing test, and breaks in production
the first time a fractional strike count is summed.

### `internal/sqlite/backfill.go`

- Delete `asInt64` (`:202`) and bind `WindSampleInterval`, `LightningStrikeCount`,
  and `ReportInterval` directly from `weather.Observation` (already `*float64`).
- `precip_type` still needs a `*float64` → `*int64` conversion. Keep a helper for
  that one column; the plan names it.
- **Do not** edit the `InsertObservations` doc comment at `:161-163`. Its
  rationale — that `observationRow`'s leading fields are non-pointer `float64`,
  so routing through it would coerce a JSON null to `0.0` — remains true and
  correct after the change (`writer.go:100-103` are unchanged). The comment that
  goes stale is `asInt64`'s at `:194-201`, and it is deleted with the function.

### `internal/httpserver/observations.go`

Exactly three edits:

- `:70,80,82` `int64` → `float64`
- `:130` `LightningTotal *int64` → `*float64`
- `:280` `i64(s.LightningTotal)` → `f64(s.LightningTotal)`. **`f64` already exists**
  and is used for `TempMax`, `RainTotal`, and the rest — no new helper is needed

**`deref` (`:342`) needs no change** — it is generic, `func deref[T any](p *T) T`.
**`i64` (`:154`) must be kept**, not converted or deleted: it serves
`CoveredFrom`/`CoveredTo` at `:272,273`, which are `MIN/MAX(timestamp)` over an
`INTEGER` column and stay `NullInt64`. Changing them would silently alter two
wire fields this design does not intend to touch.

This is the public JSON contract the TypeScript UI consumes. The change is nearly
invisible on the wire: Go marshals `float64(5)` as `5`, not `5.0`, so every
integral value serialises byte-identically (verified for `0` through `2^53`;
divergence begins at ≥ 1e21, unreachable here). Only a genuinely fractional value
looks different — which is the point. `web/src/types/weather.ts:21,31,33,150`
already types these as `number`, so no TypeScript change is needed. Note CI has
no web/TypeScript step, so that claim is not machine-checked.

### `internal/postgres/backfill.go`

`:159` binds `o.PrecipType` — a `*float64` — into `precip_type INTEGER`
(`schema.go:55`). pgx truncates it silently (`pgtype/builtin_wrappers.go:358-364`,
`int64(w)` with no error). The stored value is correct and agrees with SQLite, so
this is not a correctness defect, but it is the same implicit truncation this
design is removing elsewhere, and Postgres's *daemon* path already converts
explicitly (`writer.go:45,541`). Make the backfill path convert explicitly too,
for symmetry with the SQLite helper.

This is #107's Scope item 4, which an earlier revision dropped without saying so.

### Postgres smoke coverage in CI

Today every Postgres-dependent test skips in CI, so the Postgres half of this
change is asserted by nothing. Add a Postgres service container and set
`POSTGRES_URL`, so the standard suite exercises it for real.

**Placement is forced.** `services:` is a job-level workflow key; a composite
action cannot declare one, and `.github/actions/tests` is composite. So the
service block goes in the three workflows that call it —
`on-pull-request.yml:22`, `on-push-main.yml:28`, `on-release.yml:20` — and the
action gains a `POSTGRES_URL` pass-through.

That triplicates the block, which is a DRY cost. It is accepted rather than
worked around: the alternative (starting Postgres with `docker run` inside the
composite action, single-sourced) hides the image tag from Renovate, and this
repo actively relies on Renovate to keep Docker tags current. A `services:`
image is scanned; a tag buried in a `run:` string is not. Renovate updating all
three in one PR is what makes the duplication safe.

**Scope: the standard, untagged suite only.** The test this activates is
`internal/postgres/backfill_test.go:89-91`, which skips on missing
`POSTGRES_URL`. `internal/postgres/writer_integration_test.go` is behind
`//go:build integration` and is **not** enabled — it contains the known-failing
`TestPostgresWriter_DrainOnClose_Integration` from #111, which would turn CI red.
Enabling the integration tag is deliberately left to #111.

With this in place, the Postgres fractional round-trip test is worth adding —
`internal/postgres/backfill_test.go` currently seeds only integral values
(`f(3)`, `f(25)`, `f(35)`), so it would not catch a regression on the Postgres
side of the conformance this design establishes.

**Check for interference:** `internal/config/database_test.go` reads and writes
`POSTGRES_URL` via `t.Setenv`, which overrides the ambient value per-test and
restores it, so a globally-set `POSTGRES_URL` should not disturb it. Verify by
running the suite with the variable set.

### Not touched

- `internal/postgres/schema.go` and `writer.go` — the three columns are already
  `DOUBLE PRECISION` / `*float64`.
- `internal/weather/observation.go` — already `*float64`.
- The `schema_version` mechanism itself, beyond the fail-loud guard above.

## Test and comment sites the change list must cover

These compile-break or, worse, keep passing while asserting nothing:

| Site | Nature |
|---|---|
| `internal/sqlite/writer_test.go:76,81,83,95,98,99` | **silent survivor** — declares the three as plain `int64` and raw-scans them; seeded `3,4,5` still scan fine from a `REAL` column, so the test stays green while proving nothing. Must move to `float64` |
| `internal/sqlite/litestream_test.go:48,49,63,75,81` | `sql.NullInt64` scans, `&x.Int64` assignment. **Correction:** an earlier revision said "CI installs litestream" — it does not (`grep -rn -i litestream .github/` returns nothing), so this test SKIPS in CI and runs only where litestream is on PATH locally. It still must be fixed; it just is not gated |
| `internal/sqlite/summary_test.go:22,31,32,33,51,52` | compile break on `.Int64` / `%d` |
| `internal/httpserver/observations_test.go:70,72,82,92,94,325` | `int64(...)` locals assigned into `sqlite.Observation`; `sql.NullInt64{}` literal |
| `internal/sqlite/backfill_test.go:381,392` | compile break |
| `internal/sqlite/schema_test.go:37-41` | comment references the deleted `0002_add_timestamp_index.sql` |
| `internal/sqlite/backfill_test.go:241-253,263,271,273,275` | comments asserting the INTEGER-column rationale |

## Testing

| Test | Purpose |
|---|---|
| rework `backfill_test.go:350-410` | fractional value survives the REST path. **`precip_type` must NOT be inverted** — it is one of the four columns this test covers (`PrecipType: f(1.9)` at `:369`, asserting `1` at `:397`) and it must still truncate. Only the other three flip |
| new: UDP path round-trip | fractional value survives `handleObservationReport` |
| new: fractional `SUM` | seed a fractional `lightning_strike_count`, assert the summed total. This is the only thing that catches a missed `LightningTotal` — the existing summary tests seed `2` and `3` and pass either way |
| new: `pragma_table_info` assertion | the three columns declare `REAL` |
| new: `Migrate` fail-loud | a database at a version higher than any bundled migration errors rather than no-opping |
| `schema_test.go:43,50` | `assertSchemaVersion` 2 → 1 |
| existing `idx_obs_time` assertion | unchanged — proves the fold did not drop the index |

| new: Postgres fractional round-trip | `internal/postgres/backfill_test.go` — closes #107's "through both stores" criterion. Real coverage now that CI sets `POSTGRES_URL` |

**Gap closed.** An earlier revision listed the Postgres round-trip as a known
omission because those tests skip without `POSTGRES_URL`. The CI service
container above removes that excuse, so the test is in scope.

**Still not covered:** anything behind `//go:build integration`, by decision —
see #111.

**TDD note.** The genuine red state is the reworked `backfill_test.go` assertion
and the new fractional-`SUM` test. Compile errors do not count as a failing test
(per this project's own standard, `docs/designs/2026-07-28-api-backfill-tool-design.md:449`),
so the many compile breaks above are mechanical fixes, not evidence.

## Consequences

- SQLite and Postgres agree on stored values for these three columns.
- The SQLite DDL declares what it stores.
- `precip_type` remains an integer in both stores **and still truncates silently**
  — in Go on the SQLite path, in pgx on the Postgres path. That is intended
  coercion of a categorical value, not an oversight.
- ~~Fresh installs apply `0001` and land at `schema_version = 1`; the next migration
  is `0002`, in normal sequence.~~

  **Superseded during execution — the whole-branch review found this combination was
  a blocking defect.** With the folded init numbered `0001`, `highest` is 1, so the
  fail-loud guard rejects *any* database at version 2 — including one this very build
  creates, then meets again after a rollback to v3.1.0 (which applies its own `0002`).
  Reproduced end to end; roll-forward became permanently impossible, and the error
  text was inverted, telling the operator the binary was older when it was newer.

  **Resolution:** the folded init ships as `0002_init.sql`, so `highest` is 2 —
  matching the version every released binary already reaches. Verified: fresh installs
  land at `schema_version = 2` with `REAL` columns; rollback-then-roll-forward
  succeeds; a legacy v3.x database (INTEGER-declared, version 2) is accepted and
  round-trips fractional values correctly, because SQLite affinity is lossless and the
  read path is float-tolerant. The guard still fires on a genuinely newer database
  (version 99 → error), with corrected wording. **The next migration must be `0003`**,
  a constraint now recorded in `0002_init.sql` itself rather than only here.
- The JSON API can now return a fractional `windSampleInterval`,
  `lightningStrikeCount`, `reportInterval`, or `lightningTotal`. No consumer
  breaks — JSON has no integer type — but the UI renders these raw
  (`web/src/components/LightningCard.tsx:25`,
  `RecordsCard.tsx:111-112`), so a fractional value would display as
  "2.5 strikes". Cosmetic, and only if the API ever returns one.
- Widening `lightningTotal` on the wire is a *choice*, not forced —
  `CAST(SUM(...) AS INTEGER)` would have avoided it. A fractional column implies a
  fractional sum, so widening is the honest option.
- **New unstated-assumption made explicit:** these fields can now carry non-finite
  floats into `json.Encoder`, which errors *after* the 200 status line — the
  failure `sanitize()` exists to prevent (`internal/httpserver/observations.go:350-357`).
  Unreachable today because both ingest paths decode via `encoding/json` and JSON
  has no `NaN`/`Infinity` literal. That is now load-bearing.

## Could not verify

- **Whether WeatherFlow ever returns a fractional value** for these three fields.
  No token, no live call. The fix is correctness insurance against a possibly
  latent divergence, not a repair of observed bad data.

  This repo already contains a conflicting claim:
  `docs/designs/2026-07-28-api-backfill-tool-design.md:458` asserts under a
  "verified by execution" heading that "the Tempest API supplies integral values
  there." That claim is unsourced for the API-behaviour half (the measured part
  was pgx truncation) and conflates the stores — three of those four columns are
  `DOUBLE PRECISION` in Postgres, not `INTEGER`. This design's hedge supersedes
  it; the older document should be annotated rather than left to disagree.
- **Whether any database is at `schema_version = 2`.** A binary producing one
  shipped twice. This rests on the owner's stated fact.
- **pgx's `*float64` → `INTEGER` behaviour against a live PostgreSQL** — verified
  by reading `pgtype/builtin_wrappers.go:358-364`, not by execution.
- **Whether the Litestream round-trip passes post-change** — not run, since the
  change is not applied.

## Disposition of #107's acceptance criteria

- "`asInt64` is gone" — satisfied in name; a renamed single-column helper remains
  for `precip_type`, by design.
- "The SQLite rebuild migration preserves existing rows" — moot under the scope
  decision; there is no rebuild migration.
- "A fractional API value round-trips losslessly through both stores" — satisfied
  for both, and the Postgres half is now genuinely exercised in CI rather than
  skipped.
