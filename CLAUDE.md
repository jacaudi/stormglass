# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-backend data utilities for Tempest weather stations. The application operates in two modes:

1. **UDP Listener Mode** (default): Listens for Tempest UDP broadcasts on port 50222 and writes to Prometheus push gateway and/or PostgreSQL in real-time
2. **API Export Mode**: Fetches historical observation data via REST API when `TOKEN` env var is set, writes to PostgreSQL and/or compressed files

## Development Commands

### The CI contract

`task ci` is the whole gate, and it is exactly what CI runs — `ci-test.yml`
invokes `task ci` and nothing else. If a check cannot be run locally with this
command, it does not belong in a workflow.

```bash
task ci          # everything CI runs: lint, format, tidy, vuln, tests, node
task lint        # static checks only
task test        # tests only
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
  tempestwx-utilities:latest

# UDP mode with scrape endpoint (Prometheus pulls from /metrics)
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_METRICS=true \
  tempestwx-utilities:latest

# UDP mode with scrape endpoint on custom port
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_METRICS=true \
  -e PROMETHEUS_METRICS_PORT=9090 \
  tempestwx-utilities:latest

# UDP mode with both push gateway and scrape endpoint
docker run -it --rm --net=host \
  -e ENABLE_PROMETHEUS_PUSHGATEWAY=true \
  -e PROMETHEUS_PUSHGATEWAY_URL=http://localhost:9091 \
  -e ENABLE_PROMETHEUS_METRICS=true \
  tempestwx-utilities:latest

# API export mode
docker run -it --rm \
  -e TOKEN=your_token \
  tempestwx-utilities:latest
```

## Architecture

### Operational Modes

The application switches modes based on presence of `TOKEN` environment variable:
- **No TOKEN**: Runs `listenAndPush()` - UDP listener with push gateway
- **With TOKEN**: Runs `export()` - Historical data export to gzipped files

### Internal Package Structure

- **`internal/tempest/`**: Defines all Prometheus metric descriptors (`prometheus.Desc`)
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
- `JOB_NAME`: Job label for pushed metrics (default: "tempest")
- `ENABLE_PROMETHEUS_METRICS`: Set to "true" or "1" to expose `/metrics` endpoint for Prometheus scraping
- `PROMETHEUS_METRICS_PORT`: Port for the metrics endpoint (default: 9000)
- `ENABLE_RADAR`: Set to "true" or "1" to advertise the radar capability and register `GET /api/radar/{site}` (default: false; requires the radar sidecar). It does not produce a radar overlay on its own — the UI also needs station coordinates and the deferred RADAR_SITE→UI wiring
- `ENABLE_FORECAST`: Set to "true" or "1" to enable the 7-day forecast card and register `GET /api/forecast` (default: false)
- `ENABLE_ALMANAC`: Set to "true" or "1" to enable the station almanac card and register `GET /api/almanac` (default: false)
- `ENABLE_POSTGRES`: Set to "true" or "1" to enable writing metrics to PostgreSQL (opt-in; SQLite is the default store — see below)
- `SQLITE_PATH`: Path to the default SQLite database file (default: `/data/tempest.db`)
- `SQLITE_BATCH_SIZE`: SQLite insert batch size (default: 100)
- `SQLITE_FLUSH_INTERVAL`: SQLite batch flush interval (default: 10s)
- `SQLITE_BUSY_TIMEOUT`: SQLite `busy_timeout` in milliseconds (default: 5000)
- `LOG_UDP`: Optional. Set to "true" or "1" to log all UDP broadcasts received (default: false)
- `TEMPEST_SERIAL`: Optional. Sets the OTel resource attribute `tempest.serial` (process-level station identity); the authoritative per-metric `serial` label comes from the UDP reports themselves
- `TOKEN`: Optional. When set, switches to API export mode for historical data

**Note:** In UDP mode, **SQLite is the default store**. If you set none of `ENABLE_PROMETHEUS_PUSHGATEWAY`, `ENABLE_PROMETHEUS_METRICS`, or `ENABLE_POSTGRES`, observations are still persisted to SQLite at `SQLITE_PATH` (default `/data/tempest.db`). SQLite is written only in UDP mode, and is disabled only when `ENABLE_POSTGRES` is the sole configured store **and** `SQLITE_PATH` is unset. See **SQLite Storage (default store)** below.

## SQLite Storage (default store)

SQLite + Litestream is the **default** store in UDP mode: with no `ENABLE_POSTGRES`, observations are written to a local SQLite database at `SQLITE_PATH` (default `/data/tempest.db`). PostgreSQL is opt-in and can run alongside SQLite (fan-out) when both are configured. SQLite is written only in UDP mode (not in API-export mode).

