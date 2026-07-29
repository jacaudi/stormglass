# API Backfill Tool — Design

**Date:** 2026-07-28 (revised 2026-07-29 after senior-Go-engineer review)
**Status:** Approved, revised
**Related:** #80 is a sibling *tool*, not a shared abstraction — no coupling between them.

## Problem

The service persists observations it receives over UDP. Anything it misses — the process was down, the host rebooted, the container was rescheduled — is simply absent from the store, and nothing today can recover it. The Tempest REST API retains historical observations, so the data is available; there is just no way to get it in.

## Goal

A `backfill` subcommand that finds holes in the local observation history and fills them from the Tempest REST API, idempotently and safely re-runnably.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| What to backfill | Auto-detect gaps, with `--from`/`--to` override | Should need no input in the common case; the override covers a known window without a full scan. |
| How data reaches the store | Dedicated typed API→row path | See "Why not reuse an existing write path" below — the reasoning is **not** simply that `WriteMetrics` is a no-op. |
| Invocation | Subcommand on the existing binary | `main.go:189` already dispatches `os.Args[1] == "healthcheck"`. Additive, no packaging change; #80's `migrate` slots in the same way. |

### Why not reuse an existing write path

Three candidates exist. All are rejected, for distinct reasons that must be recorded so an implementer does not "helpfully" reuse one:

- **`WriteMetrics` (`[]prometheus.Metric`)** — `sqlite.WriteMetrics` is a documented no-op (`internal/sqlite/writer.go:528-537`), so this path never reaches the default store.
- **`WriteReport` (typed, correct row construction)** — the real blocker is its **enqueue semantics**: the continuous-report path is non-blocking and **drops on saturation** (`internal/sqlite/writer.go:346-357`, channel cap 1000 at `:23`). A single 1-day window is ~1440 rows, so naive reuse silently loses rows. It also cannot return an inserted count, which the reporting below depends on.
- **`sqlite.Writer` methods generally** — `run` is documented as "the only goroutine that ever touches db" (`internal/sqlite/writer.go:161,236`). Adding an insert method to that type would breach the single-writer invariant and be callable from the daemon.

**Therefore:** `FindObservationGaps` and `InsertObservations` are **package-level functions taking `*sql.DB` / `*pgxpool.Pool`**, not methods on the writer types. They start no goroutines.

## Scope

**In:** `tempest_observations` only.

**Out:** `tempest_rapid_wind`, `tempest_hub_status`, `tempest_events` — no historical REST endpoint exists for any of them (rapid wind is UDP/WebSocket-only; hub/device status and discrete strike/rain-start events are push-only). Note lightning is *partially* recovered in aggregate via `obs[14]`/`obs[15]` (avg distance, strike count per interval), which populate observation columns — but not as `tempest_events` rows.

**Also out:** changing `ModeAPIExport` or `sqlite.WriteMetrics`.

## Data model

### `Observation` and `Gap` ownership

Both stores must name these types, so they cannot live in `tempestapi` (storage would depend on the REST client). They also must not collide with the existing `sqlite.Observation` read-model (`internal/sqlite/writer.go:712`).

**Decision:** they live in **`internal/tempestudp`** — the neutral package both stores already import. Each store converts to its own private `observationRow` at its boundary (SQLite's timestamp is `int64`, Postgres's is `time.Time`; their pointer types differ).

`Gap` carries `SerialNumber`, `From`, `To`, using `time.Time` as the canonical representation.

### Nullability — mandatory

`GetObservationRows` **must** decode the `obs` array as `[][]*float64` (or `[]json.RawMessage`), mapping JSON `null` → nil pointer → SQL NULL.

It **must not** reuse `tempestudp.TempestObservationReport`, whose `Obs` is `[][]float64` (`internal/tempestudp/report.go:135`): unmarshalling `null` into a non-pointer numeric is a silent no-op that yields `0.0`. That would write `battery = 0.0 V`, `pressure = 0.0 mb` where the API said "unknown" — physically meaningful values that `SummarizeObservations`' `MIN(pressure)` and every chart read as real. Backfill operates precisely on marginal windows, where nulls are most likely.

