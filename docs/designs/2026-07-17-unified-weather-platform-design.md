# Unified Weather Platform — Design

**Date:** 2026-07-17
**Status:** Design (Phase 1 of Design → Plan → Cold-Review). No code changes.
**Repo:** `tempestwx-exporter` (module `tempestwx-utilities`, go 1.24)
**Author:** senior-Go design pass (non-interactive; open decisions surfaced in-doc, see §17)
**Revisions:**
- 2026-07-17 — per user: **O1** radar decode → **Python Py-ART sidecar** (reuse DRAS), **O3** output → **contoured GeoJSON isobands**, **O5** → **the app only ships OTLP signals; trace/log backends are the operator's choice** (not prescribed). Reflected throughout.
- 2026-07-17 (v2, plan cold-review coherence) — per user: **B1** the legacy Postgres tunables (`POSTGRES_BATCH_SIZE`/`FLUSH_INTERVAL`/`MAX_RETRIES`) are now wired from env, so **C-H2 is resolved for the Postgres path too** (not only carried as a lesson into the SQLite writer); **B2** the map basemap is **OpenStreetMap served via a same-origin Protomaps `.pmtiles` file** (no external tile server, no API key, offline-capable, OSM attribution required) — so the **CSP becomes self-only**; **B3** `/api/almanac` **proxies WeatherFlow** (the §11 "proxy or computed" ambiguity is resolved to proxy). Reflected in §4, §6, §7, §9, §11, §15, §16.

> **Superseded by `docs/designs/2026-08-13-weatherflow-token-and-shaping-design.md`.**
> Decision B3 resolved the almanac to a WeatherFlow `better_forecast`
> passthrough. That is reversed: the almanac is computed from the local store
> and from astronomy, and the WeatherFlow proxy is deleted, because in UDP
> mode the appliance holds no WeatherFlow credential (issue #62, won't-do).
>
> The "Sunrise/sunset/moon" row also overstates what the endpoint could have
> supplied. Sunrise and sunset are correct — they exist in
> `better_forecast.forecast.daily[]`. **No moon field is returned by
> `better_forecast` by default** in any of six recorded payloads spanning
> 2021–2026 across four independent lineages, and none is documented; that is
> not proof that no undocumented parameter could surface one, but it is why
> moon phase is computed locally.

---

## 1. Summary

Evolve `tempestwx-utilities` from a headless UDP/API exporter into a **single-binary weather
platform**: it keeps ingesting Tempest UDP broadcasts and historical REST data, but now also (1)
**serves its own embedded React UI**, (2) **overlays NOAA NEXRAD Level 3 radar** decoded
server-side by an **opt-in Python (Py-ART) sidecar** (reusing the DRAS approach) and served as
contoured GeoJSON, (3) **defaults to SQLite + Litestream** for storage while keeping PostgreSQL as a
supported option, (4) ships a **Grafana dashboard for weather enthusiasts**, (5) gets a **prioritized
UX overhaul** of the absorbed UI, and (6) **exports all telemetry (logs, metrics, traces) over a
unified OpenTelemetry (OTLP) backbone** — the app ships the signals to a configurable Collector and
leaves backend storage to the operator — superseding the bespoke Prometheus push/scrape path.

All six workstreams are built **additively on the existing `internal/sink` fan-out seam** and a new
HTTP surface, and each **fixes or supersedes the CRITICAL/HIGH review findings it touches**. A
**Workstream 0 (foundation)** lands the cross-cutting lifecycle fixes first, because every later
workstream depends on correct shutdown, panic isolation, and context handling.

The two source reviews this design is grounded in:
- Exporter: `docs/review/2026-07-17-code-review-summary.md` (1 CRITICAL, 17 HIGH).
- UI: `docs/review/2026-07-17-ui-code-review-summary.md` (0 CRITICAL, 8 HIGH).

---

## 2. Goals / Non-goals

### Goals
- One deployable artifact: Go binary with the UI embedded via `//go:embed`, plus a documented
  docker-compose for the full stack (app + OTel Collector + Prometheus + Grafana + object storage).
- Move the WeatherFlow token **server-side** (resolves UI **B-H1** and exporter **F-H1**).
- Real data in the UI (resolves UI **B-H2**): local observations from the exporter's own DB, forecast/
  almanac proxied through the backend.
- Radar overlay from NOAA NEXRAD Level 3, decoded server-side, cached, rendered on a map.
- SQLite + Litestream as the **default** store; PostgreSQL retained as an option (ratified).
- Unified OTLP telemetry (ratified): weather readings become OTel metric instruments; ingest, sink
  writes, and HTTP are traced and logged; a Collector fans out to Prometheus/trace-store/log-store.
- Fix/supersede the review's CRITICAL/HIGH lifecycle findings (graceful shutdown + drain, RadioStats
  panic, send-on-closed-channel panics, token-in-URL, dead error checks).

### Non-goals (explicitly out of scope; some are follow-ups in §14/§17)
- No new auth/user system for the UI (single-tenant appliance assumption).
- No alerting engine (surfaced as a UX suggestion only — §13).
- No multi-station clustering / HA of the SQLite node (Litestream is backup/PITR, not clustering).
- No OAuth flow for WeatherFlow (Personal Access Token via server-side proxy is sufficient).
- No rewrite of the correct-and-praised pieces (UDP field mapping, wet-bulb math, SQL column mapping,
  unit conversions, moon-phase geometry).

---

## 3. Ratified decisions