- **Driver:** `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0` — no CGO, preserving the static image). WAL journal mode; `busy_timeout=5000`, `synchronous=NORMAL`, `foreign_keys=ON`. WAL checkpointing is intentionally left to Litestream (no aggressive `wal_autocheckpoint`).
- **`/data` must be a writable mount.** If the SQLite database cannot be opened, the process **exits on startup** (fail-loud). Mount a writable volume at `/data`, or set `SQLITE_PATH` to a writable location.
- **Tunables:** `SQLITE_BATCH_SIZE` (default 100), `SQLITE_FLUSH_INTERVAL` (default 10s), `SQLITE_BUSY_TIMEOUT` (default 5000 ms).
- **Schema:** the same four typed tables as Postgres (`tempest_observations`, `tempest_rapid_wind`, `tempest_hub_status`, `tempest_events`), UUIDv7 text primary keys, unix-epoch **integer** timestamps; created via an embedded versioned migration (`schema_version`) on startup.
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
POSTGRES_USERNAME=tempest
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
- `tempest_observations` - Main weather data (~1/minute) with UUID primary keys
- `tempest_rapid_wind` - High-frequency wind readings (~3 seconds)
- `tempest_hub_status` - Device health metrics
- `tempest_events` - Rain start and lightning strike events

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

> **SQLite default:** every UDP-mode row above (`TOKEN` unset) **also** persists to SQLite at `SQLITE_PATH` (default `/data/tempest.db`), unless `ENABLE_POSTGRES` is the only configured store and `SQLITE_PATH` is unset. SQLite is not written in API-export mode (`TOKEN` set).

> **UI cards are gated separately from the operational mode.** `ENABLE_RADAR`,
> `ENABLE_FORECAST` and `ENABLE_ALMANAC` decide which optional cards the
> embedded UI mounts; they are orthogonal to every row above. The server
> reports them at `GET /api/capabilities`, and a disabled feature's API route
> is not registered at all (it 404s). All three default to false, so a
> deployment that sets none renders only the core dashboard.
>
> **Enabling a flag does not yet make its card work.** The container only
> serves the UI when `TOKEN` is unset (a set `TOKEN` switches it to
> API-export mode), so the WeatherFlow-backed routes have no credential:
> issues #62 (a UDP-mode token source) and #61 (response shaping) must land
> before Forecast and Almanac render real data, and Radar additionally needs
> the deferred RADAR_SITE→UI wiring. Leave all three false until then. The
> same caveat is stated in `deploy/.env.example` and `deploy/docker-compose.yml`.

> **Subcommands bypass mode selection.** `backfill` and `healthcheck` are chosen
> by the first CLI argument, not by environment variables, and neither starts the
> UDP listener or the HTTP server. Any other non-flag first argument is a usage
> error (exit 2) rather than a silent fallthrough to daemon mode.

### Docker Compose Example

```yaml
services:
  tempest-utilities:
    image: tempestwx-utilities:latest
    network_mode: host
    environment:
      ENABLE_PROMETHEUS_PUSHGATEWAY: "true"
      PROMETHEUS_PUSHGATEWAY_URL: http://pushgateway:9091
      ENABLE_PROMETHEUS_METRICS: "true"  # Exposes /metrics on port 9000
      # PROMETHEUS_METRICS_PORT: "9090"  # Optional: override default port
      ENABLE_POSTGRES: "true"
      POSTGRES_HOST: postgres
      POSTGRES_USERNAME: tempest
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_NAME: weather
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: weather
      POSTGRES_USER: tempest
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tempest"]
      interval: 10s

volumes:
  pgdata:
```

### Prometheus Scrape Configuration

When using the metrics endpoint (`ENABLE_PROMETHEUS_METRICS=true`), configure Prometheus to scrape it:

```yaml
scrape_configs:
  - job_name: 'tempest'
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
TOKEN=your_api_token tempestwx-utilities backfill
TOKEN=... tempestwx-utilities backfill --dry-run
TOKEN=... tempestwx-utilities backfill --from 2026-07-01T00:00:00Z --to 2026-07-05T00:00:00Z
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

**Scope:** `tempest_observations` only. There is no historical REST endpoint for
rapid wind, hub status, or discrete events. Lightning is partially recovered in
aggregate through the observation columns, but not as `tempest_events` rows.

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

Go 1.25.0+ required (see go.mod; the pinned toolchain is go1.26.1).