### Derived columns

`temp_wetbulb` is **not** returned by the API — it is computed in Go at ingest from `ob[7]/ob[8]/ob[6]` with a NaN→NULL guard (`internal/sqlite/writer.go:410,432-434`).

`GetObservationRows` returns **raw API fields only**. Wet bulb is derived at the store boundary using the same `tempestudp.WetBulbTemperatureC` + `math.IsNaN` guard the ingest path uses, single-sourced — so backfilled and live rows are indistinguishable. (Change the formula and all call sites change together: shared knowledge, not shared shape.)

### Field alignment

REST `obs_st` indices 0–17 match the UDP layout exactly, so the existing `len(ob) >= N` guards apply unmodified. The REST array has 22 elements; indices 18–21 (local-day rain accumulation, Nearcast accumulations, precip analysis type) map to no existing column and are ignored deliberately.

## Gap detection

### Partitioned by serial — mandatory

```sql
-- SQLite (timestamp INTEGER, epoch seconds)
SELECT serial_number, prev, timestamp FROM (
  SELECT serial_number,
         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
         timestamp
  FROM tempest_observations
  WHERE timestamp BETWEEN ? AND ?
) WHERE prev IS NOT NULL AND timestamp - prev > ?
```

```sql
-- Postgres (timestamp TIMESTAMPTZ)
SELECT serial_number, prev, ts FROM (
  SELECT serial_number,
         LAG(timestamp) OVER (PARTITION BY serial_number ORDER BY timestamp) AS prev,
         timestamp AS ts
  FROM tempest_observations
  WHERE timestamp BETWEEN $1 AND $2
) q WHERE prev IS NOT NULL AND EXTRACT(EPOCH FROM (ts - prev)) > $3
```

**`PARTITION BY serial_number` is not optional.** The series is identified by `(serial_number, timestamp)` — the same uniqueness contract idempotency relies on. Without partitioning, two stations phase-offset by ~30s produce a merged sequence with no interval ever exceeding `--min-gap`, so a multi-hour outage on one station is **undetectable** and the tool reports "no gaps" and exits 0. The same failure hides a hardware swap (new serial) behind an apparently continuous sequence.

**Required test:** two serials whose interleaved timestamps mask each other.

### Detection domain — head, tail, and empty

`LAG` yields NULL for the first row of each partition, so it finds **interior gaps only**. Left there, a fresh/empty store reports "no gaps" and writes nothing, and the natural "the box was down, repair it" case — whose outage is entirely in the tail — is invisible.

The detection domain is explicit:

- `detectTo` defaults to `now - minGap`.
- `detectFrom` defaults to the station's `created_epoch` from `ListStations` (`internal/tempestapi/client.go:123`).
- Gaps are the union of: `[detectFrom, first_row]`, the interior `LAG` gaps, and `[last_row, detectTo]`.
- **Empty table:** the whole `[detectFrom, detectTo]` range is one gap. This is a first-class case, not an edge case.

## Fetch and insert

### Chunking

All fetches — auto-detected **and** `--from/--to` — go through the chunker. The constraint is the API's documented cap: *observation data at one-minute resolution is available only for ranges of five days or less* ([apidocs.tempestwx.com](https://apidocs.tempestwx.com/reference/getobservationsbydeviceid)). Chunk size is **1 day**, comfortably inside it. Exceeding the cap silently returns coarser data that would be written as if it were 1-minute observations.

**Required test:** a multi-day `--from/--to` produces N single-day requests.

### Empty-window handling

`GetObservationRows` must check `status.status_code` (as `ListStations` does, `client.go:102-104`; `GetObservations` currently does not), distinguish "no data" from a real error, treat an absent/null `obs` array as **zero rows, not an error**, and must **not** route through `tempestudp.ParseReport`, whose type dispatch errors with `unhandled message type: ""` on a `status`-only envelope.