| # | Decision | Rationale |
|---|---|---|
| R1 | **Telemetry = unified OTLP; the app only *ships* signals.** In-process OTel SDK + OTLP exporter → a configurable Collector endpoint. The app emits metrics+logs+traces over OTLP and stops there; **which backends store them is the operator's choice**, not prescribed here. The example compose includes only what the Grafana deliverable needs (Collector + Prometheus + Grafana); trace/log stores are optional. | Single backbone; supersedes the panic-prone bespoke writers (D-H1/D-H2); avoids over-prescribing infra (KISS/YAGNI). |
| R2 | **DB = SQLite default + PostgreSQL optional.** New `internal/sqlite` writer; `internal/postgres` retained untouched. | Simplest self-contained default; Postgres stays for users who want it. Additive on the sink seam (No-Wall). |
| R3 | **Radar = server-side decode via a Python Py-ART sidecar** (reusing DRAS's approach) of NEXRAD Level 3 base reflectivity from `s3://unidata-nexrad-level3`, output as **contoured GeoJSON isobands**; the Go backend proxies + caches it. | Reuses DRAS's proven server-side Python radar decode (no risky hand-written Go NIDS decoder); keeps S3/decode off the browser and shared-cached. Radar is an **opt-in, separable sidecar**, so the core exporter stays a single static Go binary when radar is disabled — see §9, decisions O1/O3. |

---

## 4. Current-state grounding (key facts, with finding IDs)

From the **exporter review**:
- **C-1 (CRITICAL):** `report.go:263-264` indexes `RadioStats[1]/[2]` with no length guard → remote
  panic/DoS from any LAN host sending a short `hub_status` packet.
- **A-H1 (HIGH):** `main.go:29` traps only SIGINT, not SIGTERM → `docker stop`/k8s never trigger the
  graceful path → buffered DB rows lost.
- **A-H2 (HIGH):** gzip-only API export mode unreachable (writer-count invariant enforced in all modes).
- **A-H3 / D-H2 (HIGH):** `MetricsServer.Start()` swallows bind errors in a goroutine, always returns
  nil → main's error check is dead code; `/metrics` can be silently dead.
- **C-H1 (HIGH):** Postgres flush inserts derive their context from the (likely-cancelled) constructor
  ctx; the `ctx.Done()` worker branch flushes only the local slice, not the 1000-deep channel → data
  loss on shutdown.
- **C-H2 (HIGH):** `POSTGRES_BATCH_SIZE/FLUSH_INTERVAL/MAX_RETRIES` documented but hardcoded/inert. **(v2/B1: now wired from env in WS0 — see §6 — so C-H2 is fixed for the legacy Postgres path, not only avoided in the new SQLite writer.)**
- **C-H3 / D-H1 (HIGH):** send-on-closed-channel + double-close panics in **both** postgres and
  prometheus writers (no `sync.Once`, no `done` gate; `default:` does not protect a closed-channel send).
- **F-H1..H4 (HIGH):** API client — token in URL query string (leaks to logs and into `*url.Error`),
  no status-code check, no request timeout, `log.Fatalf` inside a library method.
- **G-H1..H3 (HIGH):** broken Docker action input, stale README env vars, no workflow `permissions:`.
- **B-HIGH:** `database.go:52` builds the Postgres DSN with raw `fmt.Sprintf` (no credential escaping);
  schema DDL effectively untested; no migration path.
- **Positives to preserve:** UDP field-index mapping (Review E), wet-bulb Bolton math, all four SQL
  column mappings, UUIDv7-once + `ON CONFLICT DO NOTHING` idempotency, static Chainguard non-root image.

From the **UI review** (`tempest-display`, React 19 + TS + Vite + 63-line Go static server):
- **B-H1 (HIGH):** commented live design embeds the WeatherFlow PAT in the frontend (`?token=`), no
  backend proxy exists.
- **B-H2 (HIGH):** the entire API client returns **stub data**; no real data path.
- **A-H1..H4 (HIGH):** no error boundary; loading/error/Retry CSS referenced but never defined; zero
  media queries (non-responsive); no `prefers-reduced-motion`.
- **E-H1 (HIGH):** Go static server has no HTTP timeouts (Slowloris).
- **F-H1 (HIGH):** UI container runs as root.
- **MEDIUM:** dead SettingsPanel Station-ID/Token inputs; no dialog semantics; hemisphere `°N/°W`
  bug; forecast weekday-across-year-boundary bug; theme-var leak; no focus-visible.
- **Positives to preserve:** all 10 unit conversions correct; moon-phase geometry correct; embed-FS
  path-traversal safety; strict TS; wind compass shortest-arc.

---

## 5. Target architecture

```
                         ┌──────────────────────────────────────────────────────────┐
                         │  tempestwx-utilities  (single Go binary)                   │
                         │                                                            │
   Tempest UDP :50222 ─► │  udp listener ─┐                                           │
   WeatherFlow REST ───► │  api client ───┤                                           │
                         │                ▼                                           │
                         │           internal/sink  (fan-out seam, +recover, +drain)  │
                         │            ├─► internal/sqlite   (default, WAL)  ──► file ──┼──► Litestream ─► S3/MinIO
                         │            ├─► internal/postgres (optional)               │
                         │            └─► internal/otel     (OTLP exporter) ─────────┼──► OTel Collector ─► Prometheus ─► Grafana
                         │                                                            │     (+ operator's trace/log backends,
                         │  HTTP server (chi/std mux):                                │      e.g. Tempo/Loki — not prescribed)
                         │   /                → embedded React UI (go:embed)          │
                         │   /api/observations/current|history → reads sqlite DB      │
                         │   /api/forecast|almanac|station     → proxies WF (token svr)│
                         │   /api/radar/{site}   → proxy+cache ─► radar sidecar ───────┼──► (Python/Py-ART) ─► s3://unidata-nexrad-level3
                         │   /healthz  /metrics (legacy, deprecated)                  │
                         └──────────────────────────────────────────────────────────┘
```

Data flow, UDP path (unchanged core, additively wired): packet → `tempestudp.ParseReport` → `Report`
→ `sink.SendReport(ctx, report)` → fan-out to {sqlite, postgres?, otel}. The UI reads the **sqlite
DB** for live/historical observations (no token needed); forecast/almanac come from the **server-side
WeatherFlow proxy**; radar comes from the **server-side NIDS decoder**.

---

## 6. Workstream 0 — Foundation: lifecycle & review-fix bedrock (do FIRST)

Every later workstream depends on correct shutdown, panic isolation, and context handling. These are
cross-cutting fixes owned at the **seam** (`main.go` + `internal/sink`) plus the UDP source, so all
writers — including the new sqlite and otel writers — inherit correctness (DRY / No-Wall: fix once at
the seam, not per-writer).

**Design:**
- `main.go`: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` (fixes **A-H1**).
- On signal: cancel the **ingest** context, but run cleanup under a **fresh** `cleanupCtx, cancel :=
  context.WithTimeout(context.Background(), 30*time.Second)` passed into `sink.Close(cleanupCtx)`
  (fixes **A-MEDIUM**; enables writers to drain after the ingest ctx is dead).
- `internal/sink`:
  - `Close(ctx)` takes the cleanup context (drop the stored `s.ctx` field — Review A NIT + C-LOW).
  - Fan-out goroutines get `defer func(){ if r:=recover(); r!=nil { log/metric } }()` (fixes
    **A-MEDIUM panic-recovery**; contains the blast radius of **C-1** and the wet-bulb panic).
  - `SendReport`/`SendMetrics` aggregate per-writer errors and return a meaningful error (all-writers-
    failed vs partial), removing the dead nil-return contract (**A-LOW**).
- `internal/tempestudp`:
  - Guard `if len(r.RadioStats) >= 3 { … }` before indexing (fixes **C-1**).
  - `wetBulb` returns `math.NaN()` (or best estimate) instead of `panic("failed to converge")`; caller
    skips emitting NaN (fixes **E-MEDIUM**). Modernize the solver loop to `for range 10000`.
- `internal/config`: build the Postgres DSN with `net/url` (percent-encoded credentials) instead of
  raw `fmt.Sprintf` (fixes **B-HIGH** escaping/injection).
- `internal/postgres` (**v2/B1**): read `POSTGRES_BATCH_SIZE`/`POSTGRES_FLUSH_INTERVAL`/`POSTGRES_MAX_RETRIES`
  from env into the writer (currently hardcoded `100`/`10s`/`3` and inert), via a pure `postgresTunables(getenv)`
  helper that falls back to those defaults. **Fixes C-H2 for the legacy Postgres path** (the SQLite writer
  already honors its own `SQLITE_*` tunables; this closes the same gap on the retained Postgres writer).
- `internal/tempestapi`: dedicated `&http.Client{Timeout: 30s}`; `Authorization: Bearer` header (drop
  token-from-URL); status-code check before decode; return an error instead of `log.Fatalf`; check
  `status.status_code`; bound the body with `io.LimitReader` (fixes **F-H1..H4, F-MEDIUM**).
- Config booleans: parse explicitly and fail/log on a non-empty unparseable value (fixes A-MEDIUM
  silent-typo-disables-a-sink).

**Note on the existing prometheus/postgres writers:** apply the **C-H3 / D-H1** `sync.Once`+`done`-gate
fixes here too, even though OTel will supersede the prometheus path and sqlite becomes default — both
writers remain in the tree during the transition and must not crash on shutdown.

**Resolves:** C-1, A-H1, A-H3, A-MEDIUM(×2), C-H1 (context half), **C-H2 (Postgres path, v2/B1)**, C-H3,
D-H1, D-H2, F-H1..H4, B-HIGH, E-MEDIUM.

---

## 7. Workstream 1 — Absorb the UI as a Go-embedded app

### Design
- **Vendor** the `tempest-display` React app (MIT, `Copyright (c) 2026 jacaudi` — same owner, license
  compatible; retain the `LICENSE` and add a provenance note in the vendored dir header) into this
  repo under `web/`.
- **Build pipeline:** multi-stage Docker — stage 1 `node:22-alpine` runs `npm ci --ignore-scripts` +
  `vite build` → `web/dist`; stage 2 `golang:1.25-alpine` compiles the Go binary that embeds
  `web/dist` via `//go:embed`; final stage `cgr.dev/chainguard/static` non-root (preserve the
  praised hardening). A local (non-Docker) build path uses a committed pre-built `web/dist` or a
  `task ui-build` target; the `go:embed` directive must have a non-empty target dir at `go build`
  time (document a `web/dist/.gitkeep` + build-order requirement).
