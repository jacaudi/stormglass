# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-backend data utilities for Tempest weather stations. The application operates in two modes:

1. **UDP Listener Mode** (default): Listens for Tempest UDP broadcasts on port 50222 and writes to Prometheus push gateway and/or PostgreSQL in real-time
2. **API Export Mode**: Fetches historical observation data via REST API when `TOKEN` env var is set, writes to PostgreSQL and/or compressed files

## Development Commands

### The CI contract

`task ci` is the whole *static* gate: `ci-test.yml` invokes `task ci` and
nothing else. Every check it runs must be runnable locally with that one
command — a check that cannot be is not allowed to live only in a workflow.

The image-shaped stages are the deliberate exception, because a container
build is not something to put in front of every local run. Each still has a
named local equivalent, and CI runs the stage rather than the task:

| CI stage | Local equivalent |
|---|---|
| `ci-build.yml` (application image) | `task build-local` |
| `ci-smoke.yml` (boot the built image) | `IMAGE=… task smoke` |
| `ci-radar.yml` (radar sidecar image) | `task radar-build` |

```bash
task ci          # everything the test stage runs: lint, format, tidy, vuln, tests, node, python
task lint        # static checks only
task test        # tests only
task radar-build # compile the radar sidecar image
task --list      # every available target
```

`task ci` needs `golangci-lint`, `actionlint`, `hadolint` and `govulncheck` on
PATH; it fails loudly naming the missing tool rather than skipping the check.

### Testing
```bash
# Run all tests
go test ./...

# Run tests with JSON output (CI format)
go test -json ./...

# Prepare dependencies
go mod tidy
```

### Building
```bash
# Build local Docker image
task build-local

# Direct Docker build
docker buildx bake image-local
```

### Running Locally
```bash
# UDP mode with push gateway (requires host network for broadcast reception)
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_PUSHGATEWAY=true \
  -e PROMETHEUS_PUSHGATEWAY_URL=http://localhost:9091 \
  stormglass:latest

# UDP mode with scrape endpoint (Prometheus pulls from /metrics)
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_METRICS=true \
  stormglass:latest

# UDP mode with scrape endpoint on custom port
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_METRICS=true \
  -e PROMETHEUS_METRICS_PORT=9090 \
  stormglass:latest

# UDP mode with both push gateway and scrape endpoint
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_PUSHGATEWAY=true \
  -e PROMETHEUS_PUSHGATEWAY_URL=http://localhost:9091 \
  -e ENABLE_PROMETHEUS_METRICS=true \
  stormglass:latest

# API export mode
docker run -it --rm \
  -e TOKEN=your_token \
  stormglass:latest
```

## Architecture

### Operational Modes

The application switches modes based on presence of `TOKEN` environment variable:
- **No TOKEN**: Runs `listenAndPush()` - UDP listener with push gateway
- **With TOKEN**: Runs `export()` - Historical data export to gzipped files

### Internal Package Structure

- **`internal/metrics/`**: Defines all Prometheus metric descriptors (`prometheus.Desc`)
- **`internal/tempestudp/`**: Parses UDP broadcast messages into metrics, includes wet bulb temperature calculations
- **`internal/tempestapi/`**: REST API client for fetching historical observations
- **`internal/weather/`**: Store-neutral `Observation`, `Gap`, and `Bounds` types shared by the REST client and both stores
- **`internal/backfill/`**: Gap detection and API backfill orchestration for the `backfill` subcommand

### Data Flow (UDP Mode)

1. UDP packets received on port 50222 → `listen()`
2. Raw bytes → `tempestudp.ParseReport()` → Report struct
3. Report → `Report.Metrics()` → `[]prometheus.Metric`
4. Metrics buffered in `outbox` channel (cap: 1000)
5. `collector` drains `outbox` when `pusher.Add()` called
6. Metrics pushed to gateway in Prometheus text format

### Key Design Patterns

- Uses Prometheus push pattern (not pull/scrape) because weather stations broadcast sporadically
- Custom `collector` implementation drains buffered metrics non-blockingly
- UDP broadcasts are link-local, requiring `--net=host` in Docker
- Background goroutine handles pushing, triggered by `more` channel

## Configuration

### Environment Variables

