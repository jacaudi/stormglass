# Changelog

## [3.1.0](https://github.com/jacaudi/tempestwx-utilities/compare/v3.0.0...v3.1.0) (2026-07-31)


### Features

* add the backfill subcommand and reject unknown subcommands ([1299bd0](https://github.com/jacaudi/tempestwx-utilities/commit/1299bd021e1e36289256bebb5337a6d1284c08f3))
* add the backfill subcommand to repair gaps in observation history from the REST API ([552b6c4](https://github.com/jacaudi/tempestwx-utilities/commit/552b6c4fd5e2b673e76e0218560321f40272e6b8))
* **backfill:** add API window chunking and retry classification ([71e09a6](https://github.com/jacaudi/tempestwx-utilities/commit/71e09a6cbb4ab290f94109ef8d57af4f6cf46a0e))
* **backfill:** add the Run core with injected clock, store, and API source ([5477bc6](https://github.com/jacaudi/tempestwx-utilities/commit/5477bc67ce8ae08e144ba7f1c053be88e15fea84))
* **backfill:** assemble head, tail, and empty-store gaps around LAG's interior gaps ([203ff68](https://github.com/jacaudi/tempestwx-utilities/commit/203ff683c4db127a96b2ebc751f6236f29251011))
* **postgres:** add partitioned gap detection and idempotent backfill insert ([d6d0300](https://github.com/jacaudi/tempestwx-utilities/commit/d6d030012b839b9c95b1285e40781fdbcb8383f0))
* **sqlite:** add partitioned gap detection and idempotent backfill insert ([8b88b6b](https://github.com/jacaudi/tempestwx-utilities/commit/8b88b6b030eec982a16f1cbbfc20b7b60a17d315))
* **tempestapi:** add Observations with null-preserving decode ([dfb241f](https://github.com/jacaudi/tempestwx-utilities/commit/dfb241f5d776b87d6e6b471c52cd7b5a8a5f829e))
* **tempestapi:** export Station identity, add StatusError and ListDevices ([d43251c](https://github.com/jacaudi/tempestwx-utilities/commit/d43251c8c9c1c161ae540d73242cc133e5532562))
* **weather:** add store-neutral Observation, Gap, and Bounds types ([327e54a](https://github.com/jacaudi/tempestwx-utilities/commit/327e54a80a6e25c9e6fe566ff9f6f6328d0d69d5))


### Bug Fixes

* **backfill:** pin the chunk max-width invariant and guard a non-positive size ([4bac68d](https://github.com/jacaudi/tempestwx-utilities/commit/4bac68dca57746efe77b20a4fa993d00e802ab14))
* **backfill:** reject a half-specified range, pin Returned, prompt cancellation, and retry exhaustion ([dd80237](https://github.com/jacaudi/tempestwx-utilities/commit/dd80237d42b56b65d63cf8c35d168c2fd7aef495))
* **ci:** go-release Go 1.26 + drop release-as (v3.0.0 binaries job failed) ([8990b88](https://github.com/jacaudi/tempestwx-utilities/commit/8990b889ecafa67c4fdf1496b2b1100a595a9667))
* **ci:** go-release must install Go 1.26 (go.mod needs &gt;= 1.25, setup-go@v6 pins GOTOOLCHAIN=local) ([6cb7a00](https://github.com/jacaudi/tempestwx-utilities/commit/6cb7a00291d076453e4e550c493947fb6c5a51a0))
* **main:** structured slog for tainted export logs, drop G706 suppressions ([#50](https://github.com/jacaudi/tempestwx-utilities/issues/50)) ([a3d30da](https://github.com/jacaudi/tempestwx-utilities/commit/a3d30da241c96d4547f7825283ffe783f9ae8728))
* P1 issues — sink backpressure bound ([#47](https://github.com/jacaudi/tempestwx-utilities/issues/47)), isRetryable classification ([#48](https://github.com/jacaudi/tempestwx-utilities/issues/48)), G706 log injection ([#50](https://github.com/jacaudi/tempestwx-utilities/issues/50)) ([d8c43bf](https://github.com/jacaudi/tempestwx-utilities/commit/d8c43bf2ba1593d2ed07f510f86582c4b5b9d270))
* pin the inserted-count plumbing, store integral columns as integers, widen the PG round-trip ([ea452e5](https://github.com/jacaudi/tempestwx-utilities/commit/ea452e55cfdbb6a29ba36522eafb45f9a1038861))
* **postgres:** default unknown errors to non-retryable in isRetryable ([#48](https://github.com/jacaudi/tempestwx-utilities/issues/48)) ([2963277](https://github.com/jacaudi/tempestwx-utilities/commit/29632770240c1412206c57f6dcf92b60d74a5f81))
* **postgres:** retry startup/connection SQLSTATEs (57P03, 57P02, class 08) (review) ([03c04bc](https://github.com/jacaudi/tempestwx-utilities/commit/03c04bc30482b75b5bdcd70dd879f6caeb3eefe2))
* **sink:** bound per-writer send so a stalled sink cannot block UDP ingest ([#47](https://github.com/jacaudi/tempestwx-utilities/issues/47)) ([00445fb](https://github.com/jacaudi/tempestwx-utilities/commit/00445fb3fd90c2bb3fd34bb388aa3c31e1dbc46e))
* **sink:** unbound SendMetrics (export path) + struct-field write timeout (review) ([4784d43](https://github.com/jacaudi/tempestwx-utilities/commit/4784d430bf9b5f682eb78d12412b4fd48d7da666))
* **tempestapi:** log drop windows in UTC, pin the request path, quiet the drop test ([97bffee](https://github.com/jacaudi/tempestwx-utilities/commit/97bffeee14008cb16b018435098611e5edc348b6))


### Miscellaneous Chores

* gitignore session handoff prompts ([9f95293](https://github.com/jacaudi/tempestwx-utilities/commit/9f95293d8197d1b206d5a15e8199da716caaa9e6))

## [3.0.0](https://github.com/jacaudi/tempestwx-utilities/compare/v2.0.0...v3.0.0) (2026-07-29)


### Features

* /api/observations current+history from sqlite (UI B-H2) ([208d8a4](https://github.com/jacaudi/tempestwx-utilities/commit/208d8a426ec52eceacfd1f11b42981c7e12fb604))
* /api/radar/{site} handler, opt-in ENABLE_RADAR (Contract C) ([1abf445](https://github.com/jacaudi/tempestwx-utilities/commit/1abf4459e0977984b4ab46f356d575d3339bc1b9))
* default to sqlite store, postgres opt-in (R2) ([17a3f93](https://github.com/jacaudi/tempestwx-utilities/commit/17a3f934214a6953e1d47c8f9ad76e2dd16a9b2a))
* dewpoint + heat-index derived helpers (tempestudp) ([aef4007](https://github.com/jacaudi/tempestwx-utilities/commit/aef4007d7f11b03f4907e62adb9d4f4be0fdbbfe))
* DOC.1 — full-stack docker-compose + Collector/Prometheus/Grafana/Litestream/MinIO configs (§15a) ([c69bf42](https://github.com/jacaudi/tempestwx-utilities/commit/c69bf42e162330b3d2b43965a990a5bfdac0b112))
* embedded UI HTTP server (timeouts, headers, SPA fallback, /healthz) ([ae403dd](https://github.com/jacaudi/tempestwx-utilities/commit/ae403dd6eea41daa434da6b29ce253a14080ee2c))
* error boundary + missing CSS + responsive/a11y + NaN-safe formatX (UI A-H1..H4, C-MEDIUM) ([7c4e64b](https://github.com/jacaudi/tempestwx-utilities/commit/7c4e64be8e8bbde51d0244aa11df054858223837))
* full-stack docker-compose (Collector+Prometheus+Grafana+Litestream+MinIO; radar opt-in) — §15a ([2482056](https://github.com/jacaudi/tempestwx-utilities/commit/2482056a01ff06431c187b97a240a882386c77db))
* Grafana Weather Nerd dashboard + provisioning (§13) ([7ef7374](https://github.com/jacaudi/tempestwx-utilities/commit/7ef7374dfe9789226cd0362fa79e85d10046c672))
* **httpserver:** GET /api/observations/summary endpoint ([187db46](https://github.com/jacaudi/tempestwx-utilities/commit/187db460b75575ce1f50ad58764bd88be1981617))
* internal/otel setup — meter/tracer/logger providers + OTLP (R1) ([0081dfd](https://github.com/jacaudi/tempestwx-utilities/commit/0081dfd8d8c0c5765482b1eab5b9b6a7b5442732))
* NEXRAD Level 3 radar overlay (opt-in Py-ART sidecar + Go proxy + MapLibre UI) — Workstream 2 ([489b1e9](https://github.com/jacaudi/tempestwx-utilities/commit/489b1e9b515109c3b228e4c1ef359cd1228478ff))
* otel sink writer with tempest_* instrument names (D-MEDIUM hygiene) ([8f84bad](https://github.com/jacaudi/tempestwx-utilities/commit/8f84bad90851873404bf8660e7aadbaa140c4851))
* otelhttp middleware + start UI/API server from main ([ad4362f](https://github.com/jacaudi/tempestwx-utilities/commit/ad4362f35b5b55e9e6bdf87e3bf7b65510c7fa7d))
* python radar sidecar (Py-ART → contoured GeoJSON, Contract A) + committed NIDS fixture ([36b4452](https://github.com/jacaudi/tempestwx-utilities/commit/36b445284a00f6bca2d972aa71aba9a5eec76806))
* radar proxy + LRU cache + N0B→N0Q fallback (O2, Contract A) ([5524818](https://github.com/jacaudi/tempestwx-utilities/commit/5524818a62afe39e985a0b188d5ba98cea653d0b))
* radar site table (generated from NOAA HOMR) + nearest-site + allowlist (SSRF guard) ([9a83879](https://github.com/jacaudi/tempestwx-utilities/commit/9a83879f99af38008d681d1724112ce6ad4ed5ea))
* real UI data layer (Contract C) + AbortController + stale indicator (UI B-H2, B-MEDIUM, §14 P1.6) ([88e8612](https://github.com/jacaudi/tempestwx-utilities/commit/88e8612afdf72f30b123347e5aa50e31c1383217))
* remove dead token inputs + self-host Inter font (UI D-MEDIUM, A-MEDIUM) ([171f1a5](https://github.com/jacaudi/tempestwx-utilities/commit/171f1a58593145063df71d6cc862024ad0180673))
* server-side WeatherFlow proxy (UI B-H1, exporter F-H1) ([74b16e0](https://github.com/jacaudi/tempestwx-utilities/commit/74b16e001c0e8e52dc19a3e3c8ee51227c2f3fcb))
* slog→OTel log bridge + wire OTel sink (ENABLE_OTEL) ([b96fff5](https://github.com/jacaudi/tempestwx-utilities/commit/b96fff5d9e6d08bdf322455e12944f138eca9d4f))
* sqlite drain-on-close + read methods for the JSON API ([cc8a937](https://github.com/jacaudi/tempestwx-utilities/commit/cc8a93737a24a64786d51991f5dd55fa07d2c11e))
* sqlite Open with exact PRAGMAs (design §10) ([973adab](https://github.com/jacaudi/tempestwx-utilities/commit/973adab107486985c73e60b2086be0e786f7abb5))
* sqlite schema + embedded migrations (B-MEDIUM) ([bb56b33](https://github.com/jacaudi/tempestwx-utilities/commit/bb56b33ed0de3be157620f965d13a0f33debf1a9))
* sqlite writer (single-writer, idempotent, backpressure-safe) ([a04f707](https://github.com/jacaudi/tempestwx-utilities/commit/a04f707ca2258866ff6c781f7f8fe41e8eaa674f))
* **sqlite:** read-only handle for query-side reads (decouple from ingest writer) ([efcd288](https://github.com/jacaudi/tempestwx-utilities/commit/efcd28857eee208546847ecdf99e2aa24dd4b101))
* **sqlite:** SummarizeObservations windowed aggregate ([83571fb](https://github.com/jacaudi/tempestwx-utilities/commit/83571fbe390b8f7e05379f1079cf2ef5fd2fcf36))
* tracing spans for udp ingest + export loop ([276a381](https://github.com/jacaudi/tempestwx-utilities/commit/276a3818c10c16ae50920b37fe5a195f58689a7b))
* UI P2 polish — memoization, dialog a11y, theme leak, viewport (§14 P2) ([73fc6a2](https://github.com/jacaudi/tempestwx-utilities/commit/73fc6a2c6eefe6ac8042947579a340afa90f5b0c))
* UI radar map card (MapLibre + same-origin OSM pmtiles basemap, dBZ isobands) — §14 P1.8, B2 ([44634f7](https://github.com/jacaudi/tempestwx-utilities/commit/44634f709d75bbe4c41df94c3752ae2c4933e183))
* unified OpenTelemetry (OTLP) backbone (Workstream 6) ([489d4d0](https://github.com/jacaudi/tempestwx-utilities/commit/489d4d08d4a807e55c304b609c39d0ca95ace27f))
* vendor tempest-display UI into web/ (owned fork [@49892063](https://github.com/49892063)) + UI manifest ([719ffa4](https://github.com/jacaudi/tempestwx-utilities/commit/719ffa421c4ffbac8bdec4b18e355e7dd0a2b103))
* **web:** fetch records summary keyed on the window pref ([8014c36](https://github.com/jacaudi/tempestwx-utilities/commit/8014c366b6908a5b0e9be1467a82c23203ff14dc))
* **web:** RecordsCard component + theme-safe CSS ([27343d8](https://github.com/jacaudi/tempestwx-utilities/commit/27343d8db4011843a727ca3f78846f2b0037d6ba))
* **web:** RecordsSummary type + fetchRecordsSummary + recordsWindowDays pref ([1be9bc4](https://github.com/jacaudi/tempestwx-utilities/commit/1be9bc4f589e9d040a3bfece3149afc7b0c25fd0))
* **web:** render RecordsCard above the 7-day forecast ([1ed3766](https://github.com/jacaudi/tempestwx-utilities/commit/1ed3766da114eaeff5ddad6f8f9afa072d819234))
* **web:** Settings records-window selector ([6de6526](https://github.com/jacaudi/tempestwx-utilities/commit/6de6526f358f4376078a9180e2855746f8ad188c))
* Workstream 1 — embedded UI + Contract-C JSON API ([0662fe8](https://github.com/jacaudi/tempestwx-utilities/commit/0662fe8d1f56cad6b0825ffe8a713caf5d337cb5))
* Workstream 4 — Grafana "Weather Nerd" dashboard + OTel→Prometheus name-translation test (§13, Contract B) ([df3be64](https://github.com/jacaudi/tempestwx-utilities/commit/df3be64f5a1295140da33ee7469cbc07a4ebe445))
* Workstream 5 (UX.1) — UI P2 polish: memoization, dialog a11y, theme-var leak fix, viewport/transition CSS (§14 P2) ([eee1153](https://github.com/jacaudi/tempestwx-utilities/commit/eee1153c8aace5b3184486a1c30ca7637e7ef6b0))


### Bug Fixes

* chown init for /data so non-root app (UID 65532) can write SQLite on fresh volume (review) ([fa6a06b](https://github.com/jacaudi/tempestwx-utilities/commit/fa6a06b57efb95085d338f12f537870a0324ce56))
* **ci:** set group-pull-request-title-pattern so the release PR title carries the version ([9e5d752](https://github.com/jacaudi/tempestwx-utilities/commit/9e5d752b43ae5fe35e8efb5aff6b8b59befd4ad2))
* clear isLoading on aborted initial load (1.7a review) ([c9ba7f6](https://github.com/jacaudi/tempestwx-utilities/commit/c9ba7f6970424d33ac59926d24728d972d48a5d5))
* cumulative reboot/bus-error counters + host.name resource attr (cold-review C1/I1) ([a0641e6](https://github.com/jacaudi/tempestwx-utilities/commit/a0641e6d6263ac2db565ecee709c9449374f69d7))
* **deps:** update module github.com/jackc/pgx/v5 to v5.8.0 ([#27](https://github.com/jacaudi/tempestwx-utilities/issues/27)) ([99aa307](https://github.com/jacaudi/tempestwx-utilities/commit/99aa307053169a7b8b5cdc98d2259ea7b7f7019d))
* **deps:** update module github.com/prometheus/common to v0.67.5 ([#28](https://github.com/jacaudi/tempestwx-utilities/issues/28)) ([1252ee8](https://github.com/jacaudi/tempestwx-utilities/commit/1252ee8393890de7abd1f88c18e2a22f8fade552))
* gust-factor ignoring(kind) + pressure-tendency mb/3h units + complete negative guard (cold review) ([50f7e79](https://github.com/jacaudi/tempestwx-utilities/commit/50f7e794e848d6df1ff9974256a37bb3b137face))
* healthcheck url robustness + gosec nolint + stale version comment (W1 gate) ([87f199c](https://github.com/jacaudi/tempestwx-utilities/commit/87f199ca86d84350c427b3e0085a820585557463))
* index read hot-path + cap history + NaN-safe derived + static caching/nil-guard (SGE review I1/M1/M2/M3/M7) ([f5dce87](https://github.com/jacaudi/tempestwx-utilities/commit/f5dce87280991de9e0bdcf5322bed1cbf5e7c350))
* NaN/Inf-guard dewpoint + heat_index records (parity with wetbulb) ([c9f2c24](https://github.com/jacaudi/tempestwx-utilities/commit/c9f2c2428feddd0a45aa795ab66c8c7aa218c876))
* owning-run spinner + status retain-on-failure + wire isStale + drop dead hourly slice (SGE review M4/M5/M6a/M6b) ([7556f7f](https://github.com/jacaudi/tempestwx-utilities/commit/7556f7fd109ffef8fe461d8075235a9959d0ee6e))
* **sqlite:** register modernc driver in production (default store was dead in the binary) ([e9d4ac8](https://github.com/jacaudi/tempestwx-utilities/commit/e9d4ac84b44cd55d4028ab75d61ec94ec2add43f))
* **sqlite:** register modernc driver in production code ([ee0bec7](https://github.com/jacaudi/tempestwx-utilities/commit/ee0bec74a13d29fec9e491698eb7270726296939))
* **ui:** add missing Settings-panel CSS (modal overlay, toggle groups, theme grid) + wire theme swatches (§14) ([3fac98e](https://github.com/jacaudi/tempestwx-utilities/commit/3fac98e7f6aabb9fc849997dc2c2e48a3967c271))
* **ui:** cold-review fixes — focus-trap Shift+Tab boundary, active-toggle contrast, rainfall dvh fallback, poll ref-stability (§14) ([7e9151f](https://github.com/jacaudi/tempestwx-utilities/commit/7e9151f0e7ef9a556f8aa98fc439d4f1ada21844))
* validate radar product param against {N0B,N0Q} (cold-review hardening) ([4b68e1b](https://github.com/jacaudi/tempestwx-utilities/commit/4b68e1b0259601ba817743b615df5efd96a547e1))
* **web:** stack Records pair labels for single-line values + Lightning "strikes" unit ([4bb0ced](https://github.com/jacaudi/tempestwx-utilities/commit/4bb0ced3f2b0909db15e15cc7015752e5a1d64bf))


### Miscellaneous Chores

* deprecation warning on bespoke prometheus path (O4) ([9bc2773](https://github.com/jacaudi/tempestwx-utilities/commit/9bc2773bd18dfe19431d639ab065662f1d28c4e6))
* **deps:** migrate to shared renovate config ([#30](https://github.com/jacaudi/tempestwx-utilities/issues/30)) ([59b8270](https://github.com/jacaudi/tempestwx-utilities/commit/59b82700964cfe0ad6ceb7e5da863702a920e630))
* **deps:** update dependency go to v1.25.6 ([#29](https://github.com/jacaudi/tempestwx-utilities/issues/29)) ([8f882f2](https://github.com/jacaudi/tempestwx-utilities/commit/8f882f295f3d31ad0b9d7b961a8a5ed6873cd920))
* **deps:** update dependency go to v1.26.1 ([#32](https://github.com/jacaudi/tempestwx-utilities/issues/32)) ([787e7a2](https://github.com/jacaudi/tempestwx-utilities/commit/787e7a2741dbc6c5d762ab99a45b4570bc4bee59))
* **deps:** update github actions ([#36](https://github.com/jacaudi/tempestwx-utilities/issues/36)) ([03ed7fa](https://github.com/jacaudi/tempestwx-utilities/commit/03ed7fa0ba915e980d55edc67f3e2b44300e8ef1))
* **deps:** update golang docker tag to v1.26 ([#33](https://github.com/jacaudi/tempestwx-utilities/issues/33)) ([b64beef](https://github.com/jacaudi/tempestwx-utilities/commit/b64beef1aaa23c1634d3394faf045a69ddd70aef))
* **deps:** update goreleaser/goreleaser-action action to v7 ([#34](https://github.com/jacaudi/tempestwx-utilities/issues/34)) ([4164c1c](https://github.com/jacaudi/tempestwx-utilities/commit/4164c1c6b84a06aae11fd6f27e0ceaa81a612549))
