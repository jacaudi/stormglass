# API Backfill Tool — Design

**Date:** 2026-07-28 (revised 2026-07-29 after two senior-Go-engineer reviews)
**Status:** Approved, revised — plan-ready
**Related:** #80 is a sibling *tool*, not a shared abstraction — no coupling between them.

## Problem

The service persists observations it receives over UDP. Anything it misses — the process was down, the host rebooted, the container was rescheduled — is simply absent from the store, and nothing today can recover it. The Tempest REST API retains historical observations, so the data is available; there is just no way to get it in.

## Goal

A `backfill` subcommand that finds holes in the local observation history and fills them from the Tempest REST API, idempotently and safely re-runnably.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| What to backfill | Auto-detect gaps, with `--from`/`--to` override | Should need no input in the common case. |
| How data reaches the store | Dedicated typed API→row path | See "Why not reuse an existing write path" — the reasoning is **not** simply that `WriteMetrics` is a no-op. |
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

## Prerequisite API additions

Two APIs the design depends on **do not exist in the tree**. They are in scope for this work and must be built before the code that consumes them.

### 1. Station serial accessor — `internal/tempestapi`

`Station.serialNumber` and `Station.deviceID` are **unexported** (`internal/tempestapi/client.go:55-56`), so the serial pre-flight check cannot read a serial from outside the package.

**Decision: export the fields** (`SerialNumber`, `DeviceID`) rather than add accessor methods. `Station` is already a plain data struct with three exported fields and no invariants to protect; adding two getters for a value type would be ceremony (KISS). Update the four construction/read sites inside the package.

### 2. Pool constructor — `internal/postgres`

There is **no way to obtain a `*pgxpool.Pool`**. `NewPostgresWriter` is the only constructor (`writer.go:144`), and it also pings, runs `CreateSchema`, and starts **four** background goroutines (`writer.go:190-199`) — all of which this design forbids for a one-shot tool.

**Decision:** add

```go
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)
```

holding the pool contract (`MaxConns=10`, `MinConns=2`, `MaxConnLifetime=time.Hour`, `MaxConnIdleTime=10m`, `HealthCheckPeriod=30s`) and the ping. `NewPostgresWriter` is refactored to **call it**, so the tuning lives in exactly one place rather than being copied out of `writer.go:151-155`. This is DRY on a genuine shared contract: change a pool setting and both callers must change together.

`OpenPool` does **not** run `CreateSchema` — backfill calls `CreateSchema` explicitly, so schema creation stays an observable step of the caller rather than a side effect of opening a pool.

### 3. Device-level lookup — `internal/tempestapi`

`ListStations` collapses each station to a single `ST` device (see "Serial-number invariant"), so there is no way to enumerate every sensor on the account.

**Decision:** add

```go
func (c *Client) ListDevices(ctx context.Context) ([]Station, error)
```

returning **one `Station` per `ST` device** across all stations — same struct, but `DeviceID`/`SerialNumber` are per device rather than per station, and a station with two Tempests yields two entries. `CreatedAt` carries the owning station's `created_epoch`, which is what `detectFrom` needs.

`ListStations` is **not** modified: `ModeAPIExport` depends on its current one-per-station shape. The two share the response-decoding struct so the JSON contract lives in one place.

## Entry point and testability

The subcommand is split into a **shell** and a **testable core**. A single `runBackfill(ctx) int` that reads flags, `TOKEN`, store config, and `time.Now()` internally cannot be driven from a test, which would make the mandated dry-run, head/tail/empty-domain, and serial pre-flight tests unwritable — a direct conflict with TDD.

```go
// Shell: parses os.Args[2:], reads env, opens handles, wires deps, returns an
// exit code. Thin enough to need no unit test of its own.
func runBackfill(ctx context.Context, args []string) int

// Core: no env, no clock, no I/O construction. Fully testable.
func backfill(
    ctx   context.Context,
    cfg   backfillConfig,
    client observationSource,
    store  backfillStore,
    now   time.Time,
) (backfillStats, error)
```