- `ENABLE_PROMETHEUS_PUSHGATEWAY`: Set to "true" or "1" to enable pushing metrics to a Prometheus Pushgateway
- `PROMETHEUS_PUSHGATEWAY_URL`: URL of Prometheus Pushgateway or compatible service (e.g., VictoriaMetrics). Required when `ENABLE_PROMETHEUS_PUSHGATEWAY` is true
- `JOB_NAME`: Job label for pushed metrics (default: "stormglass")
- `ENABLE_PROMETHEUS_METRICS`: Set to "true" or "1" to expose `/metrics` endpoint for Prometheus scraping
- `PROMETHEUS_METRICS_PORT`: Port for the metrics endpoint (default: 9000)
- `ENABLE_RADAR`: Set to "true" or "1" to render the radar card and register `GET /api/radar/{site}` (default: false). `RADAR_SITE` and `STATION_LATITUDE`/`STATION_LONGITUDE` are checked at startup, and **every** unmet precondition is reported, not just the first: if either is missing, or if `RADAR_SITE` is not one of the 163 WSR-88D codes in `internal/radar/sites.go`, the card is not mounted and an ERROR names it — the process still starts and keeps ingesting. An unknown `RADAR_SITE` is reported with the nearest WSR-88D site to the station's coordinates and the distance to it, so the message names an answer rather than a source file; with no coordinates configured there is nothing to compute a hint from, and the unknown-code ERROR names the format rule only. The radar sidecar must also be running to serve tiles, but its absence is **not** checked at startup; it surfaces as failing tile requests at runtime.
- `ENABLE_FORECAST`: Set to "true" or "1" to enable the 7-day forecast card (default: false). **There is no forecast provider yet.** The WeatherFlow proxy was removed (issue #62, closed won't-do) and the tokenless NWS replacement is issue #81, so enabling this today logs an ERROR and mounts nothing.
- `ENABLE_ALMANAC`: Set to "true" or "1" to render the station almanac card and register `GET /api/almanac` (default: false). Requires `STATION_LATITUDE`/`STATION_LONGITUDE` and an observation store (SQLite is the default).
- `ENABLE_POSTGRES`: Set to "true" or "1" to enable writing metrics to PostgreSQL (opt-in; SQLite is the default store — see below)
- `SQLITE_PATH`: Path to the default SQLite database file (default: `/data/stormglass.db`)
- `SQLITE_BATCH_SIZE`: SQLite insert batch size (default: 100)
- `SQLITE_FLUSH_INTERVAL`: SQLite batch flush interval (default: 10s)
- `SQLITE_BUSY_TIMEOUT`: SQLite `busy_timeout` in milliseconds (default: 5000)
- `LOG_UDP`: Optional. Set to "true" or "1" to log all UDP broadcasts received (default: false)
- `STORMGLASS_SERIAL`: Optional. Sets the OTel resource attribute `stormglass.serial` (process-level station identity); the authoritative per-metric `serial` label comes from the UDP reports themselves
- `TOKEN`: Optional. When set, switches to API export mode for historical data

**Note:** In UDP mode, **SQLite is the default store**. If you set none of `ENABLE_PROMETHEUS_PUSHGATEWAY`, `ENABLE_PROMETHEUS_METRICS`, or `ENABLE_POSTGRES`, observations are still persisted to SQLite at `SQLITE_PATH` (default `/data/stormglass.db`). SQLite is written only in UDP mode, and is disabled only when `ENABLE_POSTGRES` is the sole configured store **and** `SQLITE_PATH` is unset. See **SQLite Storage (default store)** below.

### Station Identity

The store holds no coordinates and no UDP message carries any, so station
identity is configuration. No value is ever required, and no combination of
absent values can prevent the process from starting. A **malformed** value —
unparseable, out of range, non-finite, an unknown timezone, or a half-set
coordinate pair — is a fatal startup error naming every offending variable at
once.

- `STATION_LATITUDE` / `STATION_LONGITUDE`: decimal degrees, −90..90 and −180..180. Must be set **together**. Needed by the almanac and the radar card. Absent means those cards are not mounted.
- `STATION_ELEVATION`: metres. Display only; absent renders `—`.
- `STATION_NAME`: display only; absent renders `Tempest Station`.
- `STATION_TIMEZONE`: IANA name (e.g. `America/Denver`). **Defaults to `UTC`**. If coordinates are set and this is not, the almanac still mounts, but renders on UTC: sunrise/sunset become UTC clock times (a Denver station shows "Sunrise 2:17 PM · Sunset 11:39 PM" on the December solstice), the calendar windows fall on UTC boundaries, and the record date labels are UTC-dated. That case logs a **WARN** at startup — the card works, it is just not what most operators mean. Server-side only; it never appears on the wire, because the server preformats every timezone-dependent value.
- `RADAR_SITE`: WSR-88D site code (e.g. `TLX`, not the ICAO form `KTLX`). Read only when `ENABLE_RADAR` is true, and validated at startup against the 163-entry table in `internal/radar/sites.go`; an unknown code leaves the card unmounted with an ERROR naming the value.

## SQLite Storage (default store)

SQLite + Litestream is the **default** store in UDP mode: with no `ENABLE_POSTGRES`, observations are written to a local SQLite database at `SQLITE_PATH` (default `/data/stormglass.db`). PostgreSQL is opt-in and can run alongside SQLite (fan-out) when both are configured. SQLite is written only in UDP mode (not in API-export mode).

- **Driver:** `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0` — no CGO, preserving the static image). WAL journal mode; `busy_timeout=5000`, `synchronous=NORMAL`, `foreign_keys=ON`. WAL checkpointing is intentionally left to Litestream (no aggressive `wal_autocheckpoint`).
- **`/data` must be a writable mount.** If the SQLite database cannot be opened, the process **exits on startup** (fail-loud). Mount a writable volume at `/data`, or set `SQLITE_PATH` to a writable location.
- **Tunables:** `SQLITE_BATCH_SIZE` (default 100), `SQLITE_FLUSH_INTERVAL` (default 10s), `SQLITE_BUSY_TIMEOUT` (default 5000 ms).
- **Schema:** the same four typed tables as Postgres (`stormglass_observations`, `stormglass_rapid_wind`, `stormglass_hub_status`, `stormglass_events`), UUIDv7 text primary keys, unix-epoch **integer** timestamps; created via an embedded versioned migration (`schema_version`) on startup.
- **Litestream** runs as a sidecar streaming the WAL to S3/MinIO for backup/PITR; Litestream owns checkpointing. See the design doc for the sidecar config.

