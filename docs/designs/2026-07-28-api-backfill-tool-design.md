# API Backfill Tool — Design

**Date:** 2026-07-28
**Status:** Approved
**Issue:** (none yet — sibling in spirit to #80, which is a separate tool, not a shared abstraction)

## Problem

The service persists observations it receives over UDP. Anything it misses — the process was down, the host rebooted, the container was rescheduled — is simply absent from the store, and nothing today can recover it. The Tempest REST API retains historical observations, so the data is available; there is just no way to get it in.

A related gap: `internal/sqlite/writer.go`'s `WriteMetrics` is a **documented no-op**, so the existing API-export mode (`ModeAPIExport`, triggered by `TOKEN`) writes to Postgres, OTel, and Prometheus but **never to SQLite** — which is the default store. Backfill built on that path would silently do nothing for most deployments.

## Goal

A `backfill` subcommand that finds holes in the local observation history and fills them from the Tempest REST API, idempotently and safely re-runnably.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| What to backfill | **Auto-detect gaps, with `--from`/`--to` override** | "Backfill missing data" should need no input in the common case; the override covers a known-bad window without a full scan. |
| How data reaches the store | **Typed API→row path** | `sqlite.WriteMetrics` is a no-op, so the `[]prometheus.Metric` export path cannot reach the default store. A typed path is lossless and works for SQLite and Postgres alike. |
| Invocation | **Subcommand on the existing binary** | `main.go:189` already dispatches `os.Args[1] == "healthcheck"`. Extending it needs no packaging change: the tool ships in the same image and release artifacts. #80 can later add `migrate` the same way, additively. |

## Scope

**In:** `tempest_observations` only.

**Out:** `tempest_rapid_wind`, `tempest_hub_status`, `tempest_events`. These are not retrievable historically — rapid wind is a 3-second live broadcast, hub status is device telemetry, and events are instantaneous pushes. The REST device-observations endpoint returns only the ~1/minute `obs_st` equivalent.

**Also out:** changing `ModeAPIExport`. Whether the legacy export path should be folded into this tool is a separate decision; this design leaves it untouched.

## Components

Each unit has one responsibility and is independently testable.

### `tempestapi.GetObservationRows(ctx, station, from, to) ([]Observation, error)` — new

Returns typed observations mirroring the `tempest_observations` columns, instead of `[]prometheus.Metric`.

The existing `GetObservations` is **not modified** — `ModeAPIExport` keeps using it. Two representations of an API observation will coexist (metrics for the legacy export, rows for backfill). This is accepted: they serve different consumers, and unifying them would require changing the legacy export path, which is out of scope.

### `FindObservationGaps(ctx, from, to, minGap) ([]Gap, error)` — new, on both stores

A single SQL pass using `LAG(timestamp) OVER (ORDER BY timestamp)` to return adjacent-row intervals wider than `minGap`. On SQLite it runs on the read-only handle, so a wide scan never queues behind the ingest writer.

### `InsertObservations(ctx, rows) (inserted int, err error)` — on both stores

Bulk idempotent insert, `ON CONFLICT DO NOTHING` against `UNIQUE(serial_number, timestamp)`. Returns the number of rows actually inserted so the tool can report `requested` vs `inserted`.

### `backfill` subcommand

Flag parsing, orchestration, progress output, dry-run. Writes to whichever store the daemon would use, reusing the existing `selectStore` logic — SQLite by default, Postgres when configured. No new configuration surface.

## Data flow

```
detect gaps (or use --from/--to)
   → chunk each gap into 1-day windows        [matches existing export pagination]
      → GetObservationRows(window)
         → InsertObservations(rows)           [ON CONFLICT DO NOTHING]
            → report: gap, requested, inserted, skipped
```

## The permanent-hole problem

If the station was genuinely offline, the API has no data for that window either. Auto-detect will therefore rediscover the same hole on every run and re-fetch it indefinitely — never converging.

**Decision: accept it, and make it visible.** Report `requested N, inserted M` per gap so a permanently empty window is obvious to the operator.

Rejected alternative: persisting a table of already-attempted windows. It adds schema and durable state to save only redundant API calls, and would itself become wrong if the API later fills in its own history.

`--min-gap` (default 30 minutes) keeps ordinary 1–2 minute reporting jitter from registering as a gap at all.

## Error handling

- A failed gap **logs and continues** to the next; one bad window must not abandon the run. The process **exits non-zero** if any gap failed, so automation notices.
- Honors context cancellation (SIGINT/SIGTERM) between windows. Already-inserted rows stay; idempotency makes re-running safe.
- `--dry-run` performs gap detection and prints the plan, making **zero** API calls and **zero** writes.
- API errors surface with station and window context. Unlike the current export path, backfill must **never** `log.Fatalf` mid-run — partial progress must be preserved and reported.

## Testing

| Area | Test |
|---|---|
| API parsing | Fixture JSON → `GetObservationRows` → assert typed fields, including null/missing array elements. No network. |
| Gap detection | Seed a temp SQLite DB with a known hole; assert exact gap boundaries. Plus no-gaps and empty-table cases. |
| Idempotency | Insert the same rows twice; the second run inserts 0 and changes nothing. |
| Dry-run | Asserts zero writes and zero API calls. |

## Constraints

- `CGO_ENABLED=0` stays buildable (pure-Go `modernc.org/sqlite`).
- Timestamps are UTC unix-epoch integers; primary keys are UUIDv7 generated in Go.
- Go 1.25 floor, toolchain go1.26.1.

## Non-goals

- Backfilling rapid wind, hub status, or events (not retrievable).
- Modifying `ModeAPIExport` or `sqlite.WriteMetrics`.
- Implementing #80's `migrate` subcommand — this only establishes the dispatch pattern it can slot into.