- **`now` is injected.** Nothing below the shell calls `time.Now()`. `detectTo = now - minGap` is then deterministic in tests.
- **`backfillStore` is the one earned interface** — two concrete implementors exist on day one (SQLite and Postgres), so it is not speculative:

  ```go
  type backfillStore interface {
      DistinctSerials(ctx context.Context) ([]string, error)
      SeriesBounds(ctx context.Context, from, to time.Time) ([]weather.Bounds, error)
      FindObservationGaps(ctx context.Context, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
      InsertObservations(ctx context.Context, obs []weather.Observation) (inserted int, err error)
  }
  ```

  `SeriesBounds` returns the first and last stored timestamp per serial **within a
  window**, supplying the head/tail boundaries `LAG` cannot see.

  `DistinctSerials` is **unwindowed** — `SELECT DISTINCT serial_number FROM
  tempest_observations`, whole table — and exists solely for the pre-flight
  check.

  **These must not be merged.** An earlier revision of this design collapsed
  `DistinctSerials` into `SeriesBounds` on the reasoning that "its key set *is*
  the set of serials present." That reasoning silently assumed an unwindowed
  query. `SeriesBounds` is windowed, so a station with no rows *inside the
  queried window* looks absent from the store entirely — and the pre-flight
  check then hard-stops on a station that is merely quiet, writing nothing,
  including for the healthy stations. That is precisely what `--from/--to`
  exists to do, so the merge broke the tool's main repair path. Keep them
  separate.

  Defined in the **consumer** package (`internal/backfill`), Go-idiomatic. Thin adapters in that package bind a `*sql.DB` / `*pgxpool.Pool` to the package-level store functions.

- **`observationSource`** is a one-method consumer-side interface over the API client, so the core tests need no HTTP at all. (`tempestapi.WithBaseURL`, `client.go:35-39`, remains available for tests that *do* want to exercise the real decode path against an `httptest.Server`.)

### Subcommand dispatch

Today `main()` matches only `healthcheck` and **falls through to daemon mode** for anything else — so `tempestwx-utilities backfil` (typo) silently starts a UDP listener.

**Decision:**

- `main` matches `os.Args[1]` against a known set: `healthcheck`, `backfill`.
- Any **other non-flag** first argument prints usage to stderr and exits **2**. No silent fallthrough.
- **Each subcommand owns its own `flag.FlagSet`, parsed from `os.Args[2:]`.** This — not an abstraction — is the No-Wall seam #80's `migrate` slots into: a new subcommand is a new file plus one dispatch line, and no sibling is edited.

**Standards divergence (recorded, not resolved):** `go-standards` §6 says "all CLIs use Cobra" with `cmd/<binary>/main.go`. This project uses a hand-rolled `os.Args[1]` dispatch with a `main.go` at the root. We keep the established pattern: adding Cobra for two subcommands would be a dependency and a framework where a `switch` suffices (KISS), and the standard defers to established project patterns. Noted so the divergence is deliberate rather than overlooked.

## Data model

### `Observation` and `Gap` ownership

Both stores must name these types, so they cannot live in `tempestapi` (storage would depend on the REST client). They must not collide with the existing `sqlite.Observation` read-model (`internal/sqlite/writer.go:712`).

**Decision:** a new leaf package **`internal/weather`** owns `Observation` and `Gap`.

`internal/tempestudp` — the earlier choice — is **wrong**: that package is the UDP wire protocol (`CLAUDE.md:78`), and `tempestudp.Gap` reads as "a gap in UDP," which is false. The original justification was import convenience, not cohesion.

`Gap` must live in `internal/weather` **with** `Observation`, not in `internal/backfill` as first proposed: `FindObservationGaps` is a package-level function in `internal/sqlite` and `internal/postgres`, so putting `Gap` in the consumer would force the store packages to import `internal/backfill` — an inverted dependency — or duplicate the type.

`internal/weather` imports nothing from this repo. `tempestapi`, `sqlite`, `postgres`, and `backfill` all import it; no cycles.

`Gap` carries `SerialNumber`, `From`, `To`, using `time.Time` as the canonical representation.

**The interval is CLOSED `[From, To]`, not half-open.** Every producer emits endpoints that are rows which already exist — `prev`/`next` from `LAG`, `First`/`Last` from `SeriesBounds` — so both ends are re-fetched and re-offered to the store. `ON CONFLICT DO NOTHING` absorbs the duplicates, so this is harmless to correctness, but it must be documented as closed: labelling it `[From, To)` would be a lie, and it explains why `Requested` slightly exceeds the number of genuinely missing observations. Adjacent chunk windows likewise share an endpoint, for the same reason and with the same absorption.