- **Serve** the SPA from the same Go HTTP server that hosts the API, with SPA fallback and the
  embed-FS path-traversal safety the UI review praised. Apply the UI backend fixes: explicit
  `http.Server` timeouts (**E-H1**), graceful shutdown, security headers (nosniff/CSP/referrer), and
  fix the `immutable`-cache-on-index.html bug (only set immutable after a real asset stat; 404 on
  missing `/assets/*`).
- **Make the data layer real** (resolves UI **B-H2**). Replace the stub `tempestApi.ts` calls with
  fetches to the backend JSON endpoints in §11. The UI stops shipping fixtures.
- **Token server-side** (resolves UI **B-H1** + exporter **F-H1**): the exporter already holds
  `TOKEN`. Forecast/almanac/station endpoints proxy WeatherFlow REST with the token injected
  server-side (`Authorization: Bearer`), and the browser only ever calls tokenless relative URLs. The
  dead SettingsPanel Station-ID/Token inputs (UI D-MEDIUM) are **removed** (the token is an env var on
  the server, not a browser input) — or repurposed to select among the server's configured stations.

### Key decisions
- Single binary and single HTTP port for UI + API (KISS; the UI's separate 63-line static server is
  absorbed and deleted).
- **CSP is self-only (v2/B2):** the map basemap is an OpenStreetMap Protomaps `.pmtiles` file served
  **same-origin**, so the CSP names **no external hosts** — no map-tile host and no font CDN (self-host
  Inter, resolving UI A-MEDIUM offline/kiosk). The only non-origin allowances are `worker-src blob:` and
  `script-src 'wasm-unsafe-eval'` for MapLibre GL JS's Web Worker + WebAssembly renderer (they add no
  network origin). This makes the appliance fully offline/air-gap capable.

### Alternatives considered
- **Keep the UI a separate container** talking to the exporter over HTTP: rejected — defeats the
  single-artifact goal and re-opens the token-location question. (No-Wall: the seam is the JSON API,
  which we build anyway; a second container is speculative separation.)
- **Server-side render**: rejected (YAGNI; the SPA is fine for an appliance display).

### Risks
- `go:embed` requires `web/dist` to exist at build time → CI/build ordering must guarantee the Vite
  build runs first. Mitigation: document + a `.gitkeep` and a Taskfile target.
- Vendoring drifts from upstream `tempest-display`. Mitigation: record the exact source commit
  (`49892063`) and treat `web/` as owned-fork henceforth.

### Resolves
UI B-H1, B-H2, E-H1, F-H1 (root user → non-root), E-MEDIUM (graceful shutdown, cache, headers),
exporter F-H1 (token off the wire).

### External deps
Node/Vite toolchain in the build image; no new Go runtime deps for embedding (`embed` is stdlib).

---

## 8. Workstream 6 — Unified OpenTelemetry (OTLP) backbone

> Ordered before WS2–WS5 in this doc because WS4 (Grafana) and the observability of every other
> workstream depend on it. In the execution plan it runs after WS0 foundation.

### Design
- New `internal/otel` package providing:
  - **Setup**: `otel.Setup(ctx, cfg) (shutdown func(context.Context) error, err error)` that
    configures a `Resource` (service.name=`tempestwx`, service.version, host/station attributes), a
    `MeterProvider`, `TracerProvider`, and `LoggerProvider`, each with an **OTLP exporter**
    (`otlptracegrpc`/`otlpmetricgrpc`/`otlploggrpc` or the HTTP variants) pointed at
    `OTEL_EXPORTER_OTLP_ENDPOINT` (the Collector). Metrics/traces OTLP are **stable**; the logs
    signal is **still experimental** but the SDK + OTLP log exporter are usable today — isolate logs
    behind the same package so an API bump is a one-file change (No-Wall).
  - **A sink writer** `otel.Writer` implementing `sink.MetricsWriter`. On `WriteReport`, it records
    each weather field to a pre-registered **OTel instrument** (Gauges for temperature/humidity/
    pressure/wind/UV/irradiance/battery/rssi; Counters for reboots/bus_errors/lightning_strike_count
    and rain accumulation; the wet-bulb/dew-point/heat-index derived values as Gauges). Instruments
    are named to **preserve the existing `tempest_*` Prometheus names** after the Collector's
    OTLP→Prometheus translation (e.g. instrument `tempest.temperature.c` → `tempest_temperature_c`),
    so WS4 dashboards and any existing scrapers stay stable.
  - **Tracing**: spans around UDP ingest (`udp.receive` → `report.parse` → `sink.write` children per
    writer), the API export loop, and each HTTP request (via `otelhttp` middleware).
  - **Structured logs**: bridge the app's logging to the OTel `LoggerProvider` (or emit via `slog`
    with an OTel handler), so logs carry trace/span IDs for correlation.
- **Metric-hygiene fixes folded in** (from Review D): rename the reserved `instance` label → `serial`
  (**D-MEDIUM**); `tempest_wind_ms` → `tempest_wind_meters_per_second` (**D-MEDIUM**); uptime becomes
  a **Gauge** `tempest_uptime_seconds` (drop `_total`, **D-MEDIUM/E-LOW**); drop the dead `RainTotal`
  or wire it as a proper `tempest_rainfall_mm_total` counter (**D-MEDIUM**).
- **Scope boundary (per R1/O5):** the *app's* responsibility ends at exporting OTLP to
  `OTEL_EXPORTER_OTLP_ENDPOINT`. It ships metrics+logs+traces and does **not** prescribe or bundle
  storage backends — that is the operator's choice. The **example** compose includes a Collector that
  routes **metrics → Prometheus** (required, because the WS4 Grafana dashboard is a deliverable and
  needs a metrics source) and sends traces/logs to a debug exporter by default, with
  **optional/commented** Tempo+Loki blocks for operators who want the full Grafana-native trace/log
  experience.

### Migration (careful, not big-bang)
1. WS0 fixes the bespoke prometheus writers so they don't crash during transition.
2. Ship `internal/otel` as an **additive** sink writer, enabled by `ENABLE_OTEL=true` +
   `OTEL_EXPORTER_OTLP_ENDPOINT`.
3. Once OTel is proven against Prometheus (metric names match, dashboards work), **deprecate** the
   pushgateway and scrape writers (log a deprecation warning; keep them for one release).
4. A later release removes the bespoke prometheus path. The `/metrics` legacy endpoint remains until
   then for users mid-migration. (Open decision O4: deprecate-then-remove vs remove-now.)