> **Migration note (upgrading from a pre-SQLite build):** existing UDP deployments that ran with only `ENABLE_PROMETHEUS_*` now **also** persist to SQLite by default. Ensure `/data` is a writable mount (or set `SQLITE_PATH` to a writable path) **before** upgrading, or the container will fail to start. There is no flag to disable the default store; to avoid a local database entirely, run Postgres as the sole store (`ENABLE_POSTGRES=true`, `SQLITE_PATH` unset).

## PostgreSQL Storage (Optional)

The exporter can write metrics to PostgreSQL in addition to (or instead of) Prometheus.

### Data Storage

**All UDP values stored as raw** - no unit conversions:
- Pressure: `mb` (millibars) from field 6
- Report Interval: `minutes` from field 17
- All other fields: stored exactly as received

### Configuration

**Option 1: Full connection string**
```bash
POSTGRES_URL=postgresql://user:pass@localhost:5432/weather
```

**Option 2: Individual components**
```bash
POSTGRES_HOST=postgres
POSTGRES_PORT=5432              # optional, default: 5432
POSTGRES_USERNAME=stormglass
POSTGRES_PASSWORD=secret
POSTGRES_NAME=weather
POSTGRES_SSLMODE=disable        # optional: disable, require, verify-ca, verify-full
```

**Optional tuning:**
```bash
POSTGRES_BATCH_SIZE=100         # default: 100
POSTGRES_FLUSH_INTERVAL=10s     # default: 10s
POSTGRES_MAX_RETRIES=3          # default: 3
```

### Database Schema

Four typed tables are automatically created on startup:
- `stormglass_observations` - Main weather data (~1/minute) with UUID primary keys
- `stormglass_rapid_wind` - High-frequency wind readings (~3 seconds)
- `stormglass_hub_status` - Device health metrics
- `stormglass_events` - Rain start and lightning strike events

All tables use UUIDv7 primary keys (generated in Go, no PostgreSQL extensions required).

### Operational Modes