#### Accepted duplication (DRY, explicit)

This adds a **fourth** in-tree representation of an observation row (`tempestudp.TempestObservationReport`, `sqlite`'s private `observationRow`, `postgres`'s private `observationRow`, now `weather.Observation`). That is accepted, and the reason is recorded so it is not "cleaned up" later:

The store row types are **not** interchangeable — SQLite's timestamp is `int64` epoch and Postgres's is `time.Time`, and their nullable numeric pointer types differ. Unifying them would mean an abstraction over two genuinely different storage encodings, i.e. the wrong abstraction.

**The cost is real and stated:** adding an observation column touches ~4 sites plus the migration. That is the accepted price; the alternative (a generic row abstraction) is worse. **Do not extract one.**

### Nullability — mandatory

The API decode **must** unmarshal the `obs` array as **`[][]*float64`**, mapping JSON `null` → nil pointer → SQL NULL. (The `[]json.RawMessage` alternative floated earlier is dropped — one rule, no fork.)

It **must not** reuse `tempestudp.TempestObservationReport`, whose `Obs` is `[][]float64` (`internal/tempestudp/report.go:135`): unmarshalling `null` into a non-pointer numeric is a silent no-op that yields `0.0`. That would write `battery = 0.0 V`, `pressure = 0.0 mb` where the API said "unknown" — physically meaningful values that `SummarizeObservations`' `MIN(pressure)` and every chart read as real. Backfill operates precisely on marginal windows, where nulls are most likely.

### Derived columns

`temp_wetbulb` is **not** returned by the API — it is computed in Go at ingest from `ob[7]/ob[8]/ob[6]` with a NaN→NULL guard (`internal/sqlite/writer.go:410,432-434`).

The API method returns **raw API fields only**. Wet bulb is derived at the store boundary using the same `tempestudp.WetBulbTemperatureC` + `math.IsNaN` guard the ingest path uses, single-sourced — so backfilled and live rows are indistinguishable. (Change the formula and all call sites change together: shared knowledge, not shared shape.)

### Field alignment

REST `obs_st` indices 0–17 match the UDP layout exactly, so the existing `len(ob) >= N` guards apply unmodified. The REST array has 22 elements; indices 18–21 (local-day rain accumulation, Nearcast accumulations, precip analysis type) map to no existing column and are ignored deliberately.

**"Unmodified" means graduated, not all-or-nothing.** The UDP path accepts a tuple at `len(ob) >= 13` and fills the tail conditionally (`>= 6`, `>= 14`, `>= 16`, `>= 17`, `>= 18` — `sqlite/writer.go:406-457`). A single hard `len(ob) < 18 → drop` is **not** the same rule: it discards a short tuple's core measurements entirely. Because every `weather.Observation` measurement is a `*float64`, honoring the graduated guards is free — absent indices simply stay nil.

**Dropped tuples must be counted and logged at WARN, per window, by the decode itself.** A tuple that *is* rejected (below the floor, or a null `ob[0]` that cannot be keyed) must appear in the log with a count and the serial. Otherwise a window whose tuples were all malformed reports `returned=0, inserted=0` — byte-identical to the permanent-hole signal, which is the one machine-readable diagnostic the whole reporting design rests on.

The count is deliberately **not** threaded into the run summary: doing so would widen the `ObservationSource` seam between backfill and the REST client to carry a diagnostic, and the log stream is already the machine-readable surface this design chose when it cut the bespoke summary line.

Note the summary field is named `returned`, not `requested`: it counts what the API handed back after drops. The closed gap interval and the shared chunk-window endpoints both mean a few observations are fetched more than once, so it is not a count of distinct missing observations.

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

### Signatures — pinned

Both stores expose the **same** signature in `time.Time`, so the interface above is satisfiable without a shim:

```go
// internal/sqlite   — converts time.Time ↔ int64 epoch internally
func FindObservationGaps(ctx context.Context, db *sql.DB, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
// internal/postgres — passes time.Time through
func FindObservationGaps(ctx context.Context, pool *pgxpool.Pool, from, to time.Time, minGap time.Duration) ([]weather.Gap, error)
```

The epoch conversion stays at the SQLite boundary, matching how that package already handles timestamps.

### SQLite connection limit — must not stream