### Alternatives considered
- **Keep Prometheus-native, skip OTel**: rejected (ratified R1; also leaves logs/traces unaddressed).
- **OTel Collector as the only metrics scraper (Prometheus receiver scraping the app)**: rejected in
  favor of in-process SDK push — the app's data is event-driven (sporadic broadcasts), which suits
  push/OTLP better than scrape (mirrors why the project chose push originally).

### Risks
- Logs API instability (experimental) — isolated in `internal/otel`; fall back to `slog`-only if a
  breaking change lands.
- Metric-name translation (OTLP→Prometheus dotted→underscore, unit suffixes) can surprise; the plan
  must include an assertion test that the Collector emits the exact `tempest_*` names WS4 queries.

### Resolves / supersedes
D-H1, D-H2 (bespoke writer panics/bind-swallow superseded), D-MEDIUM metric-hygiene set, A-H3.

### External deps
`go.opentelemetry.io/otel`, `.../sdk`, `.../exporters/otlp/*`, `.../contrib/instrumentation/net/http/otelhttp`; an OTel Collector image + Prometheus + Grafana in the example compose (Tempo/Loki optional/commented).

---

## 9. Workstream 2 — NEXRAD Level 3 radar overlay

### Verified externals (researched live — do not trust memory)
- **Bucket:** `s3://unidata-nexrad-level3/` (region `us-east-1`, **anonymous** access, real-time).
  (Registry of Open Data on AWS; NSF Unidata.) The older `noaa-nexrad-level2/` archive bucket was
  deprecated 2025-09-01 — not relevant here (that's Level II).
- **Object naming:** flat keys `SSS_PPP_YYYY_MM_DD_HH_MM_SS` where `SSS` = 3-char site (no leading K,
  e.g. `TLX` for `KTLX`), `PPP` = product code.
- **Product (base reflectivity):** `N0B` = super-resolution base reflectivity (0.5° tilt, 256 levels,
  0.54nm×1° res) — the **current** product; `N0Q` = legacy base reflectivity, same geometry. IEM now
  sources N0Q from N0B. **Recommendation:** default to **N0B**, fall back to **N0Q** if a site lacks
  a recent N0B object. (Open decision O2.)

### Prior art (DRAS — the user's own repo, studied)
DRAS is a Go orchestrator that decodes radar **server-side** and ships a rendered image/JSON
downstream; it never puts S3 or decode logic in the browser/notifier. Its **Advanced** mode uses a
**Python Py-ART sidecar** to decode **Level II** volumes assembled from
`s3://unidata-nexrad-level2-chunks/` (server-side, anonymous S3, `boto3`), caches per
`(station_id, latest_chunk_time)`, and hard-fails cleanly (notification with no image) on decode
error. Its **Basic** mode just downloads the NWS pre-rendered ridge GIF. **Key architectural lessons
this design adopts:** (a) decode server-side; (b) cache keyed on the volume/scan timestamp, not the
slot; (c) anonymous S3, egress to `*.s3.amazonaws.com:443`; (d) a clean error envelope.

### Recommendation: **server-side decode via a Python Py-ART sidecar (reuse DRAS), output contoured GeoJSON**
Per the user's direction (O1/O3), radar is decoded by a **Python sidecar service** rather than a
hand-written Go NIDS decoder. Py-ART reads Level 3 NIDS directly (`pyart.io.read_nexrad_level3`),
exactly mirroring how DRAS already decodes radar server-side in Python — so this **reuses proven prior
art** and removes the design's single highest-risk item (no mature pure-Go L3 decoder exists). The
sidecar fetches the newest NIDS object from S3 (anonymous), decodes to a gridded reflectivity field,
and emits **contoured GeoJSON isobands** (dBZ banded polygons via `geojsoncontour`/matplotlib
`contourf`). The Go backend calls the sidecar over HTTP, **caches** the result per `(site, product,
scanTime)`, and serves it at `/api/radar/{site}`.

**Why a Python sidecar (O1):** reuses DRAS; no risky bespoke Go decoder; Py-ART is the de-facto
radar-decode library. Cost: it is a separate container, so the *pure single Go binary* no longer holds
**when radar is enabled** — but radar is **opt-in** (`ENABLE_RADAR`), so users who don't want it keep
the single static binary, and the deployment is already a compose stack, so one more sidecar is
consistent. The core exporter never imports Python; it only HTTP-calls the sidecar.

**Why contoured GeoJSON over a PNG overlay (O3):** full-resolution radar is a raster of >1M gate
values, which is why a raw-GeoJSON-per-gate payload was rejected — but **contoured isobands** collapse
that to a few thousand banded polygons: light, **vector** (crisp at any zoom), **restylable**
client-side, and interactive (click a band → dBZ range). This is the conventional modern web-radar
representation and is cheap for Py-ART to produce. A georeferenced PNG overlay remains the simpler
fallback if the GeoJSON proves too heavy on weak clients.

**Why server-side over client-side decode:**
- Keeps S3 access and the decode CPU **off the browser** (no AWS credentials or SDK in client JS;
  weak kiosk/Pi clients stay light).
- Enables a **shared server cache** (all UI clients reuse one decoded scan), impossible client-side.
- Matches DRAS's proven pattern and the token-proxy pattern from WS1 (the backend is already the
  trusted egress point).

### Design
- New `internal/radar` package:
  - **Site selection:** map the station's lat/lon (from WeatherFlow station meta) to the nearest
    WSR-88D site. Ship a small static table of site codes + coordinates (the ~160 CONUS sites),
    reusing DRAS's `ValidateStationID`/site conventions. `RADAR_SITE` env can override.
  - **Fetch:** anonymous S3 discovery of the newest scan (`ListObjectsV2` prefix `SSS_PPP_`, pick the
    largest `YYYY_MM_DD_HH_MM_SS` suffix, `GetObject`). **Owned by the Python sidecar** (`boto3`
    anonymous, as DRAS does) to keep S3+decode in one place; the Go backend triggers a refresh on the
    scan cadence (~4–6 min, VCP-dependent) and caches the result. (Alternative: Go does S3 discovery
    and passes bytes to the sidecar — the plan picks the simpler split.)
  - **Decode (in the Python sidecar):** the sidecar exposes `GET /radar?site=&product=`; it
    `read_nexrad_level3`s the fetched NIDS bytes via **Py-ART**, grids the base-reflectivity field,
    contours it into dBZ isobands (e.g. 5-dBZ steps over the NWS reflectivity scale) with
    `geojsoncontour`, and returns **GeoJSON** (+ metadata: scan time, site, lat/lon extent). The Go
    `internal/radar` package owns caching + serving and never decodes NIDS itself. References: Py-ART
    `read_nexrad_level3`; DRAS's Py-ART sidecar; `geojsoncontour`.
  - **Cache:** in-memory LRU keyed on `(site, product, scanTime)` (DRAS pattern), TTL ~ one scan
    cycle; bound size.
  - **Serve:** `GET /api/radar/{site}?product=N0B` → the PNG/GeoJSON + metadata (scan time, site,
    lat/lon extent) with a clean error envelope (`no_recent_scan`, `decode_failed`, `internal`).
