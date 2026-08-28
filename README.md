# Stormglass

A self-hosted appliance for [WeatherFlow Tempest](https://weatherflow.com/tempest-home-weather-system/)
weather stations. Point it at your station's UDP broadcasts and it gives you a
live dashboard, a durable observation history, and Prometheus metrics — with no
WeatherFlow account, no API token, and no cloud dependency.

> [!WARNING]
> **Agentically generated.** This codebase was produced through agentic,
> spec-driven development: each feature began as a written design and
> implementation spec, then a coding agent executed the plan under human
> review. Tests, code review, and CI gates apply as they would for any
> project, but the authorship pattern is not a single human contributor —
> keep that in mind when evaluating fit for your environment.

![The Stormglass dashboard](docs/images/dashboard.png)

## What it does

Your Tempest hub broadcasts every observation onto your LAN once a minute, plus
rapid-wind samples every three seconds. Stormglass listens for those broadcasts,
stores them, and serves them back as a dashboard and as metrics. Nothing leaves
your network, and nothing depends on WeatherFlow's cloud staying up.

## Features

- **Live dashboard** — current conditions, wind compass, humidity, pressure,
  solar and UV, precipitation, lightning and station health, plus a records
  strip and a station almanac. Embedded in the binary.
- **Durable history** — SQLite by default (with optional Litestream
  replication), PostgreSQL if you want it, or both at once.
- **Metrics** — OpenTelemetry, or a Prometheus scrape endpoint or push gateway.
- **Optional radar** — NEXRAD Level II tiles rendered by a sidecar.
- **Gap repair** — a `backfill` subcommand fills holes in local history from the
  Tempest REST API, if you have a token.

Everything except backfill works with **no WeatherFlow credential at all**.

## Quick start

```bash
docker run -d --name stormglass \
  --net=host \
  -v stormglass-data:/data \
  ghcr.io/jacaudi/stormglass
```

Open <http://localhost:8080>. The first observation usually lands within a
minute.

Two things about that command are not optional:

- **`--net=host` is required.** Tempest stations broadcast to UDP :50222 as
  link-local traffic. Docker's default bridge network does not deliver those
  broadcasts to a container, so without host networking Stormglass starts
  cleanly and then sits there receiving nothing.
- **`/data` must be writable.** SQLite is the default store, and if the database
  cannot be opened the process **exits at startup** rather than silently running
  without persistence. The named volume above satisfies this.

That is the whole required configuration. Everything else is optional — the
three variables most people set next are `STATION_LATITUDE` /
`STATION_LONGITUDE` and `STATION_TIMEZONE`, which turn on the almanac:

```bash
docker run -d --name stormglass --net=host -v stormglass-data:/data \
  -e STATION_LATITUDE=39.7392 -e STATION_LONGITUDE=-104.9903 \
  -e STATION_TIMEZONE=America/Denver -e ENABLE_ALMANAC=true \
  ghcr.io/jacaudi/stormglass
```

## How it works

One process. It listens, stores, and serves — there is no queue, no scheduler,
and no external database unless you ask for one.

```
Tempest station ──UDP :50222──▶ listener ──▶ SQLite (default)
                                    │         └▶ PostgreSQL (opt-in)
                                    ├────────▶ dashboard  (HTTP :8080)
                                    └────────▶ metrics    (OTel / Prometheus)
```

Broadcasts are parsed into typed observations, batched, and written to whichever
stores are configured. The same values feed the embedded dashboard and the
metrics exporters. Optional cards (almanac, radar) are gated by configuration
and never block ingestion: if one is misconfigured it logs an error, stays
unmounted, and the data path keeps running.

## Documentation

**Getting started**

- [Configuration](docs/configuration.md) — every environment variable, the
  operational-mode matrix, and what is fatal at startup versus merely degraded

**Configuration**

- [Storage](docs/storage.md) — SQLite, Litestream replication, PostgreSQL, schema
- [Metrics](docs/metrics.md) — OTel and the deprecated Prometheus paths, and why
  they emit different metric names
- [The dashboard](docs/dashboard.md) — optional cards, station identity, HTTP
  endpoints, the radar basemap

**Operations**

- [Docker Compose](docs/deployment/docker-compose.md) — the full stack with
  Litestream, MinIO, Prometheus and Grafana
- [Kubernetes](docs/deployment/kubernetes.md) — bjw-s `app-template`, Flux, and
  the two settings you cannot drop
- [Backfill](docs/backfill.md) — repairing gaps from the REST API
- [Other modes](docs/modes.md) — API export, healthcheck, debugging the UDP path

**Development**

- [Development](docs/development.md) — the CI contract, building, tests

## Credits

Started as a fork of [tempest_exporter](https://github.com/willglynn/tempest_exporter).
The dashboard is vendored from [tempest-display](https://github.com/jacaudi/tempest-display).

Licensed under the [MIT License](LICENSE).