`sqlite.Open` sets `db.SetMaxOpenConns(1)` (`internal/sqlite/db.go:73`) — a single writer connection, by design.

**Therefore `FindObservationGaps` must fully materialize its `[]weather.Gap` and close its `*sql.Rows` before any insert runs.** The specification above already does this. A "nicer" streaming iterator that yielded gaps while the caller inserted would **deadlock on the single connection with no error and no timeout** — it would simply hang. This constraint is load-bearing; do not refactor it away.

### Detection domain — head, tail, and empty

`LAG` yields NULL for the first row of each partition, so it finds **interior gaps only**. Left there, a fresh/empty store reports "no gaps" and writes nothing, and the natural "the box was down, repair it" case — whose outage is entirely in the tail — is invisible.

The detection domain is explicit:

- `detectTo` defaults to `now - minGap` (`now` injected, see entry point).
- `detectFrom` defaults to the station's `created_epoch` from `ListStations` (`internal/tempestapi/client.go:123`).
- Gaps are the union of: `[detectFrom, first_row]`, the interior `LAG` gaps, and `[last_row, detectTo]`.
- **Empty table:** the whole `[detectFrom, detectTo]` range is one gap. This is a first-class case, not an edge case.

## Fetch and insert

### API method

```go
func (c *Client) Observations(ctx context.Context, station Station, start, end time.Time) ([]weather.Observation, error)
```

Named `Observations`, not `GetObservationRows`: Go getters take no `Get` prefix, and "Rows" is storage vocabulary that does not belong in a REST client. It sits alongside the existing `GetObservations` (which returns `[]prometheus.Metric` for `ModeAPIExport` and is untouched).

### Chunking

All fetches — auto-detected **and** `--from/--to` — go through the chunker. The constraint is the API's documented cap: *observation data at one-minute resolution is available only for ranges of five days or less* ([apidocs.tempestwx.com](https://apidocs.tempestwx.com/reference/getobservationsbydeviceid)). Chunk size is **1 day**, comfortably inside it. Exceeding the cap silently returns coarser data that would be written as if it were 1-minute observations.

**Required test:** a multi-day `--from/--to` produces N single-day requests.

### Empty-window handling

`Observations` must check `status.status_code` (as `ListStations` does, `client.go:102-104`; `GetObservations` currently does not), distinguish "no data" from a real error, treat an absent/null `obs` array as **zero rows, not an error**, and must **not** route through `tempestudp.ParseReport`, whose type dispatch errors with `unhandled message type: ""` on a `status`-only envelope.

