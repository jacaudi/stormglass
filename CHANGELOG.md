# Changelog

## [4.1.0](https://github.com/jacaudi/tempestwx-utilities/compare/v4.0.0...v4.1.0) (2026-08-18)


### Features

* **radar:** name the nearest WSR-88D site when RADAR_SITE is rejected ([ca157f8](https://github.com/jacaudi/tempestwx-utilities/commit/ca157f8762d9034abdd0778e2fcc180f280e9a5b)), closes [#169](https://github.com/jacaudi/tempestwx-utilities/issues/169)
* **radar:** name the nearest WSR-88D site when RADAR_SITE is rejected, and prove the startup diagnostics are emitted ([fb52e3f](https://github.com/jacaudi/tempestwx-utilities/commit/fb52e3fc7a8017f310424416a819bea8bc04f1d0))

## [4.0.0](https://github.com/jacaudi/tempestwx-utilities/compare/v3.1.2...v4.0.0) (2026-08-16)


### ⚠ BREAKING CHANGES

* delete the WeatherFlow proxy from the UDP-mode appliance

### Features

* add ENABLE_FORECAST and ENABLE_ALMANAC ([9338df9](https://github.com/jacaudi/tempestwx-utilities/commit/9338df928696806ed65458085024709b11fe9c7b)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **almanac:** warn when coordinates are set and STATION_TIMEZONE is not ([6a02b03](https://github.com/jacaudi/tempestwx-utilities/commit/6a02b039abf1089503c806057ba59d2fbf330dc4)), closes [#165](https://github.com/jacaudi/tempestwx-utilities/issues/165)
* **astro:** compute moon phase, name and illumination via Meeus 48.4 ([16e97fb](https://github.com/jacaudi/tempestwx-utilities/commit/16e97fb6ef64967f21e0ac7f1506c48bba415b7e))
* **astro:** compute sunrise and sunset from vendored NOAA equations ([1bd04b2](https://github.com/jacaudi/tempestwx-utilities/commit/1bd04b254b828a179bfa1a0860831e204cc316b3))
* **config:** load station identity from STATION_* and RADAR_SITE ([1face3e](https://github.com/jacaudi/tempestwx-utilities/commit/1face3e815f4f5b25456d06a781a6b26c0a25f3a))
* gate the Forecast, Radar and Almanac cards behind config ([4654e97](https://github.com/jacaudi/tempestwx-utilities/commit/4654e97f57ea42e4ceb1236d7cacea3ba8f10e13))
* **httpserver:** add calendar-aligned almanac window arithmetic ([e6dde8c](https://github.com/jacaudi/tempestwx-utilities/commit/e6dde8c3bb20e6ffbeaea5ba6fecfc23fd4c4c87))
* **httpserver:** add GET /api/capabilities ([eb40eb0](https://github.com/jacaudi/tempestwx-utilities/commit/eb40eb0407df4a4e5d70453ae51bb60426a2a251)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **httpserver:** compute /api/almanac from the local store and astronomy ([5b626cb](https://github.com/jacaudi/tempestwx-utilities/commit/5b626cb2c7e88da3dba0ec1400c1afde2f95ba63))
* **httpserver:** register /api/forecast and /api/almanac only when enabled ([ac14a78](https://github.com/jacaudi/tempestwx-utilities/commit/ac14a78f265c3dd2a26d1a55527006c9659b74db)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **httpserver:** serve /api/station from station identity config ([1cc03c0](https://github.com/jacaudi/tempestwx-utilities/commit/1cc03c05a271edbc6ee10abbd02443187b1efe44))
* **main:** degrade loudly on unmet UI preconditions instead of exiting ([b4838e9](https://github.com/jacaudi/tempestwx-utilities/commit/b4838e9848b2b101f9ddc14abd40fc2a2ea3314e))
* make the UDP-mode appliance tokenless and delete the WeatherFlow proxy ([017fdfa](https://github.com/jacaudi/tempestwx-utilities/commit/017fdfa13c1d99a2362cb6c851d1e5756e27cb04))
* **radar:** reject an unknown RADAR_SITE at startup ([9aaa8b3](https://github.com/jacaudi/tempestwx-utilities/commit/9aaa8b3637ced430008157fbab73c419d7213d54)), closes [#163](https://github.com/jacaudi/tempestwx-utilities/issues/163)
* **sqlite:** add TemperatureExtremes with argmax timestamps ([462b77a](https://github.com/jacaudi/tempestwx-utilities/commit/462b77a693baee8c105ddd8d61d95358016d4bd5))
* **web:** add Capabilities type and fetchCapabilities ([4616860](https://github.com/jacaudi/tempestwx-utilities/commit/46168606c9967bc84f9a41dfc804d1cc57a99e4d))
* **web:** fetch capabilities in useWeatherData and skip disabled slices ([6a7642f](https://github.com/jacaudi/tempestwx-utilities/commit/6a7642f06782d835fdb2c78433e6d94e09af93c0)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **web:** mount Forecast, Almanac and Radar only when enabled ([2711979](https://github.com/jacaudi/tempestwx-utilities/commit/2711979be63a744114583b8f9f66533fb244c4f1)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **web:** retype the station and almanac contracts for the tokenless API ([a6897c9](https://github.com/jacaudi/tempestwx-utilities/commit/a6897c96768fa73f92d1f06a48a3a5ffe90906de))
* **web:** wire RADAR_SITE through /api/station to the radar card ([08e65f9](https://github.com/jacaudi/tempestwx-utilities/commit/08e65f9c2a2e57a265ce8b39d13ef76eff2d55e2))


### Bug Fixes

* address SR-eng review findings (comments, almanac windows, radar site) ([8b23f92](https://github.com/jacaudi/tempestwx-utilities/commit/8b23f92204b4139086f9f919e9ef634ab27ea841))
* **astro:** anchor sunrise/sunset on solar noon, not the UTC date ([5565e87](https://github.com/jacaudi/tempestwx-utilities/commit/5565e87039e7f9d77cdfec77c33ec5de0b5c0d92)), closes [#166](https://github.com/jacaudi/tempestwx-utilities/issues/166)
* **astro:** compute the obliquity once and anchor it in a test ([12f1953](https://github.com/jacaudi/tempestwx-utilities/commit/12f19537c4296ae036db177e95cf88a953db7ff9)), closes [#167](https://github.com/jacaudi/tempestwx-utilities/issues/167)
* **backfill:** close the three observability and seam gaps ([#108](https://github.com/jacaudi/tempestwx-utilities/issues/108), [#109](https://github.com/jacaudi/tempestwx-utilities/issues/109), [#110](https://github.com/jacaudi/tempestwx-utilities/issues/110)) ([73352e1](https://github.com/jacaudi/tempestwx-utilities/commit/73352e10184e0c6c14e875fcbbb9d8beabd04c49))
* **backfill:** reject the epoch sentinel and announce the plan before filling ([c47a397](https://github.com/jacaudi/tempestwx-utilities/commit/c47a39718accb55391361869736282c5c22cf602)), closes [#108](https://github.com/jacaudi/tempestwx-utilities/issues/108)
* correct prose findings from the final whole-branch review ([5fe7f4e](https://github.com/jacaudi/tempestwx-utilities/commit/5fe7f4e5d5084aa02c36b443158fef286d2b31e1))
* **postgres:** bound retry backoff by the flush ctx, not the writer's ctx ([fd77cb6](https://github.com/jacaudi/tempestwx-utilities/commit/fd77cb6f20a5ecb4b823de7ed97d38a600d8c4a5)), closes [#153](https://github.com/jacaudi/tempestwx-utilities/issues/153)
* **postgres:** flush with a shutdown-proof context so Close loses no rows ([1a40bbf](https://github.com/jacaudi/tempestwx-utilities/commit/1a40bbf1ee82620286c51b68be2e0505adcfa4b3)), closes [#111](https://github.com/jacaudi/tempestwx-utilities/issues/111)
* solar-meridian sunrise anchor, single obliquity, and two startup diagnostics ([d15670f](https://github.com/jacaudi/tempestwx-utilities/commit/d15670fef338e8471ee4a937d6277b49b5556ef0))
* **sqlite:** detach steady-state flushes from the cancellable run ctx ([d5f5495](https://github.com/jacaudi/tempestwx-utilities/commit/d5f54957bd8a98804e395de749a4de16ecb879ca)), closes [#154](https://github.com/jacaudi/tempestwx-utilities/issues/154)
* stop losing rows on shutdown in both writers ([c85ba74](https://github.com/jacaudi/tempestwx-utilities/commit/c85ba743987817eaea27610bf969cef12cd7cc20))
* **tempestapi:** check the WeatherFlow envelope status_code in GetObservations ([c27c4d0](https://github.com/jacaudi/tempestwx-utilities/commit/c27c4d0b966dd7d54c297635c9589ed495bb2457))
* **tempestapi:** check the WeatherFlow envelope status_code in GetObservations ([6e8deb6](https://github.com/jacaudi/tempestwx-utilities/commit/6e8deb61d7beffce8394162554ffde01561b40cb)), closes [#49](https://github.com/jacaudi/tempestwx-utilities/issues/49)
* **web:** refetch the records summary when Refresh is pressed ([34f7c0b](https://github.com/jacaudi/tempestwx-utilities/commit/34f7c0b7ed3da96a2d18a8956ba4e448e046f3c1))
* **web:** refetch the records summary when Refresh is pressed ([570e9db](https://github.com/jacaudi/tempestwx-utilities/commit/570e9db55bee49b1c38aba258743a2f01b26b061)), closes [#89](https://github.com/jacaudi/tempestwx-utilities/issues/89)
* **web:** stop a capability flip from blanking a rendered dashboard ([821d8e2](https://github.com/jacaudi/tempestwx-utilities/commit/821d8e22db4a34d952dd4f7a79dd79627a274da2)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
* **web:** stop rendering NaN coordinates for a station-less response ([1fdefb3](https://github.com/jacaudi/tempestwx-utilities/commit/1fdefb339f51697f703b9356133e06136b1666c4)), closes [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)


### Miscellaneous Chores

* **deps:** Update grafana/grafana Docker tag to v13 ([2e19f69](https://github.com/jacaudi/tempestwx-utilities/commit/2e19f693b713c3d62e71396ef0d0350e7a4a9fc5))
* **deps:** Update python Docker tag to v3.14 ([b3f901a](https://github.com/jacaudi/tempestwx-utilities/commit/b3f901ab91941c8a6af6896cd404decdc3cc9b0c))
* gitignore implementation plans; add the card-gating design ([2c41fe2](https://github.com/jacaudi/tempestwx-utilities/commit/2c41fe2927f85c1322b5dffc8c7e2de3ddadc36d))


### Code Refactoring

* delete the WeatherFlow proxy from the UDP-mode appliance ([5279803](https://github.com/jacaudi/tempestwx-utilities/commit/5279803fe3a6d1a016ad132446b7ba35165b7f50))

## [3.1.2](https://github.com/jacaudi/tempestwx-utilities/compare/v3.1.1...v3.1.2) (2026-08-07)


### Miscellaneous Chores

* **deps:** Update cgr.dev/chainguard/static:latest Docker digest to 24dd7ff ([ebc3121](https://github.com/jacaudi/tempestwx-utilities/commit/ebc3121e54818848dd110419522536a48bdbe6bd))
* **deps:** Update dependency globals to v17 ([0bb4d86](https://github.com/jacaudi/tempestwx-utilities/commit/0bb4d865356b9b764b7e9530a9432d83467408ad))
* **deps:** Update dependency globals to v17 ([6c11c81](https://github.com/jacaudi/tempestwx-utilities/commit/6c11c8160d66308c1919e8650adc95ff6db6ce50))
* **deps:** Update dependency maplibre-gl to v6.2.0 ([41d330c](https://github.com/jacaudi/tempestwx-utilities/commit/41d330cb2c11637342310502c38ac1292ae2e93a))
* **deps:** Update dependency uvicorn to v0.52.1 ([5cac13d](https://github.com/jacaudi/tempestwx-utilities/commit/5cac13d7bd26bc3fd65950061ca4dff80c302107))
* **deps:** Update dependency uvicorn to v0.52.1 ([8547b5d](https://github.com/jacaudi/tempestwx-utilities/commit/8547b5dbe40d40b0748c2fc856517917adce0cc6))
* **deps:** Update dependency vite to v8.2.1 ([17ce621](https://github.com/jacaudi/tempestwx-utilities/commit/17ce621b47555b01ed62570bfbdcbb8839d4a8b6))
* **deps:** Update litestream/litestream Docker tag to v0.5.16 ([674aff2](https://github.com/jacaudi/tempestwx-utilities/commit/674aff26cb0bd5f4f7f9e45ee55fb9694d666ec5))
* **gitignore:** ignore .auto-claude/ and docs/screenshots/ ([5c100d3](https://github.com/jacaudi/tempestwx-utilities/commit/5c100d3117154af371dd2d6321c4619233fbda9b))
* **gitignore:** ignore .auto-claude/ and docs/screenshots/ ([1892854](https://github.com/jacaudi/tempestwx-utilities/commit/1892854f416ca24e3a23b7b666b794bfbfc49b3a))
* **renovate:** group routine Docker image updates into one PR ([caf7103](https://github.com/jacaudi/tempestwx-utilities/commit/caf7103c9fe01d1353007235c3fd997668fee4c5))
* **renovate:** group routine Docker image updates into one PR ([8198484](https://github.com/jacaudi/tempestwx-utilities/commit/81984845297da5ef87e447382228c3a2e2dbc84a))

## [3.1.1](https://github.com/jacaudi/tempestwx-utilities/compare/v3.1.0...v3.1.1) (2026-08-05)


### Bug Fixes

* **ci:** push to GHCR with GITHUB_TOKEN, not the App token ([8c03532](https://github.com/jacaudi/tempestwx-utilities/commit/8c03532ad34fe46d306e9e54e9ca2d2b8f9ccc7c))
* **deps:** clear six reachable CVEs govulncheck reports ([a9cc6f9](https://github.com/jacaudi/tempestwx-utilities/commit/a9cc6f989f8f310b8fe6b0bfc427ffa6cabf2e04))
* **deps:** clear six reachable CVEs govulncheck reports ([a150527](https://github.com/jacaudi/tempestwx-utilities/commit/a15052786b07c75293b72a8917a45a4e3caa1591))
* **deps:** move to eslint v10 ([d5410ea](https://github.com/jacaudi/tempestwx-utilities/commit/d5410eaa6f13ecf97b80c72707594377ca838e94))
* **deps:** move to eslint v10 ([6c09b69](https://github.com/jacaudi/tempestwx-utilities/commit/6c09b690b10e8f673baa17afb31dd2e1d210852f))
* **deps:** move to maplibre-gl v6 (ESM-only) ([479d394](https://github.com/jacaudi/tempestwx-utilities/commit/479d394f9e3aff00bc7b8e4fd1b63e29ab98faba))
* **deps:** move to maplibre-gl v6 (ESM-only) ([a4a6608](https://github.com/jacaudi/tempestwx-utilities/commit/a4a6608e39abaa611e9c8e0a9b33aadbac6c5550))
* **deps:** move to vite v8 and @vitejs/plugin-react v6 together ([bde8662](https://github.com/jacaudi/tempestwx-utilities/commit/bde86629fd0cc364465d7b715603140eb58d1dc8))
* **deps:** move to vite v8 and @vitejs/plugin-react v6 together ([f3df169](https://github.com/jacaudi/tempestwx-utilities/commit/f3df1697d226dd98ef5400718a70ce583ce08ea4))
* **deps:** take otel log signal to v0.21.0 with the matching slog bridge ([e00ac9a](https://github.com/jacaudi/tempestwx-utilities/commit/e00ac9a432e96633d958c7317ffe63a39e8a2ac4))
* **deps:** take otel log signal to v0.21.0 with the matching slog bridge ([f0093ac](https://github.com/jacaudi/tempestwx-utilities/commit/f0093ac7ee1ddd2f8f82dfd0f4afc2d38669886e))
* **sqlite:** conform three measurement columns to float across both stores ([4898e06](https://github.com/jacaudi/tempestwx-utilities/commit/4898e063591f34fab8adece98cf984f251656634))
* **sqlite:** declare the three measurement columns REAL; fold 0002 into 0001 ([f928bb9](https://github.com/jacaudi/tempestwx-utilities/commit/f928bb9b52ff30ee6b5f589213d290b2d4d2af94))
* **sqlite:** read the three measurement columns as float64 ([c7ad83e](https://github.com/jacaudi/tempestwx-utilities/commit/c7ad83e96bac0b361495bc6b7cbfa6f9c989bdc3))
* **sqlite:** stop truncating measurement columns on the write path ([34dd455](https://github.com/jacaudi/tempestwx-utilities/commit/34dd455e70f0d043c806585804195a1d85ae2fc3))
* unblock roll-forward, close JSON fractional coverage gap, stop a test row leak ([1ea9f4d](https://github.com/jacaudi/tempestwx-utilities/commit/1ea9f4d9db3cf261e7cb77172be52f9a6fac4014))


### Miscellaneous Chores

* **deps:** Update busybox Docker tag to v1.38.0 ([b242f77](https://github.com/jacaudi/tempestwx-utilities/commit/b242f77523deb0b6a26fcb821995ef0d6f18fb60))
* **deps:** Update busybox Docker tag to v1.38.0 ([2d90d66](https://github.com/jacaudi/tempestwx-utilities/commit/2d90d665f08c7185a054051093f0087bd01a7d79))
* **deps:** Update cgr.dev/chainguard/static:latest Docker digest to 399c8cb ([3522dd8](https://github.com/jacaudi/tempestwx-utilities/commit/3522dd8368540d7745270a90fce4a07e0ec663a3))
* **deps:** Update cgr.dev/chainguard/static:latest Docker digest to 399c8cb ([67f27f2](https://github.com/jacaudi/tempestwx-utilities/commit/67f27f26156320e4c4ebcc4c6c789708d6f15281))
* **deps:** Update dependency @testing-library/jest-dom to v7 ([203f99f](https://github.com/jacaudi/tempestwx-utilities/commit/203f99f67840cd4da0187a77e0ebac0c4ba8790b))
* **deps:** Update dependency @testing-library/jest-dom to v7 ([6de4622](https://github.com/jacaudi/tempestwx-utilities/commit/6de462203f3efdcdb93002eaa7579f740f44ecab))
* **deps:** Update dependency @types/node to v24.13.3 ([47e82c4](https://github.com/jacaudi/tempestwx-utilities/commit/47e82c4b05147a3ade828c739e249e6f7027a93f))
* **deps:** Update dependency @types/node to v24.13.3 ([9c2589f](https://github.com/jacaudi/tempestwx-utilities/commit/9c2589f47a76bfb384c5d2b039aad5e5732087f3))
* **deps:** Update dependency eslint-plugin-react-refresh to ^0.5.0 ([8170216](https://github.com/jacaudi/tempestwx-utilities/commit/8170216061745e3ada4ffb7e22a1b9cb26b55a57))
* **deps:** Update dependency eslint-plugin-react-refresh to ^0.5.0 ([979dba7](https://github.com/jacaudi/tempestwx-utilities/commit/979dba7b9538f7218db2982fd7574928b83726bf))
* **deps:** Update dependency fastapi to v0.141.1 ([5c4edfe](https://github.com/jacaudi/tempestwx-utilities/commit/5c4edfefd5ce18560d492b3d60bc15671c6c6213))
* **deps:** Update dependency fastapi to v0.141.1 ([a75ca2e](https://github.com/jacaudi/tempestwx-utilities/commit/a75ca2edc82d5e5b7cd04a93188522256f9b3ec4))
* **deps:** Update dependency jsdom to v30 ([70bf61b](https://github.com/jacaudi/tempestwx-utilities/commit/70bf61b63a6bd66ee5376b5fadd31697282b080f))
* **deps:** Update dependency jsdom to v30 ([03f476d](https://github.com/jacaudi/tempestwx-utilities/commit/03f476dea22e32f9e8aacb370483fe8fdd278837))
* **deps:** Update dependency react-dom to v19.2.8 ([24aeb5e](https://github.com/jacaudi/tempestwx-utilities/commit/24aeb5ee27af857c022dc06cf45ca746c3ab3eb1))
* **deps:** Update dependency react-dom to v19.2.8 ([b09572d](https://github.com/jacaudi/tempestwx-utilities/commit/b09572dc869f05728f09ed333e169683bd1adb63))
* **deps:** Update dependency typescript-eslint to v8.66.0 ([416fe91](https://github.com/jacaudi/tempestwx-utilities/commit/416fe91f2f4d1c6d5d15acd2eb90b8515787d18d))
* **deps:** Update dependency typescript-eslint to v8.66.0 ([5b43b89](https://github.com/jacaudi/tempestwx-utilities/commit/5b43b89547358e7522df17dfec7744a70ab0dc9b))
* **deps:** Update dependency vite to v7.3.6 ([9b2b579](https://github.com/jacaudi/tempestwx-utilities/commit/9b2b579674591aa54c9001b96157ca1dd4baf096))
* **deps:** Update dependency vite to v7.3.6 ([72ecd68](https://github.com/jacaudi/tempestwx-utilities/commit/72ecd6880ee03a05f4b1ad39398fe42c6dd6c06e))
* **deps:** Update eslint monorepo to v9.39.5 ([0ac8e37](https://github.com/jacaudi/tempestwx-utilities/commit/0ac8e373872a6d38ea3b719377548bf0abd067db))
* **deps:** Update eslint monorepo to v9.39.5 ([324839a](https://github.com/jacaudi/tempestwx-utilities/commit/324839a864e75bf22649db00d0ea0ba124dc32c6))
* **deps:** Update grafana/grafana Docker tag to v11.6.16 ([5b4718f](https://github.com/jacaudi/tempestwx-utilities/commit/5b4718fb1559ab4e659829a4cd3d6eab15e9a31b))
* **deps:** Update grafana/grafana Docker tag to v11.6.16 ([cc6cd68](https://github.com/jacaudi/tempestwx-utilities/commit/cc6cd684edf5a3e1ae509a3f8a00a86161b15d01))
* **deps:** Update litestream/litestream Docker tag to v0.5.15 ([7346a48](https://github.com/jacaudi/tempestwx-utilities/commit/7346a482403791d26505308b12454313b7ec5f49))
* **deps:** Update litestream/litestream Docker tag to v0.5.15 ([cbfc7ce](https://github.com/jacaudi/tempestwx-utilities/commit/cbfc7cebe4fb15608f562c46545a5cb318e231b6))
* **deps:** Update module modernc.org/sqlite to v1.56.0 ([977c8d3](https://github.com/jacaudi/tempestwx-utilities/commit/977c8d3250cfd4791b34461fbd2e66b4db9de39c))
* **deps:** Update module modernc.org/sqlite to v1.56.0 ([f9b0da7](https://github.com/jacaudi/tempestwx-utilities/commit/f9b0da7cc9483426640619d8882f31414531153d))
* **deps:** Update Node.js to v24 ([98ecb48](https://github.com/jacaudi/tempestwx-utilities/commit/98ecb48b52b60b5cedcb8a36022ac634a602a738))
* **deps:** Update Node.js to v24 ([3f689c5](https://github.com/jacaudi/tempestwx-utilities/commit/3f689c5e5ec0f0d1833b7b2064a47ccc0f842ae6))
* **deps:** Update otel/opentelemetry-collector-contrib Docker tag to v0.158.0 ([1359a88](https://github.com/jacaudi/tempestwx-utilities/commit/1359a883bdee497d464f132a2c75d0b27bdc013d))
* **deps:** Update otel/opentelemetry-collector-contrib Docker tag to v0.158.0 ([06d74b6](https://github.com/jacaudi/tempestwx-utilities/commit/06d74b64350bb3325b5856e40acd27187ffdff2f))
* **deps:** Update postgres Docker tag to v18 ([a714cc0](https://github.com/jacaudi/tempestwx-utilities/commit/a714cc00c9d0331bcf73ec7988b7e4f6eedaf47c))
* **deps:** Update postgres Docker tag to v18 ([8cc26b1](https://github.com/jacaudi/tempestwx-utilities/commit/8cc26b1c1b43155b14358552aeb6aae2cf74ff5d))
* **deps:** Update prom/prometheus Docker tag to v3.13.2 ([55da9d4](https://github.com/jacaudi/tempestwx-utilities/commit/55da9d46e491a22003cc28edc615dfff5db2de17))
* **deps:** Update prom/prometheus Docker tag to v3.13.2 ([d4a2d77](https://github.com/jacaudi/tempestwx-utilities/commit/d4a2d7773fd2e713d94659fe4d459acbee87091c))
* **deps:** Update react monorepo ([78b6555](https://github.com/jacaudi/tempestwx-utilities/commit/78b65557a669e8df5bc99b4c02d1c1ebb6127dec))
* **deps:** Update react monorepo ([c1cb599](https://github.com/jacaudi/tempestwx-utilities/commit/c1cb599caac3eabef47ee5a3bafa6adbff93c112))
* drop the workflows' dead composite actions ([9185ebc](https://github.com/jacaudi/tempestwx-utilities/commit/9185ebc565c1126c7a6ec699e639b7e4b3161de5))
* drop the workflows' dead composite actions ([08353e4](https://github.com/jacaudi/tempestwx-utilities/commit/08353e48652eba5cbb6707ecb013832a3d7fd01e))

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
