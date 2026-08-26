# Stormglass

Self-hosted appliance for [Tempest weather stations](https://weatherflow.com/tempest-home-weather-system/).

This tool provides:
- **UDP Mode**: Listens for [Tempest UDP broadcasts](https://weatherflow.github.io/Tempest/api/udp.html) and persists to a local **SQLite** database by default, with an optional Prometheus push gateway / scrape endpoint and/or PostgreSQL
- **API Export Mode**: Fetches historical data via REST API and stores to PostgreSQL and/or compressed files

## Quickstart

Container images are available at [GitHub
Container Registry](https://github.com/jacaudi/stormglass/pkgs/container/stormglass).

```bash
$ docker run -it --rm --net=host \
  -v stormglass-data:/data \
  ghcr.io/jacaudi/stormglass

starting UDP listener mode
listening on UDP :50222
```

By default this persists observations to a local SQLite database at `/data/stormglass.db`, so a writable `/data` is required (mounted above as a named volume). Prometheus and/or PostgreSQL outputs are opt-in — see [Exporter configuration](#exporter-configuration).

Note that `--net=host` is used here because UDP broadcasts are link-local and therefore cannot be received from typical
(routed) container networks.

## Exporter configuration

Via environment variables. SQLite is the default store (below); Prometheus and PostgreSQL are opt-in.

**Prometheus (optional)** — push and/or scrape:

* `ENABLE_PROMETHEUS_PUSHGATEWAY`: set to `true`/`1` to push metrics to a [Pushgateway](https://github.com/prometheus/pushgateway) or [compatible service](https://docs.victoriametrics.com/?highlight=exposition#how-to-import-data-in-prometheus-exposition-format) (e.g. VictoriaMetrics)
* `PROMETHEUS_PUSHGATEWAY_URL`: the Pushgateway URL (required when `ENABLE_PROMETHEUS_PUSHGATEWAY` is set)
* `JOB_NAME`: the value for the `job` label (default: `"stormglass"`)
* `ENABLE_PROMETHEUS_METRICS`: set to `true`/`1` to expose a `/metrics` scrape endpoint
* `PROMETHEUS_METRICS_PORT`: scrape endpoint port (default: `9000`)

### SQLite Storage (default)

In UDP mode the exporter persists observations to a local **SQLite** database by default (pure-Go `modernc.org/sqlite`, no CGO), in WAL mode suited to [Litestream](https://litestream.io/) streaming backup.

* `SQLITE_PATH`: path to the database file (default: `/data/stormglass.db`)
* Optional tuning: `SQLITE_BATCH_SIZE` (default `100`), `SQLITE_FLUSH_INTERVAL` (default `10s`), `SQLITE_BUSY_TIMEOUT` in ms (default `5000`)

`/data` must be a writable mount — if the database cannot be opened, the process exits on startup. Set `ENABLE_POSTGRES=true` to use PostgreSQL instead; both can run together (fan-out). SQLite is not written in API-export mode. See `CLAUDE.md` for the full schema and a Litestream sidecar example.

### PostgreSQL Storage (Optional)

The exporter can optionally write to PostgreSQL in addition to (or instead of) SQLite/Prometheus. Set `ENABLE_POSTGRES=true`/`1`, then configure using either:

* `POSTGRES_URL`: Full PostgreSQL connection string (e.g., `postgresql://user:pass@host:5432/dbname`)
* Or individual components: `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USERNAME`, `POSTGRES_PASSWORD`, `POSTGRES_NAME`, `POSTGRES_SSLMODE`

When enabled, the exporter automatically creates and maintains typed tables for observations, rapid wind data, hub status, and events.

See `CLAUDE.md` for detailed configuration options and Docker Compose examples

## Source Credit

- [tempest-exporter](https://github.com/willglynn/tempest_exporter) - Started as a fork of this project.