- **UI render:** add a map card (MapLibre GL JS — open-source; no API key). **Basemap = OpenStreetMap
  via a same-origin Protomaps `.pmtiles` file (v2/B2):** the backend serves (or the image embeds) one
  `.pmtiles` OSM vector-tile file and MapLibre reads it via the `pmtiles://` protocol with byte-range
  requests to `'self'` — **no external tile server, no API key, offline-capable, and OSM attribution
  (`© OpenStreetMap contributors`) is rendered on the map (required).** The public
  `tile.openstreetmap.org` raster server is an online-only, light-use **fallback only**, not the default.
  The radar overlay is a **GeoJSON fill layer** (dBZ isobands, styled by band via a data-driven color
  ramp; restylable client-side), centered on the station, auto-refreshing on the scan cadence.
  Respects `prefers-reduced-motion` (no auto-pan). (PNG image-layer is the O3 fallback.)

### Alternatives considered
- **Pure-Go in-process NIDS decoder:** rejected per O1 — no mature Go L3 decoder exists (`go-nexrad`
  is Level II only), making it the highest-risk/effort item; the Python sidecar reuses DRAS's proven
  code instead. Its only advantage (preserving the single binary) is largely retained because radar is
  opt-in and separable.
- **Full-resolution GeoJSON (polygon per gate):** rejected — >1M features, tens of MB, kills the
  browser. **Contoured isobands** (the chosen output) give the GeoJSON benefits without the payload.
- **Georeferenced PNG overlay:** viable and simpler; kept as the O3 fallback if contoured GeoJSON is
  too heavy on weak clients.
- **Client-side JS decode / pre-rendered NWS ridge GIF:** rejected (bundle bloat + no shared cache;
  GIF is low quality) — the NWS ridge GIF remains a degraded last-resort for sites with no decodable
  NIDS.

### Risks
- **Sidecar image size / operational surface:** Py-ART + SciPy is a large image (~hundreds of MB) and
  a second moving part. Mitigation: radar is **opt-in** (`ENABLE_RADAR`), separable, and only deployed
  when wanted; the core exporter stays a single static binary otherwise.
