# Configuration

Every setting is an environment variable — Stormglass has no config file. This
page is the authoritative reference for the **37 variables Stormglass itself
reads**; see [Scope](#scope) for what deliberately lives elsewhere.

## How values are read

**Booleans** are parsed by Go's `strconv.ParseBool`, so `true`, `TRUE`, `True`,
`t`, `T` and `1` all enable a flag, and `false`, `FALSE`, `False`, `f`, `F` and
`0` all disable it. Unset means false. **Any other value is a fatal startup
error** naming the variable — a typo like `ENABLE_RADAR=yes` stops the process
rather than silently disabling the card.

**Malformed is fatal; missing is not.** A value that cannot be parsed — an
unparseable or out-of-range coordinate, a non-finite number, an unknown
timezone, half of a coordinate pair — aborts startup, and **every** offending
variable is named at once rather than just the first. An *absent* value never
prevents startup: an optional card whose prerequisites are missing logs an
`ERROR`, leaves its route unregistered, reports itself false at
`/api/capabilities`, and the data path keeps running. A card flag can never
take down ingestion.

The one middle case is a prerequisite that is met but degraded: coordinates set
without `STATION_TIMEZONE` logs a `WARN` and the almanac still mounts, rendering
every time on UTC.

## Mode selection

| Condition | Mode |
|---|---|
| `TOKEN` unset | **UDP listener** — the daemon. Listens on UDP :50222, serves the dashboard and metrics, writes to the configured stores |
| `TOKEN` set | **API export** — fetches historical observations over REST, writes them to Postgres and/or gzipped files, then exits |
| first CLI arg is `backfill` | **Backfill subcommand** — see [Backfill](backfill.md) |
| first CLI arg is `healthcheck` | **Healthcheck** — probes a running server's `/healthz` |

Subcommands are selected by argument, not environment, and neither starts the
UDP listener or the HTTP server. Any other non-flag first argument is a usage
error (exit 2).

## Core

| Variable | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Bind address for the dashboard and JSON API. Accepts `:8080` or `0.0.0.0:8080` |
| `TOKEN` | — | WeatherFlow API token. **Setting it switches the process to API-export mode.** Also required by the `backfill` subcommand |
| `LOG_UDP` | `false` | Log every UDP broadcast received. The fastest way to tell whether the station reaches the container at all |
| `KEEP_EXPORT_FILES` | `false` | In API-export mode, retain the `.gz` files instead of deleting them after a successful database write |
| `STORMGLASS_SERIAL` | — | Sets the OTel resource attribute `stormglass.serial` (process-level identity). The authoritative per-metric `serial` label always comes from the reports themselves |

## Storage

SQLite is the **default** store. With nothing set, observations are written to
`/data/stormglass.db`. Postgres is opt-in and can run alongside SQLite.

| `ENABLE_POSTGRES` | `SQLITE_PATH` | Result |
|---|---|---|
| unset / false | anything | **SQLite only**, at `SQLITE_PATH` or `/data/stormglass.db` |
| true | set | **Both** — every observation is written to each (fan-out) |
| true | unset | **Postgres only** — no local database |

There is no flag that turns storage off. To avoid a local database entirely,
make Postgres the sole store. SQLite is written in UDP mode only, never in
API-export mode.

### SQLite

| Variable | Default | Meaning |
|---|---|---|
| `SQLITE_PATH` | `/data/stormglass.db` | Database file. Its directory must be writable — **if the database cannot be opened the process exits at startup** |
| `SQLITE_BATCH_SIZE` | `100` | Rows per insert batch |
| `SQLITE_FLUSH_INTERVAL` | `10s` | Batch flush interval. Any Go duration string |
| `SQLITE_BUSY_TIMEOUT` | `5000` | SQLite `busy_timeout`, in **milliseconds** |

An unparseable or non-positive value for the three tunables falls back to its
default rather than failing — they are performance knobs, not correctness ones.

### PostgreSQL

Supply either a full connection string or the components. `POSTGRES_URL` wins
when both are present.

| Variable | Default | Meaning |
|---|---|---|
| `ENABLE_POSTGRES` | `false` | Turn on the Postgres store |
| `POSTGRES_URL` | — | Full connection string, e.g. `postgresql://user:pass@host:5432/weather` |
| `POSTGRES_HOST` | — | Component form: host |
| `POSTGRES_PORT` | `5432` | Component form: port |
| `POSTGRES_USERNAME` | — | Component form: user |
| `POSTGRES_PASSWORD` | — | Component form: password |
| `POSTGRES_NAME` | — | Component form: database name |
| `POSTGRES_SSLMODE` | `disable` | `disable`, `require`, `verify-ca` or `verify-full` |
| `POSTGRES_BATCH_SIZE` | `100` | Rows per insert batch |
| `POSTGRES_FLUSH_INTERVAL` | `10s` | Batch flush interval |
| `POSTGRES_MAX_RETRIES` | `3` | Write retry attempts |

See [Storage](storage.md) for the schema and the Litestream replication path.

## Metrics

> **`ENABLE_PROMETHEUS_*` is deprecated** and slated for removal in the next
> release; `ENABLE_OTEL` is the replacement. Enabling either logs a one-time
> `WARN` at startup. The deprecation is recorded in
> `internal/prometheus/deprecation.go`, not just in documentation.

| Variable | Default | Meaning |
|---|---|---|
| `ENABLE_OTEL` | `false` | Emit OTLP to a collector — **the supported path** |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Collector endpoint. Required when `ENABLE_OTEL` is true |
| `ENABLE_PROMETHEUS_METRICS` | `false` | *(deprecated)* Serve `/metrics` for scraping |
| `PROMETHEUS_METRICS_PORT` | `9000` | Port for that endpoint |
| `ENABLE_PROMETHEUS_PUSHGATEWAY` | `false` | *(deprecated)* Push to a gateway instead |
| `PROMETHEUS_PUSHGATEWAY_URL` | — | Gateway URL. Required when pushing |
| `JOB_NAME` | `stormglass` | `job` label on pushed metrics |

**The two paths emit different metric names**, and the bundled Grafana dashboard
only works with the OTel one. This trips people up; see
[Metrics](metrics.md) before choosing.

## Station identity

No UDP message carries the station's location, so identity is configuration.
Nothing here is required and no combination of missing values prevents startup.

| Variable | Default | Meaning |
|---|---|---|
| `STATION_LATITUDE` | — | Decimal degrees, −90 to 90. **Must be set together with longitude** |
| `STATION_LONGITUDE` | — | Decimal degrees, −180 to 180. **Must be set together with latitude** |
| `STATION_ELEVATION` | — | Metres. Display only; any finite value is accepted. Absent renders `—` |
| `STATION_NAME` | — | Display only. When absent the dashboard renders `Tempest Station` |
| `STATION_TIMEZONE` | `UTC` | IANA name, e.g. `America/Denver`. Server-side only — the server preformats every timezone-dependent value, so it never appears on the wire |

Setting only one of latitude/longitude is a fatal startup error. Both are
required by the almanac and radar cards.

**`STATION_TIMEZONE` matters more than it looks.** With coordinates set and the
timezone unset, the almanac mounts but renders on UTC: sunrise and sunset become
UTC clock times (a Denver station shows "Sunrise 2:17 PM · Sunset 11:39 PM" on
the December solstice), calendar windows fall on UTC boundaries, and record date
labels are UTC-dated. That case logs a `WARN`.

## Dashboard cards

All three default off and are independent of the storage and metrics choices.

| Variable | Default | Requires | If the requirement is missing |
|---|---|---|---|
| `ENABLE_ALMANAC` | `false` | coordinates + a store | Card not mounted, `ERROR` logged, process keeps running |
| `ENABLE_RADAR` | `false` | `RADAR_SITE` + coordinates + the radar sidecar | As above. **Every** unmet precondition is reported, not just the first |
| `ENABLE_FORECAST` | `false` | **nothing yet — there is no forecast provider** | Card not mounted, `ERROR` logged |

`ENABLE_FORECAST` has nothing behind it. The WeatherFlow proxy was removed
(issue #62, closed won't-do) and the tokenless NWS replacement is
[#81](https://github.com/jacaudi/stormglass/issues/81). Leave it false.

### Radar

| Variable | Default | Meaning |
|---|---|---|
| `RADAR_SITE` | — | WSR-88D site code — `TLX`, **not** the ICAO form `KTLX`. Validated at startup against the 163-entry table in `internal/radar/sites.go` |
| `RADAR_SIDECAR_URL` | `http://radar-sidecar:8081` | Where to reach the radar sidecar |

An unknown `RADAR_SITE` leaves the card unmounted and logs an `ERROR` naming the
**nearest** WSR-88D site to your coordinates and the distance to it, so the
message names an answer rather than a source file. With no coordinates set there
is nothing to compute that hint from, and the error names the format rule
instead.

The sidecar's absence is **not** checked at startup; it surfaces as failing tile
requests at runtime.

## Operational modes

Every row below is UDP mode (`TOKEN` unset) unless stated.

| Pushgateway | Scrape | OTel | Postgres | Behaviour |
|---|---|---|---|---|
| — | — | — | — | SQLite only (the default appliance) |
| yes | — | — | — | Push gateway + SQLite |
| — | yes | — | — | Scrape endpoint on `:9000` + SQLite |
| — | — | yes | — | OTLP + SQLite |
| — | — | — | yes | Postgres (+ SQLite if `SQLITE_PATH` set) |
| yes | yes | yes | yes | All sinks at once |
| *(any)* | | | yes | **`TOKEN` set:** API export to Postgres and/or `.gz` files, then exit |

Every UDP-mode row also persists to SQLite unless Postgres is the only
configured store and `SQLITE_PATH` is unset.

## Scope

This page covers what **Stormglass** reads. Two neighbouring sets do not appear
above, deliberately:

- **OpenTelemetry SDK variables.** Stormglass passes only the endpoint and
  insecure flag to the exporter, so the SDK's own environment configuration
  stays live — `OTEL_EXPORTER_OTLP_HEADERS`, `_TIMEOUT`, `_COMPRESSION`,
  `_CERTIFICATE`, `_METRICS_TEMPORALITY_PREFERENCE` and
  `OTEL_METRIC_EXPORT_INTERVAL` among them. They are defined by the
  [OpenTelemetry specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/),
  not by this project.
- **Variables belonging to the shipped Compose stack** — `LITESTREAM_BUCKET`,
  `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`,
  `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD` and `GF_SECURITY_ADMIN_PASSWORD`.
  Those configure Litestream, MinIO and Grafana. See
  [Docker Compose](deployment/docker-compose.md).
