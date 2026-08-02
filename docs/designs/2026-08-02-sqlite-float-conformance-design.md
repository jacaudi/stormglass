# SQLite float conformance — design

**Issue:** [#107](https://github.com/jacaudi/tempestwx-utilities/issues/107)
**Date:** 2026-08-02
**Status:** proposed
**Branch point:** `a1b07f1`

## Goal

Stop three measurement columns from being silently truncated to integers on the
SQLite write path, so the SQLite and Postgres stores hold the same value for the
same observation, and make the SQLite DDL declare the type it actually stores.

## The defect, correctly stated

Issue #107 describes the problem as a *read* failure: a fractional value is
stored with `REAL` affinity and the `*int64` read model then fails for the whole
query. **That framing is wrong, and the correction matters because it changes
what the fix is.**

No fractional value can reach these columns from either write path, because both
truncate in Go before binding:

| Path | Site | Conversion |
|---|---|---|
| UDP ingest | `internal/sqlite/writer.go:437,446,455` | `int64(ob[5])`, `int64(ob[15])`, `int64(ob[17])` |
| REST backfill | `internal/sqlite/backfill.go:173,176,177` | `asInt64(...)` (`:202`) |

`asInt64`'s own doc comment names the exact crash #107 describes and exists to
prevent it.

### Verified by probe, not assumed

Run against `modernc.org/sqlite` (the driver this project uses), in-memory,
declaring one table `INTEGER` and one `REAL`:

```
INTEGER-declared, bound float 1.5 -> stored typeof=real    value=1.5   (NOT truncated)
INTEGER-declared, bound float 5.0 -> stored typeof=integer value=5
REAL-declared,    bound float 5.0 -> stored typeof=real    value=5

sql.NullInt64   scan of 5.0 -> 5       (succeeds in BOTH declared types)
sql.NullInt64   scan of 1.5 -> ERROR   (fails    in BOTH declared types)
sql.NullFloat64 scan        -> 1.5 / 5 / 2.6 correct in BOTH declared types
```

Two conclusions follow:

1. **SQLite's `INTEGER` declaration was never lossy.** Affinity stores `1.5` as
   `REAL` rather than truncating it. The DDL is not where data was being lost.
2. **The declared type is nearly irrelevant to the crash.** An integral value
   scans into `NullInt64` fine even in a `REAL` column; a fractional one fails
   even there. Changing the DDL alone fixes nothing.

### What is actually broken

Silent truncation in Go. The same observation is persisted as `1.5` in Postgres
(`DOUBLE PRECISION`, `*float64` end to end) and as `1` in SQLite. That is a
cross-store data divergence, and it is reachable today through the normal
backfill path.

Postgres needs no change: `internal/postgres/schema.go:43,58,61` already declares
all three `DOUBLE PRECISION`, and `internal/postgres/writer.go`'s `observationRow`
carries them as `*float64`.

## Scope decision

Nothing is deployed and no database exists anywhere that this must remain
compatible with. The schema is therefore rewritten to what it should be, rather
than patched forward with a rebuild migration. `0002` is folded into `0001` and
deleted; no `0003` is created.

This is only sound because there are no databases to migrate. It is recorded
here so a future reader does not mistake it for a general licence to edit applied
migrations.

## Changes

### `internal/sqlite/migrations/0001_init.sql`

- `wind_sample_interval` `INTEGER` → `REAL`
- `lightning_strike_count` `INTEGER` → `REAL`
- `report_interval` `INTEGER` → `REAL`
- `precip_type` stays `INTEGER` — a categorical enum, not a measurement, and it
  already agrees with Postgres. Conformance means the two backends agree *per
  column*, not that all four columns share one type.
- Drop the now-false `-- INTEGER not float (fix B-LOW)` comment.
- Fold in `CREATE INDEX IF NOT EXISTS idx_obs_time ON tempest_observations(timestamp)`
  from `0002`, **carrying its rationale comment verbatim**. That comment is the
  only record of why the index exists (`idx_obs_serial_time` leads with
  `serial_number`, so `LatestObservationAny` and `HistoryPoints` cannot use it);
  losing it invites a later reader to delete the index as redundant.

### Delete `internal/sqlite/migrations/0002_add_timestamp_index.sql`

### `internal/sqlite/writer.go`

| Site | Change |
|---|---|
| `:104,115,117` | `observationRow` three fields `*int64` → `*float64` |
| `:113` | `precipType` stays `*int64` |
| `:437,446,455` | drop the `int64(...)` conversions |
| `:441` | `precipType` keeps `int64(ob[13])` |
| `:760-761` | three columns `sql.NullInt64` → `sql.NullFloat64`; `precipType` stays `NullInt64` |
| `:720,731,733` | public `Observation` three fields `*int64` → `*float64` |
| `:948` | `Summary.LightningTotal` `sql.NullInt64` → `sql.NullFloat64` |

`Summary.LightningTotal` is load-bearing and easy to miss: it scans
`SUM(lightning_strike_count)` (`:960`). Once that column holds `REAL`, the sum
returns `REAL` and a `NullInt64` scan fails — the summary endpoint breaks at
runtime while every compile-time check passes.

Also update the `observationRow` doc comment at `:87-95`, which currently states
the `*int64` divergence from Postgres as deliberate.

### `internal/sqlite/backfill.go`

- Delete `asInt64` (`:202`) and bind `WindSampleInterval`, `LightningStrikeCount`,
  and `ReportInterval` directly from `weather.Observation` (already `*float64`).
- `precip_type` still needs a `*float64` → `*int64` conversion. Keep a helper for
  that one column, named for what it does rather than the general shape it used
  to have.
- Update the `InsertObservations` doc comment (`:161-163`).

### `internal/httpserver/observations.go`

- `:70,80,82` `int64` → `float64`
- `:130` `LightningTotal *int64` → `*float64`
- the `deref` / `i64` helpers follow

This is the public JSON contract the TypeScript UI consumes, so it is called out
explicitly rather than treated as an internal ripple. The change is nearly
invisible on the wire: Go marshals `float64(5)` as `5`, not `5.0`, so every
integral value serialises byte-identically. Only a genuinely fractional value
looks different — which is the point of the change.

### Not touched

- `internal/postgres/**` — already correct.
- `internal/weather/observation.go` — already `*float64`.
- The `schema_version` machinery itself.

## Testing

TDD has a real red state available without inventing one.
`internal/sqlite/backfill_test.go:358-406` currently **asserts the truncation**:
`want %d (truncated from the fractional API value...)`. Inverting it to assert
the fractional value round-trips fails against today's code and passes after.

| Test | Purpose |
|---|---|
| invert `backfill_test.go:358-406` | fractional value survives the REST path |
| new: UDP path round-trip | fractional value survives `handleObservationReport` |
| new: `pragma_table_info` assertion | the three columns declare `REAL` |
| `schema_test.go:43,50` | `assertSchemaVersion` 2 → 1 |
| existing `idx_obs_time` assertion | unchanged — proves the fold did not drop the index |
| existing summary tests | cover the `LightningTotal` type change |

## Consequences

- SQLite and Postgres agree on stored values for these three columns.
- The SQLite DDL declares what it stores.
- `precip_type` remains the one integer measurement-adjacent column, in both
  stores, by intent.
- Fresh installs apply `0001` and land at `schema_version = 1`. The next
  migration is `0002`, in normal sequence.
- The JSON API can now return a fractional `windSampleInterval`,
  `lightningStrikeCount`, `reportInterval`, or `lightningTotal`. No consumer
  breaks on this — JSON has no integer type — but it is a contract widening.

## Could not verify

- **Whether WeatherFlow ever actually returns a fractional value** for these
  three fields in practice. The REST API's `obs` arrays are JSON numbers, so it
  *can*; whether it *does* was not established. The fix is therefore correctness
  insurance against a divergence that may currently be latent, not a repair of
  observed bad data.
- **Whether any database anywhere is at `schema_version = 2`.** This rests on
  the stated fact that nothing is deployed. `0001` and `0002` did ship in v3.0.0
  and v3.1.0, so such a database *could* exist; the scope decision above assumes
  none does.