**Resolved from the published OpenAPI spec** ([swagger.json](https://weatherflow.github.io/Tempest/api/swagger/swagger.json)), which settles this without a token:

- The `ObservationSet` response includes a `status` object → `status_code` (`integer/int32`, example `0`) and `status_message` (`string`, example `"SUCCESS"`). So `status_code == 0` is the success signal, and checking it is correct.
- **`ObservationSet` declares no `required` array.** Neither `type` nor `obs` is required, so a response omitting either is schema-legal. This confirms the hazard directly.
- Reported in the field: `status_code` 0 with `status_message` `"SUCCESS - Either no capabilities or no recent observations"` and an empty/absent `obs` ([Tempest community](https://community.tempest.earth/t/getting-station-data/17879)).

**Therefore, treat as normative:** `status_code == 0` is success; an absent, `null`, or empty `obs` is **zero rows, not an error**; an absent `type` is **not** an error. A non-zero `status_code` is a real failure carrying `status_message`.

*Remaining (non-blocking) verification:* pin a real empty-window body as a fixture when a token is available. The design no longer depends on that answer.

### Error taxonomy

Three behaviors branch on the *kind* of error — retry on 429/5xx/network, treat non-zero `status_code` as a real failure, exit non-zero if any gap failed. Without a typed error, the retry layer could only string-match.

```go
// internal/tempestapi
type StatusError struct {
    HTTPStatus int    // 0 when the failure is an API-level status_code
    StatusCode int    // WeatherFlow status.status_code; 0 when purely HTTP
    Message    string // status.status_message
}
func (e *StatusError) Error() string
```

- Classification uses **`errors.As`** — **not `errors.AsType`**, which is Go 1.26 and `go.mod` declares `go 1.25.0`.
- Retryable: `HTTPStatus == 429`, `HTTPStatus >= 500`, or a network error (`net.Error`). A non-zero `StatusCode` is **not** retryable — it is a real API-level failure.
- Per-gap failures accumulate with **`errors.Join`**; the joined error drives the non-zero exit.

#### The per-attempt-timeout trap — mandatory

`http.Client.Timeout` is **implemented as a context deadline**. The client sets a 30s timeout (`client.go:42`), and the `*url.Error` it produces satisfies **both** `errors.Is(err, context.DeadlineExceeded)` **and** `errors.As(err, &netErr)`:

```
Get "...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
errors.Is(err, context.DeadlineExceeded) = true
errors.As(err, &net.Error)              = true
```

So a classifier that opens with a blanket `if errors.Is(err, context.DeadlineExceeded) { return false }` guard marks **every per-attempt HTTP timeout as non-retryable**, and the `net.Error` branch is never reached. One slow WeatherFlow response then fails an entire gap on the first try with zero retries — the single most likely transient failure in a tool issuing thousands of sequential requests.

**Therefore:**

- Only **`context.Canceled`** is a blanket non-retryable signal.
- A deadline error is fatal only when the **parent** context is actually done — check `ctx.Err() != nil` at the call site, not the error's identity.
- A per-attempt timeout falls through to the `net.Error` branch and **is** retried.
- The regression test must be built from a **real** `*url.Error` (an `httptest.Server` that stalls, plus a short-timeout client), not a synthetic `net.Error` fake. A fake cannot reproduce the dual-predicate error that causes the bug, so a test using one will pass against the broken classifier.

### Retry

Bounded exponential backoff on retryable errors. A gap is marked failed only after retries are exhausted. Context cancellation is checked between windows **and** between retry attempts.

**Cut:** `Retry-After` parsing and the fixed inter-request delay. WeatherFlow publishes no rate limits, so both were guesses at a constraint we cannot see; bounded backoff already handles 429 correctly. Add either if a real 429 is ever observed (YAGNI — no present consumer).

### Insert

```go
func InsertObservations(ctx context.Context, db *sql.DB, obs []weather.Observation) (inserted int, err error)
```

`ON CONFLICT (serial_number, timestamp) DO NOTHING`, matching both existing insert paths (`internal/sqlite/writer.go:631`, `internal/postgres/writer.go:255`).

The inserted count is obtainable on both stores and is the mechanism that makes the permanent-hole tradeoff visible: SQLite's per-row `stmt.ExecContext` returns `RowsAffected` 0 for a skipped conflict and 1 for an insert; Postgres's `br.Exec()` returns a `pgconn.CommandTag` with the same semantics (currently discarded at `internal/postgres/writer.go:268`). Both need a signature change, neither a semantics change.

The count is reported **only after a successful `Commit`** — `execBatch` rolls the whole transaction back on any row error (`internal/sqlite/writer.go:589-620`), and pgx `SendBatch` is all-or-nothing per batch.

Insert transactions are bounded to **≤200 rows** (see concurrency below). If `InsertObservations` reuses the Postgres batch helper, it needs its own timeout: the existing hardcoded 5s (`internal/postgres/writer.go:240`) was sized for the 1-row live path.

## Concurrency with a running daemon

The realistic invocation is `docker exec <running container> tempestwx-utilities backfill` against a live `/data/tempest.db`. **This is supported**, with these consequences stated deliberately:

- A long backfill transaction contends with ingest writes; `busy_timeout` defaults to 5s, and ingest's error path only **logs** (`internal/sqlite/writer.go:646`). Unbounded transactions could therefore cause **live observations to be silently lost while repairing historical ones**. Bounding inserts to ≤200 rows per transaction is the mitigation.
- Litestream owns checkpointing; a second writer process is compatible with it.
- Both processes run `Migrate` on open; per-migration transactions make this safe in practice, though `schema_version` has no uniqueness constraint.

## Lifecycle

- `runBackfill(ctx, args) int` performs **all** cleanup via internal defers and returns an exit code; the dispatch site does `os.Exit(runBackfill(ctx, os.Args[2:]))`. Copying the healthcheck shape (`os.Exit` at the dispatch site with no cleanup, `main.go:189-191`) would skip `db.Close()` and the pgx pool drain.
- Signal context (SIGINT/SIGTERM) is wired in the subcommand; the healthcheck path has none to inherit.
- **Missing `TOKEN` fails fast** — validated in the shell *before* any store handle is opened, so the failure costs no I/O and leaves nothing to close.
- Store selection reuses `selectStore` (`main.go:147-154`), which is genuinely pure — no I/O, no goroutines, no globals. Everything *downstream* of it is currently inline in `main()` (`main.go:256-336`) and entangled with HTTP-server and sink teardown; backfill constructs its own handles rather than reusing that block.
- Backfill opens the **write** handle (it must migrate and insert). `OpenReadOnly` is not used: it fails when the file does not exist and cannot migrate, and its ingest-contention rationale does not apply to a separate one-shot process.
- **Logging: `log/slog` for all new code.** Structured attrs are also what makes progress machine-readable (see below). The existing `log.Printf` call sites are untouched.

## Serial-number invariant

Dedupe, gap closure, and convergence all require that the serial written by backfill exactly matches the serial written by UDP ingest. Backfill's comes from `ListStations` (`client.go:110-115`), ingest's from the broadcast field. If they ever diverge, backfill writes a **parallel series under a second serial**, `UNIQUE` never fires, rows double, and the gap never closes — silently and cumulatively.

**Pre-flight check — hard stop, not a warning.** Compare `ListStations` serials against `SELECT DISTINCT serial_number FROM tempest_observations`. On mismatch, log the offending serials and **exit non-zero having written nothing.** Warning-then-writing-anyway was the earlier spec and is incoherent: it names an outcome as corrupting and then produces it. The check only earns its place if it prevents the damage.

**Mismatch means the two serial sets are DISJOINT on a non-empty store** — no API serial appears in the store at all. That is the actual signature of format divergence, and it is the only condition that stops the run. The comparison uses `DistinctSerials` (whole table, unwindowed), never `SeriesBounds`.

The tempting-but-wrong rule is "*some* API serial is absent from a non-empty store." Two ordinary, non-corrupt situations satisfy it, and both would brick the tool:

- **A second station the UDP listener never hears** (different VLAN or subnet). Its serial will *never* enter the store, so backfill would refuse to run — including for the healthy station.
- **A newly added station.** Its first backfill would exit non-zero having written nothing, permanently, until the daemon happened to ingest a row from it.

Neither is serial-format divergence. Under the disjoint rule both proceed normally, and a serial the API knows but the store has not yet seen simply yields a whole-range gap — which is exactly what the head/tail/empty detection domain is for.

An **empty** store is likewise not a mismatch: it is the first-run case, and every gap is legitimately new.

**Multiple `ST` devices — resolved by a new device-level lookup.** `client.go:110-115` has no `break`, so within one station the last `ST` device silently wins, and `ListStations` returns one already-collapsed `Station` per station. Backfill therefore **cannot** "iterate all `ST` devices returned by `ListStations`" — an earlier revision of this design said exactly that, and it is unimplementable: there is nothing to iterate.

The consequence of inheriting the behavior is silent data loss. With two Tempest units on one station the UDP listener records both serials, but backfill only ever learns the last one; the other unit's gaps never close, and nothing is logged. It also evades the pre-flight check, which tests API⊄store, whereas this is the store⊄API direction.

**Decision:** add `tempestapi.ListDevices`, returning **one entry per `ST` device** across all stations, and have backfill enumerate that. `ListStations` is left untouched — `ModeAPIExport` depends on its current behavior, and fixing its missing `break` remains a non-goal.

> **Scope note (No-Wall, explicitly authorized).** The maintainer runs a single Tempest, so by the seam test this serves no present consumer and would normally be deferred. The seam test was **explicitly overridden for this feature only**, on the grounds that the tool is published and a multi-sensor user is a plausible request. Recorded so the exception stays visible and does not become precedent.

## The permanent-hole problem

If the station was genuinely offline, the API has no data for that window either. Auto-detect rediscovers the same hole on every run and never converges.

**Decision: accept it, and make it visible.** Log per gap with structured `slog` attrs — `serial`, `from`, `to`, `requested`, `inserted` — so automation can detect non-convergence (`inserted=0` across runs) directly from the log stream. Exit code stays 0: a permanent hole is not an error.

**Cut:** the bespoke `gaps=3 requested=4320 inserted=0` summary line. It was a third output format with no present consumer, and `go-standards` §6.1 would want `--json` rather than an ad-hoc grammar. `slog` attrs already provide the machine-readable surface; if a caller later needs a single-line total, add `--json` then.

Rejected: persisting a table of attempted windows. It adds schema and durable state to save only redundant API calls, and goes stale if the API later fills its own history.

`--min-gap` (default 30m) keeps ordinary reporting jitter from registering.

**Considered and rejected: deriving `--min-gap` from the stored `report_interval` column.** It is nullable (`migrations/0001_init.sql:12`, `*int64` in Go), so a fallback would be needed anyway; it varies per row; and the flag has a genuine present consumer — a station configured with a long report interval needs a larger value than the default. Keep the flag.

## Flags

Parsed from a `backfill`-owned `flag.FlagSet` over `os.Args[2:]`.

| Flag | Default | Notes |
|---|---|---|
| `--from`, `--to` | unset (auto-detect) | **RFC3339, interpreted UTC.** The store is UTC epoch and the API takes epoch seconds; an ambiguous local-time parse is a quiet wrong-window bug. |
| `--min-gap` | `30m` | |
| `--dry-run` | false | Gap detection + plan only: **zero observation fetches**, **zero** writes. It still calls the device lookup (one API call), so it *does* validate the token and reachability. |
| `--store` | unset | `sqlite` or `postgres`. **Required** when the environment selects more than one store; optional (and validated against the selection) otherwise. |

**Why `--store` exists.** `selectStore` (`main.go:147-154`) returns *both* stores when `ENABLE_POSTGRES=true` **and** `SQLITE_PATH` is set — a documented fan-out the daemon honors by writing every observation to both. Backfill repairs one store per run, so with both configured it cannot infer the target. It must **never** guess: silently repairing Postgres while leaving the SQLite database (the one Litestream replicates to S3) still holed, and then reporting success, is the worst available outcome. Absent `--store` in that configuration, backfill exits `2` naming both candidates.

**Why `--from/--to` exists** (restated — the earlier "avoids a full scan" rationale does not hold; `idx_obs_serial_time` already covers the detection query): it **bounds the API work**. Because permanent holes are accepted and never recorded, auto-detect re-requests every known-empty window on every run. An operator repairing a known outage can name it directly and skip that cost.

## Error handling

A failed gap logs and continues; per-gap errors accumulate via `errors.Join` and the process exits **non-zero** if any gap failed. Context cancellation is honored between windows and retries, leaving inserted rows intact — idempotency makes re-running safe. Backfill must **never** `log.Fatalf` mid-run: partial progress must be preserved and reported.

**A failed *window* must not abandon its gap.** "Continue on failure" applies at the window level too, not only between gaps. Returning from a gap on its first bad window discards every remaining window in that gap, and for a *deterministic* per-window failure the loss is **permanent across runs**, not transient: once the earlier windows land, the head gap collapses and the hole reappears as an interior gap starting at the same bad window, which fails again — so the windows behind it are never requested on any run. A tool whose entire premise is convergence would silently stop converging, and the `inserted=0` signal never fires because those windows are never even requested. Accumulate window errors with `errors.Join`, keep going, and fail the gap at the end.

Exit codes: `0` success (including permanent holes, and `--help`), `1` one or more gaps failed or a runtime error, `2` usage error. **`flag.ErrHelp` is not a usage error** — `backfill --help` must exit `0`, or every CI smoke test that runs `cmd --help` fails.

## Testing

Every row below is written **test-first**; the entry-point split above is what makes that possible.

| Area | Test |
|---|---|
| Nullability | Fixture with `null` obs elements → assert row pointer fields are **nil**, not zero. |
| Gap detection — partitioning | **Two serials with interleaved timestamps that mask each other.** |
| Gap detection — domain | Head gap, tail gap, empty table, no-gaps. |
| Chunking | Multi-day range → N single-day requests. |
| Empty window | One table-driven test, three cases, all → zero rows + no error: `obs` empty, `obs` null/absent, status-only envelope with no `type`. |
| Error taxonomy | 429/503/network → retried; non-zero `status_code` → not retried, surfaces as `*StatusError` via `errors.As`. |
| **Per-attempt timeout** | A **real** `*url.Error` from a stalled `httptest.Server` + short-timeout client → **retried**. A synthetic `net.Error` fake does not reproduce the dual-predicate error and would pass against the broken classifier. |
| **Window failure isolation** | Three windows in one gap, the middle one permanently failing → windows 1 and 3 both inserted, gap still reported failed. |
| Idempotency | Insert twice; second inserts 0, changes nothing. |
| Dry-run | Zero writes, zero **observation fetches**. |
| Serial pre-flight | **Disjoint** sets on a non-empty store → non-zero exit, **zero rows written**. Empty store → not a mismatch. A *new* serial alongside a known one → **not** a mismatch; it yields a whole-range gap. A known serial with no rows *in the requested window* → **not** a mismatch. |
| **Short tuples** | `len(ob) == 13` → row inserted with the tail columns NULL, not dropped. Rejected tuples increment the reported drop counter. |
| **Store selection** | Both stores configured without `--store` → exit 2, nothing written. `--store` naming an unconfigured store → exit 2. |
| Dispatch | Unknown subcommand → usage, exit 2, **daemon not started**. Asserted by re-exec, not by testing the predicate in isolation. |
| **`--help`** | Exits **0**, not 2. |
| Multi-device | A station reporting two `ST` devices → `ListDevices` returns two entries; both are backfilled. |

Repo idiom applies: `t.Context()`, `t.TempDir()`, stdlib table-driven, no testify.

**Tests that can only fail at compile time or by string match** (a struct-literal field-name assertion, a `strings.Contains` on a SQL constant) do **not** count toward the rows above. They assert the language spec or restate the file they live in; the behavior needs a test that can fail at runtime for the reason the row names.

**The Postgres path ships unverified unless an integration test runs.** Its SQL differs materially from SQLite's — `EXTRACT(EPOCH FROM (ts - prev)) > $3` compares a `numeric` against a `float64` parameter, and `precip_type` is `INTEGER` while the bind is a `*float64` (pgx truncates silently). A skip-guarded integration test following `writer_integration_test.go`'s existing idiom must seed the same two-serial interleaved fixture the SQLite test uses, and it must be run at least once against a real database before the branch merges.

## Documentation owned by this design

`CLAUDE.md` must be updated as part of this work:

- **Name collision:** `CLAUDE.md:239` already has a section "**API Export with Backfill**" describing the *existing* `TOKEN`-triggered `ModeAPIExport`. Rename it (e.g. "API Export Mode") and add a distinct section for the `backfill` subcommand, so the two are not conflated.
- `CLAUDE.md:78` describes `internal/tempestudp` as UDP parsing — accurate, and now protected by moving `Observation`/`Gap` to `internal/weather`. Add `internal/weather` to the package list.
- The **operational-modes table** assumes mode is chosen by env vars; a subcommand bypasses that. Add a line stating that `backfill` and `healthcheck` short-circuit mode selection entirely.
- Document the new flags and exit codes — including `--store` (when it is required and why backfill refuses to guess), and the accurate `--dry-run` semantics (zero *observation fetches*, but one device-lookup call, so it *does* validate the token).
- State the multi-sensor behavior: `backfill` enumerates every `ST` device via `ListDevices`, unlike `TOKEN`-mode export, which sees one device per station.

Out of scope but noted: `CLAUDE.md` is broadly stale elsewhere (no SQLite-default/OTel/httpserver/radar coverage; still says "Go 1.23.0+" against a 1.25 floor). Not this design's job to fix — flagged for a separate docs pass.

## Constraints

- `CGO_ENABLED=0` stays buildable. `modernc.org/sqlite` bundles SQLite 3.46.0; `LAG(...) OVER (PARTITION BY ...)` verified working under it by execution.
- Zero new dependencies.
- Timestamps UTC epoch; PKs UUIDv7 generated in Go. Note UUIDv7 embeds *insert* time, so backfilled rows sort by `id` in insert order, not observation order — consistent with the ingest path, but `id` ordering must not be assumed chronological.
- Go 1.25 floor, toolchain go1.26.1. **No Go 1.26-only APIs** (notably `errors.AsType`).

## Non-goals

- Backfilling rapid wind, hub status, or events.
- Modifying `ModeAPIExport` or `sqlite.WriteMetrics`.
- Fixing the missing `break` in `ListStations`' device loop.
- Implementing #80's `migrate` — this only establishes the dispatch pattern it slots into.