| ENABLE_PROMETHEUS_PUSHGATEWAY | ENABLE_PROMETHEUS_METRICS | ENABLE_POSTGRES | TOKEN | Behavior |
|-------------------------------|---------------------------|-----------------|-------|----------|
| Yes | No | No | No | Push gateway only |
| No | Yes | No | No | Scrape endpoint only (`:9000/metrics`) |
| Yes | Yes | No | No | Both push gateway + scrape endpoint |
| Yes | No | Yes | No | Push gateway + Postgres |
| No | Yes | Yes | No | Scrape endpoint + Postgres |
| Yes | Yes | Yes | No | All three outputs |
| N/A | N/A | No | Yes | API export to .gz files |
| N/A | N/A | Yes | Yes | API export to Postgres (+ optional .gz files) |

> **SQLite default:** every UDP-mode row above (`TOKEN` unset) **also** persists to SQLite at `SQLITE_PATH` (default `/data/stormglass.db`), unless `ENABLE_POSTGRES` is the only configured store and `SQLITE_PATH` is unset. SQLite is not written in API-export mode (`TOKEN` set).

> **UI cards are gated separately from the operational mode.** `ENABLE_RADAR`,
> `ENABLE_FORECAST` and `ENABLE_ALMANAC` decide which optional cards the
> embedded UI mounts; they are orthogonal to every row above. The server
> reports them at `GET /api/capabilities`, and a disabled feature's API route
> is not registered at all (it 404s). All three default to false, so a
> deployment that sets none renders only the core dashboard.
>
> **The UI's optional cards no longer need a WeatherFlow credential.**
> `/api/station` is served from `STATION_*` configuration and `/api/almanac`
> from the local store plus computed astronomy, so both work in UDP mode with
> no token — issue #62 is closed as won't-do and #61 is resolved. The
> forecast card is the exception: a 7-day forecast is the one thing that
> cannot come from UDP or the store, and its tokenless NWS provider is issue
> #81. Leave `ENABLE_FORECAST` false until then.
>
> An unmet precondition is never fatal. `ENABLE_ALMANAC=true` without
> coordinates, or `ENABLE_RADAR=true` without `RADAR_SITE` — or with a
> `RADAR_SITE` that is not in the site table — logs an ERROR naming the
> problem, leaves the route unregistered and reports the capability false.
> The process starts and keeps ingesting; a card flag must never be able to
> stop the data path. A precondition that is *met* but degraded logs a **WARN**
> instead and the card still mounts: setting coordinates without
> `STATION_TIMEZONE` is the one such case, and renders every almanac time on
> UTC.

> **Migration note (upgrading from a pre-tokenless build):** a deployment
> running `ENABLE_FORECAST=true` or `ENABLE_RADAR=true` today is up and
> ingesting with a dead card. After this change it stays up and ingesting,
> the card is not mounted at all, and an ERROR names what is missing. **No
> configuration that starts today becomes unstartable.** A malformed
> `STATION_*` value or a malformed `ENABLE_*` boolean is still a fatal
> startup error — that is an operator error, not an unconfigured feature. To
> get the almanac and radar cards working, set
> `STATION_LATITUDE`/`STATION_LONGITUDE` (and `RADAR_SITE` for radar).
> `TOKEN`'s meaning is unchanged: it still selects API-export mode.

> **Subcommands bypass mode selection.** `backfill` and `healthcheck` are chosen
> by the first CLI argument, not by environment variables, and neither starts the
> UDP listener or the HTTP server. Any other non-flag first argument is a usage
> error (exit 2) rather than a silent fallthrough to daemon mode.

### Docker Compose Example

```yaml
services:
  stormglass:
    image: stormglass:latest
    network_mode: host
    environment:
      ENABLE_PROMETHEUS_PUSHGATEWAY: "true"
      PROMETHEUS_PUSHGATEWAY_URL: http://pushgateway:9091
      ENABLE_PROMETHEUS_METRICS: "true"  # Exposes /metrics on port 9000
      # PROMETHEUS_METRICS_PORT: "9090"  # Optional: override default port
      ENABLE_POSTGRES: "true"
      POSTGRES_HOST: postgres
      POSTGRES_USERNAME: stormglass
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_NAME: weather
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: weather
      POSTGRES_USER: stormglass
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U stormglass"]
      interval: 10s

volumes:
  pgdata:
```

### Prometheus Scrape Configuration

When using the metrics endpoint (`ENABLE_PROMETHEUS_METRICS=true`), configure Prometheus to scrape it:

```yaml
scrape_configs:
  - job_name: 'stormglass'
    static_configs:
      - targets: ['localhost:9000']  # Default port, or use PROMETHEUS_METRICS_PORT value
```

