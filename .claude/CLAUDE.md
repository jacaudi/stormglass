# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A self-hosted appliance for Tempest weather stations. It listens for the hub's
UDP broadcasts on :50222, persists observations, serves an embedded dashboard,
and exports metrics. It operates in two modes:

1. **UDP Listener Mode** (default): ingests broadcasts and writes them to the
   configured stores — **SQLite by default**, PostgreSQL when opted in, both
   when both are configured — while serving the dashboard and metrics.
2. **API Export Mode**: when `TOKEN` is set, fetches historical observations via
   REST, writes them to PostgreSQL and/or gzipped files, and exits.

`backfill` and `healthcheck` are subcommands selected by the first CLI argument
and bypass mode selection entirely.

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
# The documented quickstart: host networking for UDP broadcasts, writable /data
docker run -it --rm --net=host -v stormglass-data:/data stormglass:latest
```

Every other invocation is a matter of which environment variables are set. See
`docs/configuration.md` rather than restating them here.

## Architecture

### Operational Modes

The application switches modes on the presence of `TOKEN`:
- **No TOKEN**: `listenAndPushWithSink()` (`main.go:789`) — UDP listener feeding
  every configured writer.
- **With TOKEN**: `exportWithSink()` (`main.go:924`) — historical export, then exit.

### Package Structure

- **`cmd/stormglass/`**: The `package main` entry point — mode selection, the UDP listener, and the `backfill`/`healthcheck` subcommands
- **`internal/metrics/`**: Defines all Prometheus metric descriptors (`prometheus.Desc`)
- **`internal/tempestudp/`**: Parses UDP broadcast messages into metrics, includes wet bulb temperature calculations
- **`internal/tempestapi/`**: REST API client for fetching historical observations
- **`internal/weather/`**: Store-neutral `Observation`, `Gap`, and `Bounds` types shared by the REST client and both stores
- **`internal/backfill/`**: Gap detection and API backfill orchestration for the `backfill` subcommand
- **`internal/sink/`**: `MetricsSink` — the fan-out point. Every output registers as a writer via `AddWriter`
- **`internal/sqlite/`**, **`internal/postgres/`**: the two stores, each a sink writer
- **`internal/otel/`**: the supported metrics path (OTLP)
- **`internal/prometheus/`**: the **deprecated** scrape/push writers, plus `deprecation.go`
- **`internal/httpserver/`**: the dashboard and `/api/*` handlers
- **`internal/config/`**: `STATION_*` decoding, Postgres connection settings, boolean/float env helpers
- **`internal/astro/`**: sunrise/sunset and almanac astronomy
- **`internal/radar/`**: WSR-88D site table and the sidecar proxy

### Data Flow (UDP Mode)

1. UDP packets received on :50222 → `listen()` (`main.go:818`)
2. Raw bytes → `tempestudp.ParseReport()` → Report struct
3. Report → `Report.Metrics()` → `[]prometheus.Metric`
4. Handed to `sink.MetricsSink`, which fans out to **every registered writer**:
   SQLite, Postgres, OTel, and the deprecated Prometheus scrape/push writers
5. Each writer batches and flushes on its own schedule

The fan-out is the architecture; any individual sink is optional. The
`outbox` channel and `pusher.Add()` are internals of the *deprecated*
Prometheus push writer (`internal/prometheus/writer.go`), not the general path.

### Key Design Patterns

- **A single fan-out sink**, not a pipeline: outputs are writers registered on
  `sink.MetricsSink`, so adding one touches no existing sink.
- **SQLite is the default store**, and failure to open it is fatal at startup —
  fail-loud rather than silently running without persistence.
- **UDP broadcasts are link-local**, requiring `--net=host` in Docker.
- The deprecated Prometheus push writer buffers into an `outbox` channel and
  drains it non-blockingly via a custom collector, because stations broadcast
  sporadically. This is a property of that writer, not of the application.

## Configuration

**`docs/configuration.md` is authoritative** for the complete environment
variable surface (37 variables), the operational-mode matrix, and the
fatal-versus-degraded startup rules. It is derived from the code's real read
sites. Do not restate it here — two hand-maintained copies of one surface is
what produced issue #217.

Points an agent gets wrong most often:

- **`ENABLE_OTEL` is the supported metrics path.** The entire
  `ENABLE_PROMETHEUS_*` surface (`_METRICS`, `_PUSHGATEWAY`) is **deprecated and
  slated for removal in the next release** — see
  `internal/prometheus/deprecation.go`, which is the source of truth, not just
  prose. Enabling either logs a one-time `WARN`.
- **SQLite is the default store.** With no `ENABLE_POSTGRES`, observations are
  written to `SQLITE_PATH` (default `/data/stormglass.db`), and the process
  **exits at startup** if that database cannot be opened. Postgres is opt-in and
  can fan out alongside SQLite.
- **A malformed value is fatal; a missing one is not.** Unparseable or
  out-of-range values abort startup naming *every* offender at once. An
  unmet optional-card precondition logs an `ERROR`, leaves the route
  unregistered, and lets ingestion continue — a card flag must never be able to
  stop the data path.
- **Booleans go through `strconv.ParseBool`**, so `1`/`t`/`T`/`TRUE` all work,
  and an unparseable value is a fatal error rather than a silent false.

Related user-facing pages: `docs/storage.md` (schema, Litestream, the raw-units
contract), `docs/metrics.md` (the two paths emit different metric names),
`docs/dashboard.md` (optional cards, endpoints), `docs/backfill.md`,
`docs/modes.md`, `docs/deployment/`.

## Testing Notes

Test files located alongside implementation:
- `internal/tempestudp/report_test.go`: UDP message parsing
- `internal/tempestudp/wetbulb_test.go`: Wet bulb calculations
- `internal/tempestapi/client_test.go`: API client
- `internal/tempestapi/observations_test.go`: Null-preserving REST decode
- `internal/weather/observation_test.go`: Store-neutral types
- `internal/backfill/`: Window chunking, retry classification, gap assembly, and the `Run` core
- `internal/sqlite/backfill_test.go`, `internal/postgres/backfill_test.go`: Gap detection and idempotent insert
- `cmd/stormglass/backfill_cmd_test.go`: Subcommand dispatch and flag parsing

Postgres tests that need a live database skip unless `POSTGRES_URL` is set:

```bash
docker run --rm -d --name pg-test -e POSTGRES_PASSWORD=x -e POSTGRES_DB=weather -p 55432:5432 postgres:16
POSTGRES_URL='postgres://postgres:x@localhost:55432/weather?sslmode=disable' go test ./internal/postgres/ -count=1
docker rm -f pg-test
```

Go 1.25.0+ required (see go.mod; the pinned toolchain is go1.26.6).