**Resolved from the published OpenAPI spec** ([swagger.json](https://weatherflow.github.io/Tempest/api/swagger/swagger.json)), which settles this without a token:

- The `ObservationSet` response includes a `status` object → `status_code` (`integer/int32`, example `0`) and `status_message` (`string`, example `"SUCCESS"`). So `status_code == 0` is the success signal, and checking it is correct.
- **`ObservationSet` declares no `required` array.** Neither `type` nor `obs` is required, so a response omitting either is schema-legal. This confirms the hazard directly: routing through `tempestudp.ParseReport` — which dispatches on a top-level `type` and errors `unhandled message type: ""` when absent — would turn a legitimate empty window into a hard error.
- Reported in the field: `status_code` 0 with `status_message` `"SUCCESS - Either no capabilities or no recent observations"` and an empty/absent `obs` ([Tempest community](https://community.tempest.earth/t/getting-station-data/17879)).

**Therefore, treat as normative:** `status_code == 0` is success; an absent, `null`, or empty `obs` is **zero rows, not an error**; an absent `type` is **not** an error. A non-zero `status_code` is a real failure carrying `status_message`.

*Remaining (non-blocking) verification:* pin a real empty-window body as a fixture when a token is available, to confirm the exact serialization. The design no longer depends on that answer — the schema establishes that both fields are optional, which is the property the handling must be robust to either way.

### Retry

Bounded exponential backoff on 429/5xx/network errors, honoring `Retry-After` when present, plus a small inter-request delay. A gap is marked failed only after retries are exhausted. Context cancellation is checked between windows **and** between retry attempts. WeatherFlow publishes no rate limits, which argues for conservatism rather than for ignoring the question.

### Insert

`InsertObservations(ctx, db, rows) (inserted int, err error)` — `ON CONFLICT (serial_number, timestamp) DO NOTHING`, matching both existing insert paths (`internal/sqlite/writer.go:631`, `internal/postgres/writer.go:255`).

The inserted count is obtainable on both stores and is the mechanism that makes the permanent-hole tradeoff visible: SQLite's per-row `stmt.ExecContext` returns `RowsAffected` 0 for a skipped conflict and 1 for an insert; Postgres's `br.Exec()` returns a `pgconn.CommandTag` with the same semantics (currently discarded at `internal/postgres/writer.go:268`). Both need a signature change, neither a semantics change.

The count is reported **only after a successful `Commit`** — `execBatch` rolls the whole transaction back on any row error (`internal/sqlite/writer.go:589-620`), and pgx `SendBatch` is all-or-nothing per batch.

Insert transactions are bounded to **≤200 rows** (see concurrency below). If `InsertObservations` reuses the Postgres batch helper, it needs its own timeout: the existing hardcoded 5s (`internal/postgres/writer.go:240`) was sized for the 1-row live path.

## Concurrency with a running daemon

The realistic invocation is `docker exec <running container> tempestwx-utilities backfill` against a live `/data/tempest.db`. **This is supported**, with these consequences stated deliberately:

- A long backfill transaction contends with ingest writes; `busy_timeout` defaults to 5s, and ingest's error path only **logs** (`internal/sqlite/writer.go:646`). Unbounded transactions could therefore cause **live observations to be silently lost while repairing historical ones**. Bounding inserts to ≤200 rows per transaction is the mitigation.
- Litestream owns checkpointing; a second writer process is compatible with it.
- Both processes run `Migrate` on open; per-migration transactions make this safe in practice, though `schema_version` has no uniqueness constraint.

## Lifecycle

- `runBackfill(ctx) int` performs **all** cleanup via internal defers and returns an exit code; the dispatch site does `os.Exit(runBackfill(ctx))`. Copying the healthcheck shape (`os.Exit` at the dispatch site with no cleanup, `main.go:189-191`) would skip `db.Close()` and the pgx pool drain.
- Signal context (SIGINT/SIGTERM) is wired in the subcommand; the healthcheck path has none to inherit.
- Store selection reuses `selectStore` (`main.go:147-154`), which is genuinely pure — no I/O, no goroutines, no globals. Everything *downstream* of it is currently inline in `main()` (`main.go:256-336`) and entangled with HTTP-server and sink teardown; backfill constructs its own handles rather than reusing that block.
- Backfill opens the **write** handle (it must migrate and insert). `OpenReadOnly` is not used: it fails when the file does not exist and cannot migrate, and its ingest-contention rationale does not apply to a separate one-shot process.

## Serial-number invariant

Dedupe, gap closure, and convergence all require that the serial written by backfill exactly matches the serial written by UDP ingest. Backfill's comes from `ListStations` (`client.go:110-115`), ingest's from the broadcast field. If they ever diverge, backfill writes a **parallel series under a second serial**, `UNIQUE` never fires, rows double, and the gap never closes — silently and cumulatively.

**Pre-flight check:** compare `ListStations` serials against `SELECT DISTINCT serial_number FROM tempest_observations` and warn on mismatch. Also note `client.go:110-115` has no `break`, so with multiple `ST` devices the last silently wins — backfill must handle multiple devices explicitly rather than inherit that.

## The permanent-hole problem

If the station was genuinely offline, the API has no data for that window either. Auto-detect rediscovers the same hole on every run and never converges.

**Decision: accept it, and make it visible.** Report `requested N, inserted M` per gap, plus a machine-greppable summary line (`gaps=3 requested=4320 inserted=0`) so automation can detect non-convergence. Exit code stays 0 — a permanent hole is not an error.

Rejected: persisting a table of attempted windows. It adds schema and durable state to save only redundant API calls, and goes stale if the API later fills its own history.

`--min-gap` (default 30m) keeps ordinary reporting jitter from registering. Stations configured with a long `report_interval` need a larger value.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--from`, `--to` | unset (auto-detect) | **RFC3339, interpreted UTC.** The store is UTC epoch and the API takes epoch seconds; an ambiguous local-time parse is a quiet wrong-window bug. |
| `--min-gap` | `30m` | |
| `--dry-run` | false | Gap detection + plan only: **zero** API calls, **zero** writes. Consequently it cannot validate the token or reachability. |

## Error handling

A failed gap logs and continues; the process exits **non-zero** if any gap failed. Context cancellation is honored between windows and retries, leaving inserted rows intact — idempotency makes re-running safe. Backfill must **never** `log.Fatalf` mid-run: partial progress must be preserved and reported.

## Testing

| Area | Test |
|---|---|
| Nullability | Fixture with `null` obs elements → assert row pointer fields are **nil**, not zero. |
| Gap detection — partitioning | **Two serials with interleaved timestamps that mask each other.** Regression test for C1. |
| Gap detection — domain | Head gap, tail gap, empty table, no-gaps. |
| Chunking | Multi-day range → N single-day requests. |
| Empty window | Three fixtures, all → zero rows + no error: `obs` empty, `obs` null/absent, and a status-only envelope with no `type`. |
| Idempotency | Insert twice; second inserts 0, changes nothing. |
| Dry-run | Zero writes, zero API calls. |
| Serial pre-flight | Mismatched serials → warning. |

## Constraints

- `CGO_ENABLED=0` stays buildable. `modernc.org/sqlite` bundles SQLite 3.46.0; `LAG(...) OVER (PARTITION BY ...)` verified working under it.
- Timestamps UTC epoch; PKs UUIDv7 generated in Go. Note UUIDv7 embeds *insert* time, so backfilled rows sort by `id` in insert order, not observation order — consistent with the ingest path, but `id` ordering must not be assumed chronological.
- Go 1.25 floor, toolchain go1.26.1.

## Non-goals

- Backfilling rapid wind, hub status, or events.
- Modifying `ModeAPIExport` or `sqlite.WriteMetrics`.
- Implementing #80's `migrate` — this only establishes the dispatch pattern it slots into.
