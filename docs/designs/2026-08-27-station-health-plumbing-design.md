# Station health plumbing — design (#196)

**Date:** 2026-08-27
**Issue:** [#196](https://github.com/jacaudi/stormglass/issues/196) — *Signal and firmware are broadcast over UDP but never reach the API*
**Milestone:** v1 · `release/v1` · `priority/high` · `type/bug`
**Plan:** `docs/plans/2026-08-27-release-v1-implementation.md` (Task 14)

---

## 1. The problem, measured

The Tempest broadcasts a `device_status` message roughly once a minute carrying
the sensor's own health. The parser already models it in full
(`internal/tempestudp/report.go:234-246`): `Voltage`, `FirmwareRevision`,
`Rssi`, `HubRssi`, `Uptime`, `SensorStatus`.

Nothing downstream keeps any of it. Four measured loss points:

| # | Where | Evidence |
|---|---|---|
| 1 | SQLite writer drops it | `internal/sqlite/writer.go:417` — `// Unknown report type (e.g. device_status) - not an error.` |
| 2 | Postgres writer drops it | `internal/postgres/writer.go:521` — same `default:` arm |
| 2b | **OTel writer drops it too** | `internal/otel/writer.go:201-207` — the type switch has cases for observation / rapid-wind / hub-status / rain / lightning and **no `device_status` case and no `default:` arm**, so it falls through silently |
| 3 | No reader exists | `grep -rn "FROM stormglass_hub_status" --include='*.go'` → **no matches** |
| 4 | API hardcodes null | `web/src/api/stormglassApi.ts:118-119` — `signalStrength: null, firmwareVersion: null` |

So the UI's Station Health card renders `SIGNAL —/4` and a blank `FIRMWARE`
forever, on a station that is broadcasting both values every minute.

**This is a defect, not a missing feature** (decision A2): the card ships, and
it is starved of data the station already sends.

---

## 2. Settled inputs (do not relitigate)

| | Decision | Source |
|---|---|---|
| **A1** | Signal ships as **raw dBm**, not 0–4 bars | Adam, 2026-08-27. No vendor dBm→bars mapping exists, so bucketing server-side would mean inventing thresholds. |
| **A2** | Stays in v1 as a `fix:` (patch) | Adam. Repairing a shipped surface is a defect. |
| **Radio** | The **device's** `rssi`, not `hub_rssi` | The card is labelled Station Health; `report.go:234-236` documents `device_status` as "the sensor's own health … distinct from HubStatusReport, which covers the hub itself." |
| **Firmware wire type** | **string** | Parser types it `int` in `obs_st` (`:163`) and `device_status` (`:244`) but **`string` in `hub_status` (`:284`) — vendor forms are mixed. The shipped UI contract is already `string \| null` (`weather.ts:117`). An int stringifies losslessly; the reverse does not hold. |

---

## 3. Storage shape — **decided: a new `stormglass_device_status` table**

This is the one genuinely open question the plan handed to this design. It is
decided on a measured asymmetry, not on taste.

### 3.1 Why not columns on an existing table

**Correction (Gate 1):** an earlier draft of this section claimed
`internal/postgres/schema.go` contains "four `CREATE TABLE IF NOT EXISTS`
statements and nothing else." That is **false** — it also contains six
`CREATE INDEX IF NOT EXISTS` statements (`:107,108,112,113,117,121`). The
narrower claims below are the true ones and are what the argument needs.

`internal/postgres/schema.go` has **no `ALTER TABLE`, no `schema_version`, and
no migration runner anywhere in `internal/postgres/`**. Its four table
statements are:

```
34: CREATE TABLE IF NOT EXISTS stormglass_observations
68: CREATE TABLE IF NOT EXISTS stormglass_rapid_wind
80: CREATE TABLE IF NOT EXISTS stormglass_hub_status
94: CREATE TABLE IF NOT EXISTS stormglass_events
```

On a database that already exists, `CREATE TABLE IF NOT EXISTS` is a **no-op**.
Adding columns to `stormglass_observations` would therefore:

1. appear to work on a fresh database,
2. silently do nothing on every existing Postgres deployment,
3. and then fail at runtime when the writer's `INSERT` names a column that
   was never created.

**But this argument is weaker than it looks, and it is not the reason for the
decision.** PostgreSQL supports `ALTER TABLE … ADD COLUMN IF NOT EXISTS`
(PostgreSQL 16 docs: *"If `IF NOT EXISTS` is specified and a column already
exists with this name, no error is thrown"*). Appending such a statement to
`CreateSchema`'s `schemas` slice would be safe on fresh **and** existing
databases, in the file's own `IF NOT EXISTS` idiom. So the three-step failure
story above only applies to a `CREATE TABLE`-only implementation that nobody
is obliged to write.

**The decision rests on §3.2 instead**, which is decisive on its own. This
section is retained because the missing `ALTER`/`schema_version` machinery is
a real property of the Postgres store that anyone touching it needs to know —
not because it settles the shape.

### 3.2 Why a new table is the natural shape

- **A `device_status` broadcast has no observation row to attach to.** It is
  its own report type on its own cadence. Columns on `stormglass_observations`
  would either be null on every row (the observation writer never sees a
  `device_status`) or require a join-and-backfill the writers do not do.
- **The precedent already exists in this schema.**
  `stormglass_hub_status` (`schema.go:80-90`) is exactly this pattern — a
  per-report-type status table keyed `UNIQUE(serial_number, timestamp)`. A
  `stormglass_device_status` table is its sibling, not a new concept.
- **`CREATE TABLE IF NOT EXISTS` is correct for a new table on both stores** —
  it fires on existing databases precisely because the table is absent. The
  Postgres path needs no migration runner it does not have.

### 3.3 Schema

Mirrors `stormglass_hub_status`, with the fields `device_status` actually
carries.

**Postgres** (`internal/postgres/schema.go`, a fifth `createDeviceStatusTable`):

```sql
CREATE TABLE IF NOT EXISTS stormglass_device_status (
    id                UUID PRIMARY KEY,
    serial_number     TEXT NOT NULL,
    timestamp         TIMESTAMPTZ NOT NULL,
    uptime            BIGINT,
    voltage           DOUBLE PRECISION,
    firmware_revision TEXT,
    rssi              INTEGER,
    hub_rssi          INTEGER,
    sensor_status     INTEGER,

    UNIQUE(serial_number, timestamp)
);
CREATE INDEX IF NOT EXISTS idx_device_status_serial_time
    ON stormglass_device_status(serial_number, timestamp DESC);
```

**SQLite** (`internal/sqlite/migrations/0003_device_status.sql` — next number;
the directory holds only `0002_init.sql`):

```sql
CREATE TABLE IF NOT EXISTS stormglass_device_status (
    id                TEXT PRIMARY KEY,
    serial_number     TEXT NOT NULL,
    timestamp         INTEGER NOT NULL,
    uptime            INTEGER,
    voltage           REAL,
    firmware_revision TEXT,
    rssi              INTEGER,
    hub_rssi          INTEGER,
    sensor_status     INTEGER,

    UNIQUE(serial_number, timestamp)
);
```

**No extra SQLite index is needed, and that is a consequence of §5.2's
serial-scoped read.** `UNIQUE(serial_number, timestamp)` creates an implicit
index leading with `serial_number`, which serves
`WHERE serial_number = ? ORDER BY timestamp DESC LIMIT 1` directly. An
*unscoped* `ORDER BY timestamp DESC LIMIT 1` could **not** use it and would
full-scan — exactly the I1 defect `0002_init.sql:41-45` documents for
`idx_obs_serial_time`. Postgres gets the explicit index above because its
`UNIQUE` constraint index is likewise serial-leading and the planner benefits
from the `DESC` ordering being declared.

`firmware_revision` is **TEXT** in both stores, per §2.

**Column affinities follow the SOURCE types, not `hub_status`'s.**
`DeviceStatusReport` types `Uptime`, `Rssi`, `HubRssi` and `SensorStatus` as
`int` (`report.go:242-246, 260`), where `HubStatusReport` uses `float64`
(`:285-286`) — so "mirrors hub_status" would mirror the wrong source. In
particular `sensor_status` is a **bitfield** (`report.go:249-259`), and this
repo already has the precedent for typing a categorical as INTEGER:
`0002_init.sql`'s `precip_type INTEGER, -- categorical enum …, not a
measurement`. Only `voltage` is genuinely a `float64` measurement.

(Note the two stores' existing `hub_status` tables already disagree — Postgres
is all `DOUBLE PRECISION`, SQLite is mixed. This design follows the Go source
types on both, which is the convention that survives review.)

Conventions preserved: UUIDv7 text/UUID primary keys, unix-epoch **integer**
timestamps in SQLite and `TIMESTAMPTZ` in Postgres, `UNIQUE(serial_number,
timestamp)` for idempotent re-insert.

### 3.4 The alternative the plan named: a latest-value (upsert) table

Plan Task 14 assigned two alternatives, and §3.1 rebutted only one. The other
is a **one-row-per-serial upsert table** (`ON CONFLICT (serial_number) DO
UPDATE`). Its genuine advantages: an O(1) read with no ordering, no index
question at all, and no accumulation of ~525k rows/device/year for data whose
only consumer reads the newest row.

**Rejected, but narrowly.** The history table wins on three counts: it matches
the `stormglass_hub_status` idiom the schema already has; it matches the
`ON CONFLICT … DO NOTHING` append-only convention both writers use everywhere
else (an upsert path would be the first mutating write in either store, with
different Litestream replication characteristics); and it leaves RSSI history
available if anyone ever wants to chart radio quality, which discarding is
irreversible while retaining is not.

The volume is bounded and small — nine columns at ~1 row/min is on the order of
tens of MB per device-year, against a store that already takes an observation
row per minute.

### 3.5 Migration safety — the SQLite side is one-way

`internal/sqlite/schema.go:56-60` refuses to start when the database is newer
than the binary:

> `database schema version %d exceeds the highest migration bundled in this
> binary (%d)` … *upgrade the binary rather than downgrading the database*

Once `0003` is applied, **rolling the image back to 1.0.1 makes the process
refuse to start** on every migrated appliance. SQLite is the default store and
a failed open is fatal at startup.

**The revert window, stated precisely** (this is the plan's §6 B6 rule and the
operationally useful half):

- **While v1.0.2 is unreleased** — merged to `main` but not shipped, so only
  development databases have run `0003` — the migration commit **can** be
  reverted normally.
- **Once Adam merges PR #214 and v1.0.2 ships**, it cannot. A #196 regression
  is then **fixed forward only**. This restates the plan's §6
one-way-migration rule and is the single highest-risk property of this change.

The Postgres side has **no version guard**, so it is revert-safe. The
asymmetry is real and is stated here so nobody assumes symmetry under
pressure.

---

## 4. Writers

### 4.1 SQLite and Postgres — the two that persist

Both currently drop `*tempestudp.DeviceStatusReport` in their `default:` arm.
Each gains a case that batches a `stormglass_device_status` row alongside the
existing paths, with the same UUIDv7 generation and `ON CONFLICT … DO NOTHING`
idempotency.

**They are not the same amount of work, and §7 must test accordingly.**
SQLite is one goroutine with local batch slices (`writer.go:265-275` `run`,
`:241-262` `flushBatches`). Postgres is **goroutine-per-table** —
`wg.Add(4)` plus four channels and four `batchX()` loops
(`postgres/writer.go:110-113, 172-190, 380-406`). Adding device_status there
means a **fifth concurrent goroutine** with its own channel, ticker,
`steadyStateFlushCtx` and `<-w.done` drain.

This is the repo's worst-history area: #111, #154, C-H1 and D-H1 are all
in-code citations for shutdown-drain data loss, and `sqlite/writer.go:275-286`
records that *"the postgres writer's drain only covered a local slice, not the
channel buffer."* **A shutdown-drain test on the new path is required, not
optional.**

### 4.2 Ingest validity — absent must not become a reading

`DeviceStatusReport` types `Rssi` and `FirmwareRevision` as **non-pointer
`int`** (`report.go:244-245`), so a broadcast missing or malforming either
field decodes to `0`. Stored naively, that becomes `0 dBm` and firmware `"0"`
— **absent data presented as a reading**, which is the exact defect
`stormglassApi.ts:97-107` and `StationHealth.test.tsx:15-21` exist to prevent.
`0` is a valid dBm value, so it cannot double as the unknown sentinel (§5.4).

**Decision: make the two served fields pointers in the parser** —
`Rssi *int` and `FirmwareRevision *int` on `DeviceStatusReport` only — so
absence is representable end to end and reaches the store as SQL NULL.
`encoding/json` leaves a pointer nil when the key is absent, which is the
distinction the non-pointer type erases. The other fields are untouched.

Every sibling handler already guards its report (`sqlite/writer.go:491`,
`:505`, `:521`, `:535`), so the new handler follows suit: a report with
`Timestamp == 0` is dropped as malformed, logged at WARN, not persisted.

### 4.3 The OTel writer is deliberately out of scope

`internal/otel/writer.go:201-207` also drops `device_status` — no case, no
`default:`. It is **not** changed here. It already emits the hub's analogous
telemetry, so extending it is coherent future work, but nothing in #196 asks
for device RSSI on the OTel path and no consumer exists. Named explicitly in
§8 rather than left as an omission.

## 5. API surface — extend `GET /api/observations/current`

### 5.1 The two fields

Additive, both nullable:

| JSON field | Type | Source |
|---|---|---|
| `signalDbm` | `number \| null` | latest `stormglass_device_status.rssi` for the observation's serial |
| `firmwareVersion` | `string \| null` | latest `stormglass_device_status.firmware_revision` |

`signalDbm` is deliberately **not** `signalStrength`: the existing UI type is
`signalStrength: number \| null; // 0-4`, and A1 changes the units. A new field
name makes the units change explicit at the type level rather than silently
redefining an existing one.

These are the **first pointer fields** on `currentObservation`
(`observations.go:67-95`), whose `deref` helper is documented as *"The single
'nil pointer means absent SQL column -> report as zero' mapping **every**
Contract C integer/float field below needs"* (`:387-390`). That comment
becomes false and **must be updated in the same commit**: this design
deliberately carves an exception, because zero-filling is exactly the failure
mode §4.2 and §5.4 exist to prevent.

### 5.2 The read side — fully specified

The API reads **SQLite only** (`server.go:52-55`, `Deps.Observations`), via the
consumer-site `ObservationReader` interface. Every decision an implementer
needs:

1. **Store:** SQLite. The Postgres `stormglass_device_status` table gets **no
   reader**, exactly as `stormglass_hub_status` has none today (§1 loss point
   3). Postgres remains a fan-out sink.
2. **Interface:** `ObservationReader` gains one method,
   `LatestDeviceStatus(ctx, serial string) (DeviceStatus, error)`, returning a
   typed `ErrDeviceStatusNotFound` checkable via `errors.Is`, mirroring
   `LatestObservationAny`'s contract (`sqlite/writer.go:852-866`).
   `fakeObservationReader` (`observations_test.go:20-76`) implements it too.
3. **Serial scoping: SCOPED, not "any".** `LatestObservationAny` is
   deliberately unscoped because a single-station appliance has no serial to
   scope by — but `device_status` is **not Tempest-only**: `report.go:249-259`
   lists AIR/SKY/Tempest sensor bits, and CLAUDE.md documents two-Tempest
   stations for `backfill`. An unscoped "newest across all serials" could
   return **another device's radio** next to a Tempest observation. Scope to
   the serial of the observation just returned; it is free and correct.
4. **Error policy: degrade, do not fail.** A query error logs WARN and serves
   both fields as `null`, mirroring the sibling `HistoryPoints` call in the
   same handler (`observations.go:206-213`, error → WARN → `trendPoints = nil`).
   A radio reading must never 500 the dashboard.
5. **Handle:** the read goes through `w.readDB`, not `w.db`
   (`sqlite/writer.go:164-169`), so it uses the read-only connection wired at
   `main.go:355` instead of serialising behind the single ingest writer.

### 5.3 Why this endpoint and not a new one

`GET /api/station` is not a candidate: it is served entirely from
configuration and computed **once at startup** (`station.go:40` — `resp :=
stationResponseFrom(deps.Station)` outside the handler).

A dedicated `GET /api/station/health` is coherent but buys nothing here: the
UI's `StationStatus` takes `isOnline`, `lastReport` and `batteryLevel` from the
current observation (`stormglassApi.ts:113-121`), so the card would need both
responses joined client-side either way.

**Correcting an earlier draft:** that draft argued a separate endpoint means "a
second fetch." That is not a real cost — the UI **already** issues two
concurrent GETs to `/api/observations/current` per load (`useWeatherData.ts:183`
directly, and `:185` via `fetchStationStatus`, which itself awaits
`fetchCurrentObservation` at `stormglassApi.ts:112`). The endpoint choice stands
on co-location with the fields the card already derives, not on request count.

**The honest cost:** these two fields describe device health, not a weather
observation, so this is a mild denormalization. Documented as *latest known
device health* in the field comments.

### 5.4 Staleness — the two report types have different cadences

`obs_st` and `device_status` arrive independently. Without a policy the card
would show `Last Report 12s ago` beside a signal reading that could be hours
old, with no way for the client to tell.

**Decision: the server serves `null` for both fields when the newest
`device_status` row is older than `deviceStatusMaxAge = 15 minutes`.** At a
~1/minute cadence that is fifteen consecutive misses — a radio that is down,
not a jittery one. A stale reading presented as live is the same class of
defect as a zero presented as a reading, and this is the one place the server
can prevent it.

No age field is added to the wire: it would need a UI consumer to be useful,
and none is proposed. The threshold is a named constant with this reasoning in
its comment.

## 6. UI

### 6.1 The refresh path — the defect an earlier draft missed

`fetchStationStatus` is called **only** inside `loadData`'s
`Promise.allSettled` (`useWeatherData.ts:185`). The 30-second poll
(`POLL_INTERVAL_MS`, `:27`; interval at `:279`) calls **only**
`fetchCurrentObservation` (`:235`) and never touches `status`. `refresh()`
(`:316`) reaches `loadData`, but its one call site is the Retry button on the
**error** screen (`App.tsx:55`, rendered only when `error && !current`).
`StationHealth` is additionally `memo`'d on `status` (`:100`).

So without a change here, signal and firmware would populate at mount and then
**freeze for the life of the tab** — and the code itself notes the appliance
tab "may stay open for weeks" (`useWeatherData.ts:277`). Every gate would still
pass: the API returns fresh values, and the component test injects `status` as
a prop.

**This repo has already fixed this exact defect class once** —
`useWeatherData.ts:319-321`: *"Without this the Records card is the one slice
the Refresh button does not reach, so it holds page-load extremes … for the
lifetime of the tab (issue #89)."*

**Decision: `pollCurrent` refreshes `status` too.** The fields ride on the
response `pollCurrent` already fetches, so this costs no extra request — the
poll simply derives and sets `status` from the observation it already has,
instead of discarding that half.

### 6.2 Types and rendering

- `web/src/types/weather.ts:116` — `signalStrength: number | null; // 0-4`
  becomes `signalDbm: number | null` (dBm). `firmwareVersion: string | null` is
  **unchanged**, which is why §2 chose string. The `:104-115` comment block
  asserting `0-4` bars must be rewritten with it.
- `web/src/components/StationHealth.tsx` — the `signalBars()` helper
  (**defined at `:27`**, called at `:41`) is removed rather than fed an
  incompatible unit; inventing a dBm→bars mapping is what A1 forbids. `:88`
  renders a dBm reading instead of `{signalStrength}/4`. The firmware `<Stat>`
  at `:94` needs **no change**.
- `web/src/App.css:1247-1268` — `.signal-display`, `.signal-bars`,
  `.signal-bar`, `.signal-bar.active` become dead CSS and are removed.

### 6.3 Two existing tests must be rewritten — and this needs Adam's sign-off

`web/src/components/StationHealth.test.tsx:36-53` contains a test whose comment
reads:

> *"once the follow-up plumbs device_status's rssi and firmware_revision
> through, the card must render them **without any further change**. If this
> breaks, the em-dash path has swallowed real data."*

It asserts `.signal-bars` exists, four `.signal-bar`s, three `.active`, and the
text `3/4`. **A1 makes every one of those unsatisfiable.** A previous author
encoded the assumption that the follow-up would keep bars; A1 overturns that
assumption deliberately.

`web/src/hooks/useWeatherData.test.ts:65` sets `signalStrength: 0` and will
fail to typecheck after the rename.

The governing plan's §6 says **"Never weaken or delete an assertion to get
green"** and routes unmade decisions to a STOP. **This is the exception, and
it must be authorised explicitly rather than assumed** — see §10. The
rewritten test must preserve the *intent* (real values render; absent values
render `—`) while changing the *units*, and must not simply be deleted.

---

## 7. Test strategy

TDD throughout.

| Area | Must cover |
|---|---|
| Migrations | table exists after startup on both stores; idempotent re-run; existing tables untouched |
| Writers | one `DeviceStatusReport` → exactly one row; firmware round-trips as TEXT; duplicate `(serial, timestamp)` does not double-insert |
| Writers — **absent vs zero** (§4.2) | a report with `rssi`/`firmware_revision` **absent** persists SQL NULL, not `0`/`"0"`; a report with `Timestamp == 0` is dropped |
| Writers — **shutdown drain** (§4.1) | a device_status queued immediately before `Close()` is persisted; required on the Postgres goroutine path given #111/#154/C-H1/D-H1 |
| Read — scoping (§5.2.3) | with two serials present, the row returned matches the observation's serial |
| Read — errors (§5.2.4) | a failing query yields `null` fields + WARN, **not** a 500 |
| Read — staleness (§5.4) | a `device_status` older than 15 min serves `null`; one inside the window serves the value |
| API | fields present when a row exists; `null` when none; **`0` dBm survives as `0`, not null** |
| UI — refresh (§6.1) | a poll tick updates `status`, not just `current` |
| UI — render | dBm value when present, `—` when null (rewritten `StationHealth.test.tsx`) |

Postgres tests skip without `POSTGRES_URL`, per the existing convention.

## 8. Scope boundary

**In:** the `device_status` path through SQLite + Postgres writers, the SQLite
read, the two API fields, and the UI including its refresh path.

**Out, named rather than omitted:**

- **The OTel writer** (§4.3) — also drops `device_status`; no consumer asks for
  it.
- **`hub_rssi`** — the hub's radio, not the station's (§2). Stored, not served.
- **`uptime`, `voltage`, `sensor_status`** — stored because the row is being
  written anyway and omitting them would force a second migration, which the
  SQLite one-way rule (§3.5) makes expensive. No API field, no UI. `hub_sn` and
  `debug` (`report.go:241, 264`) are **not** stored: they identify the hub and
  carry vendor debug state, neither of which is device health. (Note `voltage`
  duplicates `stormglass_observations.battery`, which the card already reads
  via `stormglassApi.ts:117` — it is stored for row completeness, not as a new
  source.)
- **Historical backfill** — no REST source exists; `backfill` is
  observations-only per CLAUDE.md.
- **Docs** — plan Task 19 Step 2 already owns adding these fields to the
  configuration/API docs; not duplicated here. `README.md:186-188` lists routes
  only, so no README change is required.

## 9. Resolved — Adam's authorisation (2026-08-27)

**Q — authorising the `StationHealth.test.tsx` rewrite (§6.3).** A previous
author wrote a test asserting the card would need *no further change* when this
data was plumbed through. A1 (raw dBm, no bars) makes that assertion
unsatisfiable by design. The plan forbids weakening assertions without
authorisation, so this cannot be resolved by an executor.

**ADAM AUTHORISED THE REWRITE, 2026-08-27.** The test is rewritten to preserve
its **intent** — real values render; absent values render `—` — while changing
only the units to dBm. The em-dash guard it was actually protecting stays.
Deleting the test, or weakening it to assert nothing, is **not** authorised.
The two alternatives (reversing A1 to keep bars, or shipping both a `signalDbm`
and a bucketed `signalStrength`) were offered and declined, because both
require inventing the dBm→bars mapping A1 rejected.

Settled — do not re-raise. This is the plan §6 "never weaken an assertion"
rule's one authorised exception in #196.

**Design approved for implementation, 2026-08-27.** Tasks 15–18 are unblocked.

## 10. Verified vs. assumed

**Verified first-hand (2026-08-27), and re-verified after Gate 1:**

- All four loss points at the cited lines, **plus a fifth**: `internal/otel/writer.go:201-207` drops `device_status` with no case and no `default:`.
- `internal/postgres/schema.go` has **no `ALTER TABLE`, no `schema_version`, no migration runner**. It does contain six `CREATE INDEX IF NOT EXISTS` — an earlier draft's "nothing else" was **false** and is corrected in §3.1.
- `internal/sqlite/migrations/` holds only `0002_init.sql`, whose header states *"The next migration after this one MUST be numbered 0003."*
- `internal/sqlite/schema.go:56-60`'s newer-than-binary guard, and `main.go:351-353`'s fatal-on-open.
- `DeviceStatusReport` field types: `Rssi`, `HubRssi`, `Uptime`, `SensorStatus` are `int`; `Voltage` is `float64` (`report.go:242-246, 260`). The struct spans `:234-268`, not `:234-246`.
- Firmware typed `int`/`int`/`string` across the three report types (`:163`, `:244`, `:284`); `weather.ts:117` already `string | null`.
- `/api/station` computed once at startup (`station.go:40`).
- `stormglass_hub_status`'s shape (`schema.go:80-90`) — and that it has **no reader**, so it is a precedent for the write path only, not the read path.
- **`useWeatherData.ts`: `fetchStationStatus` at `:185` only; the poll at `:279` calls `fetchCurrentObservation` at `:235` and never refreshes `status`.** This is §6.1.
- `StationHealth.test.tsx:36-53`'s assertions and their "without any further change" comment.
- `sqlite/writer.go:164-169`'s `readDB` split and `:852-866`'s `LatestObservationAny` contract.
- `observations.go:387-390`'s `deref` "report as zero" comment, and `:206-213`'s degrade-on-error precedent.
- `0002_init.sql:41-45`'s I1 index reasoning (serial-leading indexes cannot serve an unscoped timestamp sort).

**Not verified — treat as open:**

- **The `device_status` cadence.** "Roughly once a minute" comes only from `report.go:231-232`'s own comment; the WeatherFlow UDP v143 doc states **no transmission interval** for this message. §5.4's 15-minute threshold is sized against the in-repo claim, not a vendor guarantee, and should be revisited if that proves wrong.
- **The dBm range a real Tempest reports.** No units or range are documented. The vendor's `device_status` example shows `-17` (an AIR device); the repo's one test literal is `-82` (`report_test.go:317`). No clamping or validation is proposed.
- **Whether `firmware_revision` ever arrives non-numeric on `device_status` specifically.** Mixed typing is proven *across* report types; the vendor example for `device_status` is numeric (`17`). String remains the safe direction.
- **Whether any existing deployment runs a Postgres database predating the current schema.** §3.1's argument is deployment-independent.
- **`rssi: -61` (an earlier draft cited this as evidence).** It came from a synthetic broadcaster written for this close-out's Task 5 harness, which lives in the session scratchpad and **not in the repo** — so it is not a reproducible in-tree source and is withdrawn as evidence. It demonstrated the ingest path, not any vendor range.