The `/metrics` endpoint exposes all weather station metrics in standard Prometheus format. A `/health` endpoint is also available for health checks.

### API Export Mode (TOKEN)

Setting `TOKEN` switches the process into the full historical-export path: it
fetches historical observation data via the REST API and writes it to
PostgreSQL and/or compressed files, then exits.

```bash
TOKEN=your_api_token ENABLE_POSTGRES=true POSTGRES_URL=postgresql://... go run .
```

Optionally keep .gz files:

```bash
TOKEN=your_api_token ENABLE_POSTGRES=true POSTGRES_URL=postgresql://... KEEP_EXPORT_FILES=true go run .
```

### Backfill Subcommand

Fills holes in the local observation history from the Tempest REST API. Unlike
API Export Mode (which is selected by setting `TOKEN` and runs the whole export
path), this is an explicit subcommand and can be run against a live database:

```bash
TOKEN=your_api_token stormglass backfill
TOKEN=... stormglass backfill --dry-run
TOKEN=... stormglass backfill --from 2026-07-01T00:00:00Z --to 2026-07-05T00:00:00Z
```

It writes to whichever store is configured (SQLite by default, Postgres when
`ENABLE_POSTGRES=true`) and is idempotent — re-running inserts nothing new.

| Flag | Default | Meaning |
|---|---|---|
| `--from`, `--to` | unset (auto-detect) | Explicit window, RFC3339 **UTC**. Must be given together. |
| `--min-gap` | `30m` | Smallest interval that counts as a gap. Raise it for stations with a long `report_interval`. |
| `--dry-run` | `false` | Detect and plan only: zero observation fetches, zero writes. It still lists devices, so it *does* validate the token. |
| `--store` | unset | `sqlite` or `postgres`. **Required** when both stores are configured. |

**`--store` and the fan-out configuration.** With `ENABLE_POSTGRES=true` *and*
`SQLITE_PATH` set, the daemon writes every observation to **both** stores.
Backfill repairs one store per run, so in that configuration it refuses to start
without `--store` rather than guessing — silently repairing Postgres while
leaving the Litestream-replicated SQLite database holed, and then reporting
success, would be worse than failing.

**Multiple sensors.** `backfill` enumerates every `ST` device on the account, so a
station with two Tempest units has both repaired. (This differs from `TOKEN`-mode
API export, which sees one device per station.)

**Exit codes:** `0` success — including `--help`, and including permanent holes
(windows the station was genuinely offline for, which the API cannot fill
either); `1` one or more gaps failed, or a runtime error; `2` usage error.

**Scope:** `stormglass_observations` only. There is no historical REST endpoint for
rapid wind, hub status, or discrete events. Lightning is partially recovered in
aggregate through the observation columns, but not as `stormglass_events` rows.

**Safety:** if the API's station serials and the store's serials have **no
overlap at all** (on a non-empty store), backfill stops and writes nothing — that
is the signature of a serial-format mismatch, which would create a parallel
series that never dedupes.

A *newly added* station, or one this host never hears over UDP, is **not** a
mismatch: its serial simply has no rows yet, and backfill fetches its whole
history normally.

## Testing Notes

Test files located alongside implementation:
- `internal/tempestudp/report_test.go`: UDP message parsing
- `internal/tempestudp/wetbulb_test.go`: Wet bulb calculations
- `internal/tempestapi/client_test.go`: API client
- `internal/tempestapi/observations_test.go`: Null-preserving REST decode
- `internal/weather/observation_test.go`: Store-neutral types
- `internal/backfill/`: Window chunking, retry classification, gap assembly, and the `Run` core
- `internal/sqlite/backfill_test.go`, `internal/postgres/backfill_test.go`: Gap detection and idempotent insert
- `backfill_cmd_test.go`: Subcommand dispatch and flag parsing

Postgres tests that need a live database skip unless `POSTGRES_URL` is set:

```bash
docker run --rm -d --name pg-test -e POSTGRES_PASSWORD=x -e POSTGRES_DB=weather -p 55432:5432 postgres:16
POSTGRES_URL='postgres://postgres:x@localhost:55432/weather?sslmode=disable' go test ./internal/postgres/ -count=1
docker rm -f pg-test
```

Go 1.25.0+ required (see go.mod; the pinned toolchain is go1.26.6).