- **Go↔sidecar contract:** define a stable HTTP contract + a clean error envelope (`no_recent_scan`,
  `decode_failed`, `internal`) so a sidecar failure degrades gracefully (mirror DRAS's soft-fail).
- **Contouring fidelity:** banded isobands lose smooth gradients — acceptable (conventional radar
  look) and tunable via band count; PNG fallback if needed.
- S3 egress/network in restricted deployments; radar is opt-in (`ENABLE_RADAR=true`).

### Resolves
No direct review finding (new feature), but corrects the UI review's **phantom `VITE_ENABLE_RADAR`
docs (F-MEDIUM)** by making radar a real, server-configured feature.

### External deps
**Radar sidecar (Python):** Py-ART, `boto3` (anonymous S3), `geojsoncontour`/matplotlib, a small HTTP
framework (Flask/FastAPI) — packaged as its own container (reuse/adapt DRAS's sidecar). **Go side:**
just an HTTP client + cache (stdlib); no NIDS/S3 Go deps required when the sidecar owns S3. **UI:**
MapLibre GL JS + the `pmtiles` protocol plugin. **Basemap asset (v2/B2):** one OpenStreetMap `.pmtiles`
vector-tile file — produced/obtained via the Protomaps tooling (a documented `pmtiles extract` of a
regional extract, or a download from `build.protomaps.com`) and served **same-origin**; no external tile
service or API key. OSM attribution is required.

---

## 10. Workstream 3 — SQLite + Litestream (default), Postgres retained

### Design
- New `internal/sqlite` package implementing `sink.MetricsWriter`, mirroring the postgres writer's
  **praised** parts (UUIDv7 PKs generated once per row, `INSERT … ON CONFLICT DO NOTHING` on the
  natural unique key, correct column mappings) while **not repeating** its lifecycle bugs:
  - **Single serialized writer goroutine** (SQLite is single-writer; this is simpler than postgres's
    4-goroutine design and makes the drain trivially correct).
  - **Drain on `Close(ctx)`** using the fresh cleanup context from WS0, draining the **channel
    buffer** (not just a local slice) — fixes the class of **C-H1**.
  - **`sync.Once` + `done`-gate on sends**, idempotent `Close` — fixes the class of **C-H3**.
  - **Honor tunables** `SQLITE_BATCH_SIZE`, `SQLITE_FLUSH_INTERVAL`, `SQLITE_BUSY_TIMEOUT` (don't
    repeat **C-H2** inert-knobs).
  - **Events are not dropped under backpressure** (don't repeat **C-MEDIUM**): block-with-bounded-
    timeout for discrete lightning/rain-start rows.
- **Driver: `modernc.org/sqlite` (pure Go, CGO-free).** **Load-bearing:** this preserves the
  `CGO_ENABLED=0` static Chainguard image the exporter review praised. Do **not** use
  `mattn/go-sqlite3` (CGO) — it would break the static build. (Throughput is irrelevant at ~1 write/
  min; the static-build benefit dominates.)
- **PRAGMAs (must be exact):** `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`,
  `foreign_keys=ON`. **Let Litestream own checkpointing** — do not set aggressive
  `wal_autocheckpoint`; Litestream holds a read transaction and drives checkpoints itself.
- **Schema:** typed tables mirroring the postgres schema — `tempest_observations`,
  `tempest_rapid_wind`, `tempest_hub_status`, `tempest_events` — with UUIDv7 text PKs, `TIMESTAMPTZ`→
  ISO-8601 text or unix-epoch integers, and the same UNIQUE constraints backing dedup (§12 has DDL).
  Fix the postgres review's LOW: integer counts as `INTEGER`, not float.
- **Litestream** runs as a sidecar in compose, streaming the WAL to S3/MinIO. It composes with
  `modernc.org/sqlite` because Litestream operates on the on-disk SQLite file/WAL independent of the
  Go driver. Config: replica → object storage; document credentials via env (§15).
- **Migrations:** adopt a lightweight versioned-migration approach (embedded `.sql` + a `schema_version`
  table) so schema evolution is handled — addresses the postgres review's **B-MEDIUM** (no migration
  path) for the new default store.

### Key decisions
- SQLite **default**, Postgres **opt-in** (R2). `main.go` selects: if `ENABLE_POSTGRES` → postgres;
  else default to sqlite at `SQLITE_PATH` (default `/data/tempest.db`). Both can run concurrently
  (fan-out) if both enabled.
- Additive on the sink seam: new package + main wiring; `internal/postgres` untouched (No-Wall).

### Alternatives considered
- **`mattn/go-sqlite3`:** rejected (CGO breaks the static image).
- **LiteFS / rqlite / dqlite:** rejected (YAGNI — clustering/HA is a non-goal; Litestream's
  backup/PITR is the actual requirement).

### Risks
- Misconfigured PRAGMAs silently lose Litestream data — the design pins exact values and the plan must
  include a restore-from-replica test.
- Single-writer contention if the executor copies postgres's multi-goroutine design — the design
  mandates a single writer goroutine.

### Resolves
Applies the **lessons of** C-H1, C-H2, C-H3, C-MEDIUM, B-MEDIUM to the new writer so they are not
reintroduced.

### External deps
`modernc.org/sqlite`, `github.com/google/uuid` (already present), Litestream (sidecar binary/image).

> **Note (WS3 execution, 2026-07-18):** adding `modernc.org/sqlite@v1.34.4` + `go mod tidy` raised the module's `go` directive from `1.24.0` to `1.25.0` (via newer transitive `modernc.org/libc`/`golang.org/x/sys`). This is **not** a hard floor of the pinned sqlite version, but it is build-safe on the pinned toolchain (`go1.26.1`) and preserves the `CGO_ENABLED=0` static build, so the floor bump was **accepted** rather than pinned back.

---

## 11. Backend JSON API (bridges UI ↔ data)

New endpoints on the single HTTP server (all tokenless from the browser; `otelhttp`-traced):

| Endpoint | Source | Notes |
|---|---|---|
| `GET /api/observations/current` | sqlite DB (latest row) | Real-time local data, **no token**. Resolves UI B-H2. |
| `GET /api/observations/history?field=&from=&to=` | sqlite DB | Powers UI historical charts (§13) and Grafana-lite in-UI. |
| `GET /api/station` | WeatherFlow REST proxy | Token injected server-side (`Bearer`). |
| `GET /api/forecast` | WeatherFlow REST proxy | Better-Forecast; token server-side. |
| `GET /api/almanac` | WeatherFlow REST proxy (**v2/B3: proxy, not computed**) | Sunrise/sunset/moon; token injected server-side (`Bearer`). |
| `GET /api/radar/{site}` | `internal/radar` → Python sidecar | Proxies+caches the sidecar's **contoured-GeoJSON** reflectivity (Py-ART). Opt-in (`ENABLE_RADAR`). |
| `GET /healthz` | — | Liveness (resolves UI F-LOW/G-LOW health probe gap). |
| `GET /metrics` | legacy prometheus scrape | **Deprecated**; removed after OTel migration (O4). |

> **Superseded by `docs/designs/2026-08-13-weatherflow-token-and-shaping-design.md`.**
> Decision B3 resolved the almanac to a WeatherFlow `better_forecast`
> passthrough. That is reversed: the almanac is computed from the local store
> and from astronomy, and the WeatherFlow proxy is deleted, because in UDP
> mode the appliance holds no WeatherFlow credential (issue #62, won't-do).
>
> The "Sunrise/sunset/moon" row also overstates what the endpoint could have
> supplied. Sunrise and sunset are correct — they exist in
> `better_forecast.forecast.daily[]`. **No moon field is returned by
> `better_forecast` by default** in any of six recorded payloads spanning
> 2021–2026 across four independent lineages, and none is documented; that is
> not proof that no undocumented parameter could surface one, but it is why
> moon phase is computed locally.

Shapes should match the UI's existing `types/weather.ts` (SI units — °C, m/s, mb, mm) so the vendored
components need minimal change; the UI's unit hook (verified correct) handles display conversion.

---

## 12. Unified data model

### SQLite schema (default store) — DDL sketch

```sql
-- All timestamps stored as INTEGER unix-epoch seconds (UTC). UUIDv7 text PKs generated in Go.
CREATE TABLE IF NOT EXISTS tempest_observations (
  id TEXT PRIMARY KEY,                 -- UUIDv7
  serial_number TEXT NOT NULL,
  timestamp INTEGER NOT NULL,          -- ob[0] epoch
  wind_lull REAL, wind_avg REAL, wind_gust REAL, wind_direction REAL,
  wind_sample_interval INTEGER,
  pressure REAL, temp_air REAL, temp_wetbulb REAL, humidity REAL,
  illuminance REAL, uv_index REAL, irradiance REAL, rain_rate REAL,
  precip_type INTEGER,
  lightning_distance REAL, lightning_strike_count INTEGER,   -- INTEGER not float (fix B-LOW)
  battery REAL, report_interval INTEGER,
  UNIQUE(serial_number, timestamp)     -- backs ON CONFLICT DO NOTHING
);
CREATE TABLE IF NOT EXISTS tempest_rapid_wind (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  wind_speed REAL, wind_direction REAL, UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_hub_status (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  uptime INTEGER, rssi REAL, reboot_count INTEGER, bus_errors INTEGER,
  UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_events (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  event_type TEXT NOT NULL, distance_km REAL, energy REAL,
  UNIQUE(serial_number, timestamp, event_type)
);
CREATE INDEX IF NOT EXISTS idx_obs_serial_time ON tempest_observations(serial_number, timestamp);
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
```

The Postgres schema is unchanged; the two schemas are parallel representations of the same knowledge
(the DRY answer: they must change together, so migrations for both live in one place per store).

### OTel semantic conventions
- Resource: `service.name=tempestwx`, `service.version`, `host.name`, and a `tempest.serial` resource
  attribute (replaces the reserved `instance` label — fixes D-MEDIUM).
- Instruments → Prometheus names (via Collector translation), preserving existing `tempest_*`:
  `tempest.temperature.c`→`tempest_temperature_c` (Gauge), `tempest.wind.meters_per_second`
  →`tempest_wind_meters_per_second` (Gauge, fixes `_ms`), `tempest.uptime.seconds`
  →`tempest_uptime_seconds` (Gauge, fixes `_total`), `tempest.reboots`→`tempest_reboots_total`
  (Counter), etc. Derived: `tempest.dewpoint.c`, `tempest.heat_index.c`, `tempest.wetbulb.c`.
- Spans: `udp.receive`, `report.parse`, `sink.write` (child span per writer), `http.server.request`.

---

## 13. Grafana dashboard spec (WS4) — "Weather Nerd" dashboard

Data source = Prometheus (fed by the OTel Collector). PromQL uses the preserved `tempest_*` names and
the `serial` label. Layout top-to-bottom; panels grouped in rows.

**Row 1 — Current conditions (stat panels):**
- Temperature: `tempest_temperature_c{kind="air", serial="$serial"}`
- Dew point (derived): `tempest_dewpoint_c{serial="$serial"}`
- Heat index / Wet-bulb: `tempest_heat_index_c`, `tempest_wetbulb_c`
- Humidity: `tempest_humidity_percent`
- Pressure: `tempest_pressure_mb`
- Wind (avg/gust): `tempest_wind_meters_per_second{kind="avg"}`, `{kind="gust"}`

**Row 2 — Trends (time series, 24h):**
- Temperature vs dew point vs wet-bulb overlaid.
- Pressure with **tendency**: `deriv(tempest_pressure_mb{serial="$serial"}[3h])` (mb/3h — the
  meteorological pressure-tendency window).
- Humidity trend.

**Row 3 — Wind:**
- **Wind rose** (Grafana wind-rose/polar panel or a heatmap of
  `tempest_wind_direction_degrees` bucketed vs `tempest_wind_meters_per_second`).
- Gust factor: `tempest_wind_meters_per_second{kind="gust"} / tempest_wind_meters_per_second{kind="avg"}`.

**Row 4 — Rain:**
- Rate: `tempest_rain_rate_mm_min`.
- Accumulation (last 24h): `sum_over_time(tempest_rain_rate_mm_min{serial="$serial"}[24h]) * <interval>`
  or `increase(tempest_rainfall_mm_total[24h])` if RainTotal is wired as a counter (preferred; see
  D-MEDIUM fix).

**Row 5 — Lightning:**
- Strike rate: `increase(tempest_lightning_strike_count[1h])` (needs the count as a counter, or
  `sum_over_time` of the per-minute gauge).
- Nearest distance: `min_over_time(tempest_lightning_distance_km{serial="$serial"}[1h])`.

**Row 6 — Solar / UV:**
- UV index: `tempest_uv_index`. Solar irradiance: `tempest_irradiance_w_m2`. Illuminance:
  `tempest_illuminance_lux`.

**Row 7 — Station health:**
- Battery: `tempest_battery_volts`. RSSI: `tempest_rssi_dbm`. Uptime:
  `tempest_uptime_seconds`. Reboots/bus errors: `increase(tempest_reboots_total[24h])`,
  `increase(tempest_bus_errors_total[24h])`.

**Row 8 — Records / almanac (stat, `max_over_time`/`min_over_time` over `$__range`):**
- Today's high/low temp: `max_over_time(tempest_temperature_c{kind="air",serial="$serial"}[$__range])`
  / `min_over_time(...)`. Peak gust, max UV, total rain, closest lightning — same pattern.

**Template variables:** `$serial` (label_values(tempest_temperature_c, serial)), `$__range`.
Delivered as a provisioned dashboard JSON + Grafana datasource/provisioning config in the compose
stack.

---

## 14. UX improvement scan (WS5) — prioritized, weather-nerd-focused

Grounded in the UI review + fresh product ideas. Priority: **P0 = correctness/broken-first-screen,
P1 = high-value, P2 = polish, P3 = follow-up (not built now)**.

**P0 — make it not break (from UI review HIGH):**
1. Add a React **error boundary** around the dashboard/cards (A-H1) — one bad card must not blank a
   24/7 display.
2. Define the missing `.loading-screen/.loading-spinner/.error-screen/.glass-btn` CSS (A-H2) — this is
   literally the first screen users see.
3. Add responsive breakpoints (A-H3) — collapse the 3-col grid to 2/1 on tablets/phones (the devices
   these displays run on).
4. Add `prefers-reduced-motion` + `:focus-visible` (A-H4/A-MEDIUM) — vestibular safety + keyboard a11y.
5. Route UV through a `formatX` helper so a missing field renders `NaN` not a hard crash (C-MEDIUM).

**P1 — high value for enthusiasts:**
6. **Real data** (B-H2 → §11 endpoints) with a clear "live vs stale" indicator (retain prior data on
   fetch failure, show last-updated age).
7. **Historical charts** in-UI from `/api/observations/history` (sparklines on each card + a
   drill-down trend view) — the biggest enthusiast upgrade; the exporter now has the DB to back it.
8. **Radar map card** (WS2) with the reflectivity overlay centered on the station.
9. Fix civil-calendar/i18n bugs users see: hemisphere `°N/°S/°E/°W` (C-MEDIUM), forecast weekday
   across year boundary (D-MEDIUM), station-local timezone for sunrise/sunset/updated-time.
10. Remove or repurpose the **dead SettingsPanel Station-ID/Token inputs** (D-MEDIUM) — token is now
    server-side; expose real settings (units, theme, station selector, kiosk toggle).
11. **Kiosk mode**: fullscreen, no-cursor, auto-refresh, wake-lock — these run on wall displays.
12. Self-host fonts (A-MEDIUM) so the appliance works offline/air-gapped.

**P2 — polish:**
13. `React.memo` the leaf cards (re-render on every 3s tick — A-MEDIUM); `useMemo` the RainCard
    raindrops (C-LOW reshuffle).
14. Settings modal dialog semantics: `role="dialog"`, `aria-modal`, Escape, focus trap/restore
    (D-MEDIUM).
15. Theme-var leak fix (`desert-sunset` missing `--text-shadow`; `applyTheme` should clear prior
    vars) (D-MEDIUM); `aria-hidden` on decorative SVGs; `°N/°W` etc.
16. `100dvh` over `100vh`; drop `background-attachment: fixed`; enumerate `transition` props.
17. Add Vitest + cover the pure math (already-correct conversions, moon phase, day-name); add CI
    (lint/typecheck/build) — the UI has none today.

**P3 — follow-ups (surfaced, NOT built now — YAGNI):**
18. In-UI **alerting** (threshold notifications for lightning proximity, freeze, high wind) — a real
    feature but its own project; note as a follow-up.
19. Multi-station switcher UI (backend already lists stations).
20. PWA/offline caching for the kiosk.
21. Unit/theme user profiles beyond localStorage.

---

## 15. Security

- **WeatherFlow token proxy** (resolves UI B-H1, exporter F-H1): token lives only in the server
  process (`TOKEN` env), sent to WeatherFlow as `Authorization: Bearer`; the browser calls tokenless
  relative URLs. Scrub any `*url.Error` before logging so the token never reaches logs. Never place
  the token in `localStorage` or the bundle.
- **Radar fetch:** anonymous S3 (no credentials); egress restricted to `*.s3.amazonaws.com:443`;
  `ENABLE_RADAR` opt-in. Validate/allowlist the `{site}` path param against the static site table (no
  arbitrary S3 key construction from user input — SSRF guard).
- **Litestream creds:** object-storage credentials via env (`LITESTREAM_ACCESS_KEY_ID`,
  `LITESTREAM_SECRET_ACCESS_KEY`, endpoint) — never committed; documented in `.env.example` with the
  file gitignored.
- **HTTP hardening** (resolves UI E-H1/E-MEDIUM): explicit `http.Server` timeouts
  (`ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`), security headers (nosniff,
  referrer-policy, and a **self-only CSP** — v2/B2: `default-src 'self'; img-src 'self' data: blob:;
  worker-src 'self' blob:; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline';
  connect-src 'self'; object-src 'none'; base-uri 'self'` — no external tile/font host because the OSM
  `.pmtiles` basemap is same-origin; the `blob:`/`wasm-unsafe-eval` allowances are only for MapLibre's
  worker/WASM), non-root container (`USER 65532`), graceful shutdown.
- **CI/supply-chain** (resolves G-H1..H3): fix the Docker action input mismatch, add least-privilege
  `permissions:` blocks, digest-pin base images, commit a `.goreleaser.yaml`.

---

## 15a. Full-stack docker-compose (service inventory)

The single-binary app runs standalone; the full observability + backup stack is a documented compose
file (the plan phase writes the exact YAML). Services:

| Service | Image | Role | Key wiring |
|---|---|---|---|
| `tempestwx` | this repo's image | app (UI + API + ingest) | `network_mode: host` (UDP broadcast); `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317`; `SQLITE_PATH=/data/tempest.db`; `ENABLE_RADAR`; `TOKEN`; volume `/data`. |
| `litestream` | `litestream/litestream` | continuous WAL backup | mounts the same `/data`; replicates `tempest.db` → `minio`; `LITESTREAM_*` creds. |
| `otel-collector` | `otel/opentelemetry-collector-contrib` | fan-out | OTLP receiver `:4317`; **metrics → Prometheus** (required for the dashboard); traces/logs → debug exporter by default, with optional/commented Tempo/Loki. |
| `prometheus` | `prom/prometheus` | metrics store | receives from Collector; datasource for Grafana. |
| `grafana` | `grafana/grafana` | dashboards | provisioned Prometheus datasource + the WS4 dashboard JSON. |
| `radar-sidecar` *(opt-in)* | Python/Py-ART — this repo's `radar/` image | radar decode | enabled with `ENABLE_RADAR`; fetches NIDS from S3 (anonymous), returns contoured GeoJSON; called only by `tempestwx`. |
| `tempo` / `loki` *(optional)* | `grafana/tempo` / `grafana/loki` | traces / logs | commented-out example backends; the app already exports OTLP, so operators can point the Collector at any store. (O5) |
| `minio` | `minio/minio` | object storage | Litestream replica target (S3-compatible); prod can point at real S3. |

`ENABLE_POSTGRES` deployments swap `litestream`+`minio` for a `postgres` service.

## 16. Migration & backward-compatibility

- **Env vars:** additive — `ENABLE_OTEL`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `SQLITE_PATH`,
  `SQLITE_*` tunables, `ENABLE_RADAR`, `RADAR_SITE`, `LITESTREAM_*`, UI served automatically.
  `ENABLE_PROMETHEUS_*` retained but **deprecated** once OTel is proven. `ENABLE_POSTGRES` retained.
  **`POSTGRES_BATCH_SIZE`/`POSTGRES_FLUSH_INTERVAL`/`POSTGRES_MAX_RETRIES` are now honored (v2/B1)** —
  previously documented but inert; behavior for unset values is unchanged (defaults `100`/`10s`/`3`).
- **Default store change:** existing Postgres users set `ENABLE_POSTGRES=true` and are unaffected. New
  users get SQLite by default. Both can run together.
- **Metric names preserved** so existing Prometheus/Grafana consumers keep working through the OTel
  transition (the `serial` label rename from `instance` is the one intentional break — documented).
- **README/docs:** rewrite to match reality (resolves G-H2 stale env vars; UI F-MEDIUM phantom radar
  args). Update the operational-mode matrix for the new default.
- **Mode fix:** enforce the "at least one writer" invariant **only in UDP mode**; API-export mode
  requires a DB writer *or* `KEEP_EXPORT_FILES` (resolves A-H2).

---

## 17. Open decisions for the user

- **O1 — Radar decoder — RESOLVED (user, 2026-07-17): Python Py-ART sidecar.** Reuses DRAS's proven
  server-side decode; removes the risky bespoke-Go-decoder item. Radar is opt-in, so the core binary
  stays single/static when radar is off. See §9.
- **O2 — Radar product default: `N0B` (super-res) vs `N0Q` (legacy).** Recommendation (open): N0B with
  N0Q fallback.
- **O3 — Radar output format — RESOLVED (user, 2026-07-17): contoured GeoJSON isobands** (vector,
  interactive, restylable; far lighter than full-res GeoJSON). Georeferenced PNG kept as fallback.
  See §9.
- **O4 — Bespoke Prometheus path: deprecate-then-remove (one release) vs remove-now.** Recommendation
  (open): deprecate-then-remove to avoid a metrics gap.
- **O5 — Telemetry backends — RESOLVED (user, 2026-07-17): the app only ships OTLP; backends are the
  operator's choice.** Not prescribed. The example compose runs only Collector + Prometheus + Grafana
  (needed for the WS4 dashboard); Tempo/Loki are optional/commented examples. See §8, R1.
- **O6 — SQLite driver.** Recommendation (strong, open): `modernc.org/sqlite` (CGO-free, preserves the
  static image). Confirm the CGO-free constraint holds.

---

## 18. Testing strategy

- **Foundation (WS0):** table-driven test for the `RadioStats` short-array case (would have caught
  C-1); wet-bulb NaN/edge tests; sink panic-recovery test (a panicking writer is contained); SIGTERM
  graceful-shutdown test asserting buffered rows are drained; DSN round-trip through `pgxpool.
  ParseConfig` with special-char credentials (catches B-HIGH); API client status-code/timeout/header
  tests.
- **SQLite writer:** unit tests for routing + optional-field population + boundary lengths; a real DB
  test (temp file) asserting column values, `ON CONFLICT` idempotency on retry, and drain-on-`Close`;
  a **Litestream restore** test (write → replicate to MinIO → restore → assert equality).
- **OTel:** assert the Collector emits the exact `tempest_*` Prometheus names WS4 queries (name-
  translation guard); span/trace-context propagation test.
- **Radar:** sidecar — decode a captured NIDS fixture → expected GeoJSON isobands (Py-ART/pytest);
  Go side — site-selection nearest-site test, cache-key correctness, sidecar error-envelope handling,
  and `{site}` allowlist (SSRF guard).
- **UI:** Vitest for the (correct) pure math + new formatX helpers; error-boundary render test;
  responsive smoke; CI running lint/typecheck/build/`docker build`.
- **Verification:** `go build ./...`, `go test -race ./...` for touched packages, `go vet`, `gofmt`
  (fix `metrics.go`), the linter; report real output.

---

## 19. Execution sequencing (for the plan phase)

1. **WS0 foundation** (lifecycle + review fixes) — everything depends on it.
2. **WS3 SQLite** (default store; UI needs a DB to read).
3. **WS6 OTel** (metrics backbone; WS4 needs it).
4. **WS1 embedded UI + backend JSON API** (needs WS3 for data, WS0 for the token proxy).
5. **WS2 radar** (needs WS1's HTTP surface + UI map card; adds the Python radar sidecar + the Go
   proxy/cache — separable/opt-in).
6. **WS4 Grafana** (needs WS6).
7. **WS5 UX scan** (folds into WS1's UI work; P0/P1 alongside, P2 after).

Each task: TDD, `superpowers:verification-before-completion`, per-task review; a fresh cold-review on
the full diff before finishing.

---

## 20. References (cited)

- Exporter review: `docs/review/2026-07-17-code-review-summary.md` and the per-file agent reports
  (review-a … review-g).
- UI review: `docs/review/2026-07-17-ui-code-review-summary.md` and review-ui-a … review-ui-f.
- DRAS (radar prior art): `github.com/jacaudi/DRAS` — `docs/architecture.md` (server-side decode,
  `s3://unidata-nexrad-level2-chunks/`, per-scan cache, anonymous S3, error envelope) and
  `dras/internal/radar/radar.go` (site validation, VCP catalog, soft-fail parsing).
- NEXRAD Level 3 on AWS: Registry of Open Data on AWS (`registry.opendata.aws/noaa-nexrad/`); NSF
  Unidata "NEXRAD Archive data available on Amazon S3" — bucket `s3://unidata-nexrad-level3/`,
  `us-east-1`, anonymous, naming `SSS_PPP_YYYY_MM_DD_HH_MM_SS`.
- NIDS base reflectivity products `N0B` (super-res) / `N0Q` (legacy): IEM NEXRAD Mosaics docs;
  Supercell-Wx NEXRAD-L3 docs; MetPy NEXRAD Level 3 example. Decode references: NOAA/NWS ICD "RPG to
  Class 1 User"; `github.com/jjhelmus/nexrad_level3` (Python); `github.com/netbymatt/nexrad-level-3-data`
  (JS). Go Level II (not L3): `github.com/bwiggs/go-nexrad`.
- Litestream + SQLite WAL: `litestream.io/how-it-works/` and `/tips/` — takes over checkpointing via a
  long read transaction + shadow WAL; requires WAL mode; streams to object storage.
- OpenTelemetry Go: `opentelemetry.io/docs/languages/go/`; metrics/traces OTLP **stable**, logs
  **experimental** (`opentelemetry.io/docs/specs/status/`). Collector Prometheus path:
  Grafana "collect Prometheus metrics with the OpenTelemetry Collector"; OTel `prometheus` /
  `prometheusremotewrite` exporters.
- WeatherFlow Tempest API: `weatherflow.github.io/Tempest/api/` — supports `Authorization: Bearer`
  header (and query-param token); OAuth available but PAT via server proxy is sufficient here.
- Basemap (v2/B2): MapLibre GL JS (`maplibre.org`) + the `pmtiles` protocol (`github.com/protomaps/PMTiles`);
  OpenStreetMap vector `.pmtiles` built/obtained via Protomaps (`build.protomaps.com`, `pmtiles extract`);
  OSM attribution required (`© OpenStreetMap contributors`, `openstreetmap.org/copyright`). Radar site
  table generated from NOAA HOMR WSR-88D station list (`ncei.noaa.gov/access/homr/file/nexrad-stations.txt`).
```

