# Design: a tokenless UDP-mode appliance — station identity, local almanac

**Date:** 2026-08-13
**Status:** Design — Gate 1 returned and addressed (§15); all questions researched and resolved (§14). No open questions.
**Issues:** closes #62 (won't-do), resolves #61, hands the forecast to #81
**Supersedes:** platform design `2026-07-17-unified-weather-platform-design.md` §11 decision **B3**
**Builds on:** `2026-08-10-optional-card-gating-design.md` (issue #145 capability gating)

> **A note on this document's filename.** It was named while the work was still
> framed as "WeatherFlow token source + WeatherFlow→Contract-C shaping". The
> design that emerged inverts that framing: there is no token, and nothing is
> shaped from WeatherFlow. The path is kept for continuity with the handoff and
> the two issue references; the title above is the accurate one. The
> implementation plan will share this feature name per `plan-workflow.md`, so it
> inherits the same caveat.

---

## 1. Summary

The embedded UI runs only in UDP listener mode. Issue #62 asked where a
WeatherFlow credential should come from in that mode. The answer settled with
the repository owner is that **it should not come from anywhere** — in UDP mode
the appliance's data comes from UDP packets and the store they feed, and after
this work the process holds no code path capable of using a WeatherFlow
credential at all.

That reframes #61. It is not a WeatherFlow→Contract-C mapping layer; it is
serving the existing Contract-C types from local and computed sources:

| Endpoint | Before | After |
|---|---|---|
| `GET /api/station` | raw passthrough of WeatherFlow `/stations` | config only |
| `GET /api/almanac` | raw passthrough of WeatherFlow `/better_forecast` | store aggregates + computed astronomy |
| `GET /api/forecast` | raw passthrough of WeatherFlow `/better_forecast` | unregistered until #81 |

A 7-day forecast is the one thing that genuinely cannot come from UDP or the
store. Issue **#81** already scopes a tokenless replacement (NWS `api.weather.gov`,
US-only, `User-Agent` only, points → gridpoint → forecast) and carries its own
unresolved design questions — caching, rate limits, replaces-vs-supplements,
gridpoint resolution. It stays a separate design.

### Non-goals

- Implementing the NWS forecast provider (#81).
- Introducing a `ForecastProvider` interface. With zero providers built, an
  interface would be a wall built early — `patchbay-principle.md`'s seam test
  fails it. #81 introduces the abstraction alongside its first implementor.
- Adding Postgres parity for the read endpoints (see §11).
- Re-litigating the #145 capability-gating mechanism.

---

## 2. Verification log

Everything load-bearing below was checked first-hand. This section exists
because the recurring failure mode on this project is inheriting a claim from a
handoff or an issue without checking it (`feedback-verify-before-asserting`).

Gate 1 independently re-checked every `file:line` citation in the "Confirmed"
table and found each exact. It could **not** reproduce the WeatherFlow evidence,
because the original write-up cited no sources; §2.2 now carries the URLs.

### 2.1 Confirmed in-tree

| Claim | Evidence |
|---|---|
| `App.tsx` wraps the whole card grid in **one** `ErrorBoundary`; `Header` is outside it; Radar has its own nested boundary | `web/src/App.tsx:78–106`, `:70–76`, `:97–102` |
| `TOKEN` selects the mode **and** gates store wiring | `main.go:565–588`; line 566's `token == ""` gates `configurePrometheusWriters` and `configureSQLiteWriter`, not just `resolveModeAndValidate` |
| `startAPIServer` returns nil unless `ModeUDP`, so a set `TOKEN` means no UI at all | `main.go:468–471` |
| `startAPIServer` runs **before** the UDP listener | `main.go:581` vs `:587`; it already uses `log.Fatal` for config errors at `:481, :489, :493` |
| `log.Fatal` skips the deferred `cleanupResources` | `main.go:556–558`; `log.Fatal` → `os.Exit(1)`, which does not run defers |
| The compose deployment restarts on exit | `deploy/docker-compose.yml` — `restart: unless-stopped` on `tempestwx` |
| `ForecastStrip` **throws** on a raw WeatherFlow envelope | Rendered it in a throwaway vitest probe: `forecast.map is not a function` (`ForecastStrip.tsx:35`) |
| `AlmanacCard` **does not throw** on a raw envelope | Same probe. Renders `Invalid Date`, `NaN% illuminated`, `NaNh NaNm daylight`, `NaN°C` ×8. Degraded, not fatal |
| No UDP message carries lat/lon/elevation/name/timezone/`station_id` | `internal/tempestudp/report.go` (all six types) and the published UDP protocol spec v171 (§2.2). The complete station metadata on the wire is `serial_number`, `hub_sn`, `firmware_revision` |
| `firmware_revision` has inconsistent types across UDP messages | `string` in `HubStatusReport` (`report.go:284`), `int` in `DeviceStatusReport` (`report.go:244`) and `TempestObservationReport` (`:163`) |
| `SummarizeObservations(ctx, from, to)` already aggregates temp min/max over an **arbitrary** window | `internal/sqlite/writer.go:993`. The `{7,30,180,365}` restriction lives only in the HTTP handler (`observations.go:250–252`) |
| `sqlite.Summary` has **no argmax** — no timestamp for when an extreme occurred | `internal/sqlite/writer.go:958–986` |
| `/api/observations/summary` has a 5s query timeout | `observations.go:135` (`summaryQueryTimeout`), applied at `:259–260` |
| `temp_air` is **nullable** | `internal/sqlite/migrations/0002_init.sql`; `weather.Observation.TempAir` is `*float64` (`internal/weather/observation.go:38`) and `internal/sqlite/backfill.go:174` inserts it directly |
| `sqlite.Observation` carries `SerialNumber` | `internal/sqlite/writer.go:737` |
| The UI reads exactly four `StationMeta` fields | `Header.tsx:30,34,37` (`name`, `elevation`) and `hasCoordinates`/`RadarCard.tsx:154,240,251` (`latitude`, `longitude`). `station_id`, `device_id`, `timezone`, `firmware_revision`, `serial_number` are declared and never read outside test fixtures |
| `Header.tsx:30`'s fallback is `??`, which does **not** fire on `""` | `{station?.name ?? 'Tempest Station'}` |
| `hasCoordinates` accepts `0` | `formatCoord.ts:19–26` — `typeof === 'number'` && `Number.isFinite`, both true for `0` |
| `RadarCard` already accepts an optional `site` prop | `RadarCard.tsx:47`, defaulting to the `not-configured` state at `:142` |
| `RADAR_SITE` is documented but read by nothing | `grep -rn RADAR_SITE --include='*.go' .` → no matches. Documented in `deploy/.env.example` and `deploy/docker-compose.yml`, the latter shipping it **empty by default** (`RADAR_SITE: ${RADAR_SITE:-}`) |
| There is no timezone handling anywhere in the Go code | No `time.Local`, no `LoadLocation`, no `TZ` in any non-test file |
| `/api/observations/summary` uses **rolling** windows, not calendar-aligned | `observations.go:257` — `from := to - int64(days)*86400` |
| Postgres-only leaves `deps.Observations` nil | `main.go:148–155` (`selectStore`) → `:347–349` → `:476–478` |
| The runtime image **carries tzdata** | `skopeo` inspection of the pinned `cgr.dev/chainguard/static@sha256:24dd7f…`: `etc/apk/world` contains `tzdata=2026c-r0`, with 1242 files under `usr/share/zoneinfo/` including `UTC`, `America/New_York`, `America/Denver`, `Europe/London` |
| `time/tzdata` costs ~450 KB | `go doc time/tzdata` |
| The repo has **19** direct dependencies (31 indirect) | `go mod edit -json` |
| Deleting the WeatherFlow proxy does not break `backfill` or API-export | `Client.Proxy`/`scrubTransportError` have no caller outside `internal/tempestapi/proxy.go` + its test; `ListDevices`, `ListStations`, `GetObservations`, `Observations` are untouched |
| SQLite's bare-column-with-aggregate pairs correctly for a **single** aggregate but not two | Empirical, SQLite 3.51: `SELECT MAX(t),MIN(t),ts FROM …` paired `ts` with the **minimum**; `SELECT MAX(t),ts` paired correctly |
| NOAA solar accuracy is "within a minute for locations between +/− 72° latitude" | `gml.noaa.gov/grad/solcalc/calcdetails.html`, verbatim |
| `AlmanacCard`'s waxing branch keys on `phase <= 0.5` | `AlmanacCard.tsx:19` |
| Four web test files construct `StationMeta` literals | `App.test.tsx:77`, `Header.test.tsx:100`, `RadarCard.test.tsx:84`, `useWeatherData.test.ts:54` |
| Two `AlmanacCard` test lines do arithmetic on `sunrise`/`sunset` | `AlmanacCard.test.tsx:28,32` — `new Date(almanac.sunrise * 1000)` |

### 2.2 Confirmed against external sources

Recorded payloads and published specs, with URLs so a reader can reproduce.
None of this is consumed by the shipped code after this change; it is cited
because §12 instructs correcting the platform design on its strength.

| Claim | Source |
|---|---|
| WeatherFlow's `/stations` array key is `stations`, not `locations` | `raw.githubusercontent.com/jeeftor/weatherflow4py/master/tests/fixtures/rest/stations/stations.json`; independently `raw.githubusercontent.com/gregertw/python-extractor-tempest/master/tempest_extractor/tempest_client.py` and `raw.githubusercontent.com/peted-davis/WeatherFlow_PiConsole/main/lib/config.py`. Matches `internal/tempestapi/client.go:122` |
| The published swagger is **stale** — declares `StationSet.locations`, omits `Station.timezone` | `weatherflow.github.io/Tempest/api/swagger/swagger.json` |
| `better_forecast` returns **no moon data** | Six recorded payloads across four independent lineages, 2021 → March 2026: `jeeftor/weatherflow4py` (= Home Assistant core, byte-identical — one lineage, not two), `tidbyt/community`, `rstanbaugh/tempest-cli`, `MagicMirrorOrg/MagicMirror`. The substring `moon` appears in none |
| Corroboration that moon must be computed | `raw.githubusercontent.com/nickdnj/TempestWeather/main/overlay/astronomy_client.py` — displays moon phase from a `better_forecast` app, computing it locally: *"this API doesn't provide moon data directly, so we'll calculate it"* |
| `better_forecast.forecast.daily[]` carries `sunrise`/`sunset` | 54/54 daily entries across all six captures, unix epoch integers |
| UDP protocol spec v171 lists no location fields | `weatherflow.github.io/Tempest/api/udp/v171/` |

### 2.3 Corrections to inherited claims

1. **Issue #61's blast-radius comment is half right.** It states that both
   `ForecastStrip`'s `.map` and `AlmanacCard`'s property access "will throw".
   Only `ForecastStrip` throws. This matters: `ENABLE_FORECAST` is what would
   kill the dashboard, `ENABLE_ALMANAC` alone only renders nonsense.
2. **Platform design §11:496's "moon" is unsupported.** It records
   `/api/almanac` as supplying "Sunrise/sunset/**moon**". Precisely stated: no
   moon field is returned by `better_forecast` by default in any of six
   captures spanning 2021–2026, and none is documented. That is not the same as
   proving no parameter could ever surface one (§2.4), and §12's correction to
   that document must use this wording rather than a flat "factually wrong".
   Sunrise/sunset in that row *are* correct — they exist in
   `forecast.daily[]` (§2.2); it is only "moon" that has no basis.
3. **The handoff's `web/src/components/__fixtures__/` does not exist.** The only
   fixture directory is `web/src/types/__fixtures__/capabilities.json`.

### 2.4 Not verified — and why each is now moot

Three WeatherFlow claims could not be verified to the standard the rest of §2
meets. **All three are closed by the design rather than by further research:
after §8, this process consumes no WeatherFlow response field in UDP mode, so
the precise shape of those responses cannot affect the shipped code.** They are
retained only because §12 corrects the platform design on their strength, and a
reader deserves to know the limit of that evidence.

| Unverified | Why it cannot affect this design |
|---|---|
| Whether an undocumented `better_forecast` parameter unlocks moon data. The evidence supports "not returned by default" across six captures; it cannot prove no such parameter exists | Moon phase is computed (§7) regardless of what the API could return |
| Nullability of WeatherFlow fields — key *presence* was enumerated, not whether values can be `null` | No WeatherFlow field is decoded **in a UDP-mode code path** after §8. `ListDevices`, `ListStations` and `GetObservations` survive and still decode WeatherFlow, but they serve `backfill` and API-export, which this design does not touch — and their decode is separately proven (commit `6e8deb6` hardened `GetObservations`' envelope check) |
| Whether the recorded `/stations` lineage reflects all account tiers — one captured payload plus two independent client implementations | `/api/station` is served from config; `/stations` is not called by the UI path. `internal/tempestapi`'s own decode is unchanged and already proven by `backfill` |

Nothing else in this document rests on an unverified premise. §7's astronomy
accuracy, previously listed here, is resolved in §7.1 with pinned algorithms
and authoritative test vectors.

---

## 3. Architecture

```
UDP broadcasts ──► sink ──► SQLite store ──┬──► /api/observations/current
                                            ├──► /api/observations/summary
                                            └──► /api/almanac ──┐
                                                                 │
env config ──► StationConfig ──┬──► /api/station                 │
                                ├──► internal/astro ─────────────┘
                                └──► RADAR_SITE ──► /api/station ──► RadarCard

(no outbound WeatherFlow call exists in this process in UDP mode)
```

Concretely the change adds: a `config.StationConfig` struct and loader; a
`Deps.Station` field on the `httpserver` seam; two handler registrations
(`registerStation`, `registerAlmanac`) replacing the deleted `registerProxy`;
one store method; one pure-computation package. It deletes the WeatherFlow
proxy. It introduces no interface and no new module dependency.

---

## 4. Station identity config

The store holds no coordinates and UDP carries none (§2.1). Station identity is
therefore configuration — 12-Factor III. It is a single contract with three
consumers: the almanac's sunrise/sunset today, and NWS gridpoint resolution
(#81) and radar site selection later. Single-sourcing it now is `patchbay`'s
"single-source the contract variants must share": the next consumer is a
reader, not a rewrite.

| Var | Type | Needed for | Absent ⇒ |
|---|---|---|---|
| `STATION_LATITUDE` | float, −90..90 | almanac, radar | those features degrade (§9) |
| `STATION_LONGITUDE` | float, −180..180 | almanac, radar | those features degrade (§9) |
| `RADAR_SITE` | string (WSR-88D code, e.g. `TLX`) | radar | radar degrades (§9) |
| `STATION_ELEVATION` | float, metres | UI display | field omitted; UI renders `—` |
| `STATION_NAME` | string | UI display | field omitted; UI renders `Tempest Station` |
| `STATION_TIMEZONE` | IANA name | almanac windows | defaults to `UTC` |

**No value here is ever required**, and no combination of them can prevent the
process from starting — see §9. A *malformed* value (unparseable float,
out-of-range latitude, unknown timezone) is still a fatal configuration error,
matching `ParseBoolEnv`'s existing stance that a typo is fatal rather than a
silently disabled feature. The distinction is deliberate: absent means "the
operator did not configure this feature"; malformed means "the operator tried
and got it wrong".

**Naming — `STATION_*`, resolved** (§14 Q1). The repo's 31 environment
variables group by *subsystem* prefix: `POSTGRES_*` (10), `ENABLE_*` (7),
`SQLITE_*` (4), `PROMETHEUS_*` (2), `RADAR_*` (1), `OTEL_*` (1), plus six
singletons (`TOKEN`, `JOB_NAME`, `HTTP_ADDR`, `LOG_UDP`, `KEEP_EXPORT_FILES`,
`TEMPEST_SERIAL`) — 31 in total. Six new site-identity variables are a
subsystem and take a subsystem prefix.

The apparent conflict with `TEMPEST_SERIAL` — which `CLAUDE.md` describes as
"process-level station identity" — dissolves on inspection. It is read at
exactly one place, `main.go:405`, **inside the `ENABLE_OTEL` branch only**, and
its sole use is populating `otel.Config.Serial` → the `tempest.serial` OTel
resource attribute. Its prefix mirrors that attribute's namespace; it is not a
general station-identity prefix, and nothing else reads it. The new variables
are not OTel attributes, so they do not inherit it.

`TEMPEST_SERIAL` is deliberately **not** renamed: it is a documented public
config surface, renaming it is a breaking change, and it has no present defect.

### 4.1 Loader placement and error aggregation

```go
type StationConfig struct {
    Name      *string
    Latitude  *float64
    Longitude *float64
    Elevation *float64
    RadarSite *string
    Location  *time.Location // never serialised; never nil -- defaults to time.UTC
}

func LoadStation() (StationConfig, error)
```

Pointers on the serialised fields, because `omitempty` on a `float64` cannot
express "unset": sea level (elevation 0), the equator (latitude 0) and the prime
meridian (longitude 0) are all legitimate values that a value-typed field with
`omitempty` would silently drop.

`Location` is a pointer for an unrelated and purely idiomatic reason — Go's
`time.LoadLocation` returns `*time.Location` and `time.UTC` *is* one, so there
is no value form to use. It is never nil and never serialised; the "unset"
rationale above does not apply to it.

**`RadarSite` is populated only when `ENABLE_RADAR` is true.** `LoadStation`
does not read feature flags, so `main()` reads `ENABLE_RADAR` (as it already
does at `main.go:479`) and clears `RadarSite` when the flag is false, before
handing the struct to `httpserver.Deps`. That keeps flag interpretation in one
place and leaves `LoadStation` a pure environment decoder.

**Called at the top of `main()`, before any backing service is attached** —
before `configureSQLiteWriter` (`main.go:568`), `configurePostgresWriter`
(`:572`) and OTel setup (`:575`). Validating after resources are open means a
malformed value produces a partial startup that then exits past
`cleanupResources`. `go-standards.md` §15.3 and 12-Factor III both put
validation at the boundary, ahead of resource attachment.

**Reports every malformed value in one error**, not the first. Per
`go-standards.md` §15.3 — "report all missing secrets in one error so operators
fix everything in one deploy cycle." With six variables, one-at-a-time failure
is six restart cycles.

**Timezone.** `time.LoadLocation` works in the shipped image — Gate 1 verified
tzdata is present in the pinned base (§2.1). The implementation nevertheless
imports `time/tzdata` (one blank import, ~450 KB) so a Renovate bump of the base
digest cannot silently turn a non-UTC `STATION_TIMEZONE` into a startup failure.
Cheap insurance against a dependency the build does not control.

`STATION_TIMEZONE` is **server-side only** — it never appears on the wire,
because its only consumer is §6.2's calendar arithmetic and the UI has its own
locale.

---

## 5. `GET /api/station`

Served entirely from config. Registered unconditionally, as today.

```json
{ "name": "Backyard", "latitude": 40.1234, "longitude": -75.9876,
  "elevation": 118.3, "radarSite": "TLX" }
```

### 5.1 Omission semantics — every field

**Every field is omitted when its source is unset.** There is no zero-value
fallback on the wire, in either direction.

| Field | Emitted when | Omitted ⇒ UI behaviour |
|---|---|---|
| `name` | `STATION_NAME` set and non-empty | `Header.tsx:30`'s `?? 'Tempest Station'` fires |
| `latitude`, `longitude` | **both** set and valid | `hasCoordinates` returns false; no location line, no radar mount |
| `elevation` | `STATION_ELEVATION` set | `Header.tsx:37`'s `?? '—'` fires |
| `radarSite` | `ENABLE_RADAR` true **and** `RADAR_SITE` set | `RadarCard` never mounts (§9) |

This is load-bearing, not pedantry. Emitting Go zero values instead would render
a blank `<h1>` (because `??` does not fire on `""`) and place every default
deployment at `0.0000°N, 0.0000°E` (because `hasCoordinates` accepts `0` as a
finite number) — reintroducing from the other side exactly the failure
`formatCoord.ts:13` was written to prevent.

`latitude` and `longitude` are emitted **as a pair or not at all**. A half-set
coordinate is a malformed configuration, not a partial one.

### 5.2 Trimming `StationMeta`

`StationMeta` declares nine fields; the UI reads four (§2.1). `station_id`,
`device_id`, `timezone`, `firmware_revision` and `serial_number` are removed
from both the wire shape and `web/src/types/weather.ts`. Two are
WeatherFlow-specific concepts with no tokenless source; the other three have
sources but no consumer. Keeping them means inventing values for fields nothing
reads.

**Is this too aggressive given #81 and radar? No — resolved** (§14 Q4). Both
future consumers were checked rather than assumed:

- **#81 / NWS** needs *latitude and longitude only*. `api.weather.gov`'s
  `/points/{latitude},{longitude}` takes no station identifier, no account and
  no API key; the only other requirement is a `User-Agent` header
  (`weather.gov/documentation/services-web-api`). It has no use for
  `station_id`.
- **Radar** needs a WSR-88D site code (now `RADAR_SITE`, §4) plus lat/lon for
  the map. `RadarCard.tsx:154,240,251` reads only `latitude`/`longitude`.

So no identified future consumer wants any of the five. And the risk is
asymmetric in the safe direction: re-adding a field later is additive — a new
config value, a new wire field, siblings untouched — whereas carrying five
fabricated values now means every consumer must learn which are real.

> **Adjacent observation, deliberately out of scope.** `StationHealth.tsx:84`
> renders `status.firmwareVersion`, which `fetchStationStatus` hardcodes to
> `''` — a permanently blank field in the UI today. UDP *does* carry
> `firmware_revision`; the store simply does not persist it. That is a
> pre-existing gap on `StationStatus`, not `StationMeta`, and fixing it needs a
> schema change. Noted so a reader does not mistake it for fallout from this
> trim.

In `weather.ts` the survivors become **optional** (`name?`, `latitude?`,
`longitude?`, `elevation?`, `radarSite?`) to match §5.1. Declaring them required
while the server omits them is the type lying about the wire.

`radarSite` goes here rather than on `/api/capabilities` because #145
established capabilities as a pure boolean feature document, and a site code is
station identity.

---

## 6. `GET /api/almanac`

Gated by `ENABLE_ALMANAC` (unchanged from #145) and by coordinate availability
(§9). **`StationAlmanac`'s shape is unchanged except for the nullability in
§6.4 and §6.7, so `AlmanacCard`'s layout, its moon SVG and its four record
columns are untouched.** Two of its helpers do change, and only those:
`formatTime` disappears (the server now sends preformatted clock times, §6.3)
and `daylightDuration` becomes an integer split rather than an epoch
subtraction.

### 6.1 Provenance

| Field | Source |
|---|---|
| `today`/`week`/`month`/`year` — all four of `.high`, `.low`, `.highDate`, `.lowDate` | store, one `TemperatureExtremes` call per window (§6.5) |
| `sunrise`, `sunset` | computed from lat/lon (§7) |
| `moonPhase`, `moonPhaseName`, `moonIllumination` | computed (§7) |

### 6.2 Windows

Calendar-aligned in `STATION_TIMEZONE`, each ending at the instant of the
request:

| Record | Window start |
|---|---|
| `today` | local midnight of the current local date |
| `week` | local midnight of the current local date's **week-to-date** start, i.e. the current day if it is a Sunday, else the preceding Sunday |
| `month` | local midnight of the 1st of the current local month |
| `year` | local midnight of 1 January of the current local year |

This deliberately differs from `/api/observations/summary`'s rolling windows.
The labels the card renders — "Today", "This Week", "This Month", "This Year" —
read as calendar periods, and unlike `RecordsCard` this endpoint has a timezone.

**Why Sunday and not ISO-8601 Monday** (resolved, §14 Q2). The honest argument
is a narrow one: **this appliance's operator is US-based, US convention is
Sunday, and switching to Monday later is a one-line change plus a config var** —
cheap to defer, exactly what YAGNI's cost-asymmetry guardrail is for.

> A previous revision argued that "the only features needing coordinates are
> US-only (WSR-88D, NWS)". **That is false and is withdrawn** — §4 lists the
> *almanac* as a coordinate consumer, it is the only one this design actually
> ships, and it is not US-specific at all: A.3 deliberately tests London,
> Sydney, Quito, Singapore and Longyearbyen, and §4 ships an IANA
> `STATION_TIMEZONE`. ISO 8601 genuinely specifies Monday, and for an
> internationalised product it would win. The decision stands on operator
> preference and reversibility, not on a claim about the feature set.

> An earlier draft justified Sunday by pointing at `ForecastStrip.tsx:14`'s
> `DAY_NAMES = ['Sun', 'Mon', …]`. **That reasoning was invalid** and is
> withdrawn: the array is indexed by `date.getDay()`, which returns `0` for
> Sunday per the ECMAScript spec, so its ordering is forced by the language
> and says nothing about week-start convention.

**Three consequences the implementation must handle rather than discover:**

- **`week` can exceed `month` and `year`.** For up to six days of every month,
  and every year not beginning on a Sunday, the week window starts before the
  month or year window — so the card can legitimately show a higher "This Week"
  high than "This Month". This is correct for a calendar week and must not be
  "fixed" by clamping.
- **When today *is* Sunday, `week` means week-to-date** (starting today), not
  the preceding seven days. Spelled out above because the phrase "most recent
  Sunday" reads both ways.
- **Local midnight does not always exist.** Santiago, Beirut, Havana and
  historical Brazil transition DST at midnight; `time.Date(y, m, d, 0,0,0,0, loc)`
  on such a date returns an unspecified nearby instant per Go's own
  documentation. Acceptable for a window start, but pinned by a test rather than
  left to chance.

**`RecordsCard` and `AlmanacCard` render simultaneously** and will disagree —
a rolling 7-day high beside a calendar week-to-date high. That is intended
(different questions), but it is visible and worth a code comment so a future
reader does not "fix" it.

### 6.3 Sunrise and sunset are preformatted, for the same reason as §6.7

`AlmanacCard.tsx:56–61` currently renders these epochs with

```ts
new Date(epoch * 1000).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
```

— **no `timeZone` option**, so they render in the *viewer's* zone. That is
precisely the defect §6.7 resolves for `highDate`: the Colorado station checked
from a phone in Tokyo shows "Sunrise 8:47 PM". §5.2 removes `timezone` from the
wire, which also forecloses the client-side fix. An earlier revision of this
document resolved the principle for the date labels and left this field
inconsistent with it.

So `StationAlmanac` changes shape here:

| Field | Was | Becomes |
|---|---|---|
| `sunrise` | `number` (epoch) | `string \| null` — preformatted station-local clock time, e.g. `"5:47 AM"` |
| `sunset` | `number` (epoch) | `string \| null` — same |
| *(new)* `daylightMinutes` | — | `number \| null` — sunset − sunrise, in whole minutes |

Preformatting removes the epochs entirely rather than shipping both, so there
is no timezone-naive value left for a future edit to render wrongly.
`daylightMinutes` carries the one genuinely timezone-independent quantity the
card needs, so `daylightDuration` becomes a pure `h`/`m` split of an integer
instead of an epoch subtraction.

### 6.4 Polar edge case

Above roughly 66.5° latitude the sun does not rise or set on some dates.
`sunrise`/`sunset` become `number | null` in `weather.ts`; `AlmanacCard` renders
`—` for a null and suppresses the daylight-duration line. This is boundary
correctness, not speculative generality: §2.1's probe already demonstrated that
the current code renders `Invalid Date` and `NaNh NaNm daylight` when handed a
bad value.

**The pair can be half-populated, and that is the correct answer** — the UI must
handle a null sunrise or a null sunset independently.

> **This reverses a claim an earlier revision of this document made.** That
> revision asserted the two are "always null together, never one alone",
> reasoning that a single `cosH` governs both events. That is true of A.1
> **step 12**, but **step 14 refines each event at its own JD**, so the second
> pass computes a *different* `cosH` per event, and near the polar boundary they
> disagree. Measured at Utqiaġvik on 2026-05-10: pass-2 `cosH` is −0.9894 for
> sunrise (in range) and −1.0033 for sunset (midnight sun), yielding a real
> sunrise and no sunset. **USNO agrees** — `Rise 10:58`, no set, and the
> following day is "continuously above the Horizon". The sun genuinely rises
> that morning and does not set again until August. Forcing `nil, nil` would
> discard a real sunrise for roughly two weeks a year at 66–72° latitude.

`AlmanacCard` therefore renders `—` for whichever bound is null and suppresses
the daylight-duration line when **either** is — which is what §10 already
specified.

Accepted consequence: during a fully dark or fully lit day the card shows `—`
for both without saying which, even though the arithmetic knows (`cosH > 1` vs
`cosH < −1`). Surfacing "Midnight sun" versus "Polar night" needs a third
return value and a UI string, and no present requirement asks for it (§14.1).

### 6.5 New store method

`sqlite.Summary` has no argmax (§2.1), so `highDate`/`lowDate` have no source.

```go
// TemperatureExtremes returns the max and min air temperature in [from, to]
// with the timestamp at which each occurred.
//
// Max/Min are invalid when the window contains no row with a non-NULL
// temp_air -- which includes both an empty window and a window whose every
// temp_air is NULL. A value and its timestamp are always valid together or
// invalid together; the query cannot produce a mismatched pair.
//
// Ties resolve to the EARLIEST occurrence.
func (w *Writer) TemperatureExtremes(ctx context.Context, from, to int64) (TempExtremes, error)

type TempExtremes struct {
    Max   sql.NullFloat64
    MaxAt sql.NullInt64
    Min   sql.NullFloat64
    MinAt sql.NullInt64
}
```

**`temp_air` is nullable and NULLs are reachable** (§2.1), and the tie-break
must be deterministic. Two forms fail, both for reasons that are easy to miss:

```sql
-- WRONG 1: NULLs sort first ascending, so this returns the NULL row as the
-- minimum and Min/MinAt disagree. (SQLite 3.51, §2.1.)
SELECT timestamp, temp_air FROM tempest_observations
 WHERE timestamp BETWEEN ? AND ? ORDER BY temp_air ASC LIMIT 1

-- WRONG 2: the ORDER BY is a NO-OP. An aggregate with no GROUP BY returns
-- exactly one row, so there is nothing to sort, and SQLite documents the bare
-- `timestamp` as arbitrary among tied rows: "The choice is arbitrary. There is
-- no way to predict from which row the bare values will be choosen."
-- (sqlite.org/lang_select.html §2.5.) Measured on 3.51 with rows tied at 25.0
-- on timestamps 100/200/300, it returns 300 -- the LATEST -- under both
-- ORDER BY ASC and DESC.
SELECT max(temp_air), timestamp FROM tempest_observations
 WHERE timestamp BETWEEN ? AND ? AND temp_air IS NOT NULL
 ORDER BY timestamp ASC
```

The correct form filters NULLs explicitly — which removes the first objection —
and orders real rows, where `ORDER BY` does apply:

```sql
-- max; the min variant is identical with ORDER BY temp_air ASC
SELECT temp_air, timestamp FROM tempest_observations
 WHERE timestamp BETWEEN ? AND ? AND temp_air IS NOT NULL
 ORDER BY temp_air DESC, timestamp ASC
 LIMIT 1
```

`temp_air ASC|DESC` selects the extreme, `timestamp ASC` breaks ties toward the
**earliest** occurrence as §6.5's contract promises, and the `IS NOT NULL`
filter means an empty *or* all-NULL window returns **zero rows** — so the value
and its timestamp are invalid together and can never be a mismatched pair.

Two statements are still required, one per extreme.

**Kept separate from `Summary`** rather than bolted onto it. "What was the
extreme" and "when did it occur" are different knowledge with different
consumers — `/api/observations/summary` wants the first and not the second, and
would otherwise ignore four new columns on every call. Two statements are
required either way.

Added to the `ObservationReader` interface in `internal/httpserver/observations.go`.

### 6.6 Query timeout

The handler makes four `TemperatureExtremes` calls, each two statements — up to
eight scans per request. `idx_obs_time` leads on `timestamp` and cannot serve a
`max(temp_air)`, so the year window (~525k rows at a 1/minute cadence) is a
scan. `refresh()` (`useWeatherData.ts:316–323`) reaches this endpoint on every
Refresh press.

The handler therefore wraps its queries in a context timeout, reusing the
constant and the reasoning `handleSummary` already established at
`observations.go:135, :259–260`. Dropping that convention for an endpoint that
scans eight times as much would be a regression.

### 6.7 Empty store and date labels

A window with no qualifying rows yields nulls. `TempRecord.high`/`low` become
`number | null` and `AlmanacCard` renders `—`, reusing §6.4's null handling. A
freshly provisioned appliance has no year of history and must not render
`NaN°C` — which §2.1's probe confirms is what it does today.

`TempRecord.highDate`/`lowDate` stay `string`, formatted **server-side**:
`"Today"` when the extreme falls on the local current date, otherwise `"Mon D"`
(e.g. `"Feb 15"`). They are `null` when the corresponding value is.

**Why not an epoch the browser formats — resolved** (§14 Q3). The decisive
argument is not convenience, it is correctness: **the browser's timezone is the
viewer's, not the station's.** Someone checking their Colorado station from a
phone in Tokyo would see an epoch rendered against JST — labelling the July 4th
high as "Jul 5", and breaking the "Today" test outright. Only the server knows
`STATION_TIMEZONE`.

The alternative that preserves correctness — send the epoch *and* the station
timezone, and format in the browser — means putting `timezone` back on
`/api/station` for one label, and reimplementing the "is this the station's
today" comparison client-side. That is more surface for a strictly worse
outcome.

Accepted cost: month abbreviations exist in both languages
(`ForecastStrip.tsx:12` holds a `MONTH_NAMES` array). Different runtimes, no
shared build step, and the DRY criterion does not fire — a change to one would
not require a change to the other to stay correct, because they describe
different renderings.

By construction the `today` column's labels are always `"Today"`. That is
accepted rather than special-cased — the alternative is a second code path for
one column.

---

## 7. `internal/astro`

Two pure functions, no state, no I/O, no new module dependency:

```go
// SunriseSunset returns the sunrise/sunset pair bracketing solar noon on the
// calendar date that t falls on IN t's OWN LOCATION -- callers pass a t already
// in the station's timezone, so "today" means the station's today. lat and lon
// are degrees, with EAST-POSITIVE longitude.
//
// Both results are absolute instants and either may fall on an adjacent UTC
// day. Either may independently be nil where that event does not occur: near
// the polar boundary a day can have a sunrise and no sunset, or vice versa
// (§6.4). Both are nil on a fully dark or fully lit day.
func SunriseSunset(lat, lon float64, t time.Time) (sunrise, sunset *time.Time)

// MoonPhase returns the phase fraction (0 = new, 0.25 = first quarter,
// 0.5 = full), its conventional name, and the illuminated fraction, for the
// instant t.
func MoonPhase(t time.Time) (phase float64, name string, illumination float64)
```

**Hand-rolled rather than a dependency.** The direct-dependency list is 19
entries, all load-bearing infrastructure (pgx, otel, prometheus, sqlite, uuid).
The repo already hand-rolls comparable numerics — `tempestudp.WetBulbTemperatureC`
and the moon-phase SVG geometry the platform design's own review notes record as
correct. Two well-tested pure functions do not warrant a supply-chain addition.

`MoonPhase`'s `phase` feeds `AlmanacCard`'s existing SVG, whose waxing branch
keys on `phase <= 0.5` (`AlmanacCard.tsx:19`). The returned convention matches
by construction, and a test must assert it.

### 7.1 Pinned algorithms

Researched and settled (§14 Q5) so the implementation plan leaves no judgment
call open. Full equation sequences and authoritative test vectors are in
**Appendix A**.

**Sunrise/sunset — NOAA solar-position equations (Meeus-derived).** The
authoritative machine-readable source is the calculator's JavaScript
(`gml.noaa.gov/grad/solcalc/main.js`), *not* `calcdetails.html`, which contains
prose but no equations. Prefer it over NOAA's spreadsheets, which NOAA itself
says are valid only 1901–2099 due to a Julian-Day approximation the web
calculator does not use.

> **NOAA now states the Solar Calculator is "no longer actively supported or
> maintained… we cannot guarantee its accuracy or functionality."** The
> mathematics is unaffected (it is Meeus), but the URL may vanish. **The
> equations must be vendored into the Go source with the derivation
> commented — NOAA must not become a live or documentation-time dependency.**

Four decisions a naive implementation gets wrong, each spelled out in
Appendix A:

1. **The date contract, and which date.** Step 13's arithmetic legitimately
   yields minute offsets below 0 or above 1440, and **must not be clamped**. At
   west longitudes sunset commonly lands on D+1 (Denver: solar noon ≈19:02 UTC);
   at east longitudes sunrise lands on D−1 (Sydney: solar noon ≈01:55 UTC).

   **The day being asked about is the station's local day, not the UTC day.**
   The handler passes `time.Now().In(cfg.Location)`, and A.1 step 1 takes
   `Y, M, D` from that value's **own location**, not from `t.UTC()`. Getting
   this wrong shifts the answer for a third of every day at Denver and half of
   every day at Sydney — only by a minute or two of displayed clock time, since
   rise and set move slowly day to day, but near the polar boundary it flips a
   nil to a non-nil. It also makes §6.7's `"Today"` comparison consistent with
   §6.2's window boundaries, which are already station-local.

   *(A.3's vectors are expressed with UTC dates because USNO publishes them
   that way; a test drives `SunriseSunset` with a `t` whose location makes the
   intended local date, and the appendix names it per row.)*
2. **Longitude is east-positive.** Verified, not assumed: with `lon = −104.98`
   the solar-noon term reproduces USNO's published Denver transit. Some older
   NOAA spreadsheets are west-positive; mixing them silently inverts results.
3. **The polar condition, and why the pair can be half-populated.** `acos` has
   no solution when `cosH ∉ [−1, 1]`. `cosH > 1` is polar night, `cosH < −1` is
   midnight sun. The guard must test `cosH` **before** `math.Acos`; letting it
   return `NaN` and testing `math.IsNaN` works but discards the sign, and with
   it the distinction.

   **The guard runs independently for each event in step 14's refinement pass**,
   which recomputes at that event's own JD. Near the polar boundary the two
   passes can disagree — a real sunrise with no sunset, confirmed against USNO
   (§6.4). Do **not** collapse an asymmetric result to `nil, nil`.
4. **The zenith angle is 90.833°**, being 16′ solar semidiameter + 34′ mean
   horizon refraction = 50′. USNO uses 90.8333 (exactly 90°50′); the ~1–2 second
   difference is far inside tolerance. Match NOAA.

Accuracy, verbatim from NOAA: *"theoretically accurate to within a minute for
locations between +/− 72° latitude, and within 10 minutes outside of those
latitudes."*

A literal transcription measured against **every non-nil rise/set value in
A.3** deviates by **at most 44 s** (mean 15.7 s), the worst being row 15 — one
of the high-latitude polar-boundary rows. At 71.29° that is outside NOAA's own
±1 min claim, which NOAA explicitly scopes to |lat| < 72°, so it is expected
rather than a defect. (Excluding rows 15–17 the worst case is 30 s; an earlier
revision quoted that figure as though it covered the whole table, which it did
not.)

**Test tolerance: ±90 s.** It is an *accuracy* bound, chosen to sit
comfortably above the 44 s worst case plus USNO's whole-minute rounding.

> **It is not a transcription check, and the plan must not treat it as one.**
> Measured term-by-term, dropping `y·sin 2L0` shifts results by 586 s and
> dropping `2e·sin M` by 451 s — both caught — but dropping
> `4ey·sin M·cos 2L0` shifts by only 38.7 s, `0.019993·sin 2M` by 28.5 s, and
> five further terms by ≤10.5 s. **Seven of ten terms could be omitted entirely
> and every vector would still pass.** Catching those needs a separate
> term-presence check, not a tighter tolerance — tightening would instead start
> failing on USNO's rounding.

The same holds for the moon: at ±0.5 pp illumination, dropping `0.214·sin 2M'`
(0.28 pp) or `0.110·sin D` (0.25 pp) passes; at ±0.005 phase, four of six
correction terms can be dropped undetected.

**Moon phase — Meeus chapter 48, formula 48.4.** The simpler
mean-synodic-month-from-epoch approach is **rejected on measurement**, not
taste:

| Approach | Phase-instant error (50 USNO events, 2026) | Illumination error (1459 JPL Horizons samples) |
|---|---|---|
| Mean synodic from epoch | max **16.8 h**, mean 8.0 h | max **7.69 pp**, mean 2.42 pp |
| **Meeus 48.4** | max **49 min**, mean 17 min | max **0.31 pp**, mean 0.09 pp |

A 16.8-hour error puts the phase name in the wrong band for a day at a time and
can show ~8% illumination at true new moon. Meeus 48.4 costs about ten more
lines and reuses the Julian-century scaffolding sunrise already needs.

`illumination = (1 − cos(2π·phase)) / 2` **adds no error of its own**: given
`ψ = 180° − i` it reduces algebraically to Meeus 48.1's `(1 + cos i)/2`.

> **It is not "exact" end-to-end, and an earlier revision of this document
> overclaimed that.** `i = 180° − ψ` is itself 48.4's approximation — the exact
> relation is 48.3, `tan i = R·sin ψ / (Δ − R·cos ψ)`, which needs the Moon's
> distance. So the composite carries 48.4's own bias: at the USNO first-quarter
> instant the pinned model returns **49.976 %** where JPL Horizons gives
> **50.127 %**. That 0.15 pp is inside the ±0.5 pp budget and must not be
> "fixed" (A.4), but the transformation being lossless is not the same as the
> output being exact.

Two implementation traps, detailed in Appendix A: the correction terms **flip
sign** when Meeus 48.4 is rearranged from phase angle to elongation, and the
modulo must be **Euclidean** (Go's `math.Mod` returns negatives, and pre-J2000
dates exercise that path). ΔT is deliberately ignored — at ~69 s in 2026 it
costs ~0.008 pp, some 20× below the model's own error.

**Phase names are eight equal 1/8-cycle bands** centred on their canonical
points, boundaries at odd multiples of 1/16, indexed by
`int(floor(phase*8 + 0.5)) % 8`. These strings are **user-visible output**
(`AlmanacCard.tsx:136` renders `moonPhaseName` verbatim), so they are pinned
here rather than left to the implementer:

| idx | `moonPhaseName` | `phase` band |
|---|---|---|
| 0 | `New Moon` | `[0, 1/16)` ∪ `[15/16, 1)` |
| 1 | `Waxing Crescent` | `[1/16, 3/16)` |
| 2 | `First Quarter` | `[3/16, 5/16)` |
| 3 | `Waxing Gibbous` | `[5/16, 7/16)` |
| 4 | `Full Moon` | `[7/16, 9/16)` |
| 5 | `Waning Gibbous` | `[9/16, 11/16)` |
| 6 | `Last Quarter` | `[11/16, 13/16)` |
| 7 | `Waning Crescent` | `[13/16, 15/16)` |

**The existing web fixtures disagree and must be reconciled to this table.**
`App.test.tsx:73` (`'Full Moon'`), `AlmanacCard.test.tsx:17` (`'First
Quarter'`) and `:52` (`'Waxing Gibbous'`) already match; **`useWeatherData.test.ts:82`
uses `'Full'` and must change to `'Full Moon'`.**

> **Do not assert phase *names* against USNO.** USNO reserves the quarter names
> for exact instants and calls everything between them crescent or gibbous — it
> reports "Waxing Crescent" at 46% illumination where the band table says
> "First Quarter". Assert **illumination and phase instants** against
> USNO/Horizons; assert **names** against the band table itself.

---

## 8. Subtraction: removing the WeatherFlow proxy

Deleted:

- `internal/httpserver/proxy.go` and `proxy_test.go`
- `httpserver.Deps.WeatherFlow` and the `WeatherFlowProxy` interface (`server.go:55,57`)
- `tempestapi.Client.Proxy` and `scrubTransportError` (+ `proxy_test.go`)
- the `tempestapi.NewClient(token)` wiring at `main.go:474`
- the `main.go:504–507` warning about forecast/almanac lacking a token, whose
  stated trigger condition no longer exists

Added in its place, because `proxy.go` is currently the **only** registration of
`GET /api/station` and `GET /api/almanac`:

- `registerStation(mux, deps)` and `registerAlmanac(mux, deps)`, replacing the
  `registerProxy(mux, deps)` call at `server.go:93`
- `Deps.Station config.StationConfig` — the seam extension that carries §4's
  loader output to the handlers

`internal/tempestapi.Client` itself **stays** — `backfill` and API-export both
depend on it, and Gate 1 confirmed its other methods have no dependency on the
deleted ones. Only the proxy method goes.

This is what makes "no WeatherFlow credential in UDP mode" structural rather
than merely unconfigured: a future contributor cannot re-enable a tokenless
proxy by setting an env var, because the code path is gone and the compiler
enforces it.

`main.go`'s `TOKEN` handling is otherwise **untouched**: `TOKEN` keeps meaning
"run API-export and exit". An existing `TOKEN=…` deployment behaves identically
after upgrade. This was the expensive-to-retract half of #62 and the design
declines to spend it.

`Deps.Forecast` is **removed** along with the route, since §9 leaves nothing
that can set it true; `/api/capabilities` reports `forecast: false` as a
constant until #81 restores the field alongside its provider.

---

## 9. Unmet preconditions degrade loudly; they never stop the process

> **Extended 2026-08-15** by
> `docs/designs/2026-08-15-astro-anchor-and-startup-diagnostics-design.md`,
> which adds two rows — an invalid (not merely absent) `RADAR_SITE`, and a
> third severity for a card that mounts but is degraded by an unset
> `STATION_TIMEZONE`. The table below is left as the record of what #162
> shipped; see that document for the current full table.

**No UI feature flag can prevent the appliance from starting or ingesting.**

`startAPIServer` runs at `main.go:581`, before `listenAndPushWithSink` at
`:587`, and already uses `log.Fatal` for config errors. `log.Fatal` exits past
the deferred `cleanupResources`, and the compose deployment sets
`restart: unless-stopped`. A fatal path here is therefore a crash loop that
stops UDP ingest into SQLite and Litestream — the appliance's primary function —
because of a flag that only decides whether a card renders.
`deploy/docker-compose.yml` already ships `RADAR_SITE` empty, so under a fatal
rule, flipping `ENABLE_RADAR=true` and nothing else would turn a dead card into
a permanent data outage.

The rule, applied uniformly:

| Condition | Behaviour |
|---|---|
| `ENABLE_FORECAST=true` (no provider until #81) | ERROR log naming #81; route unregistered; `capabilities.forecast=false` |
| `ENABLE_ALMANAC=true`, coordinates absent | ERROR log naming the missing vars; route unregistered; `capabilities.almanac=false` |
| `ENABLE_ALMANAC=true`, no observation store (Postgres-only) | ERROR log at startup; route unregistered; `capabilities.almanac=false` |
| `ENABLE_RADAR=true`, `RADAR_SITE` or coordinates absent | ERROR log naming the missing vars; route unregistered; `capabilities.radar=false` |
| Any `STATION_*` value present but **malformed** | fatal at startup (§4) — operator error, not an unconfigured feature |

This reuses the mechanism #145 already built: capability flags derived from
`Deps` gate both route registration and the capability document, so the two
cannot disagree, and a card whose capability is false is never mounted. It is
loud — an ERROR log per unmet precondition — without substituting silence or
killing an unrelated process.

It also resolves an inconsistency Gate 1 found in the earlier draft, which
proposed a fatal exit for a forecast card with no provider while accepting a
**silent 503** for an almanac card with no store. Same shape, now the same
treatment: the Postgres-only case moves from a silent 503 to a logged,
capability-gated refusal.

**Migration note for the release.** A deployment currently running
`ENABLE_FORECAST=true` or `ENABLE_RADAR=true` is up and ingesting today, with a
dead card. After this change it stays up and ingesting, the card is not mounted
at all, and an ERROR names what is missing. No configuration becomes
unstartable. The repo has precedent for stating this in `CLAUDE.md`, per the
existing pre-SQLite migration note.

---

## 10. UI changes

| File | Change |
|---|---|
| `types/weather.ts` | trim `StationMeta` to 4 optional fields + optional `radarSite`; `TempRecord.high`/`low`/`highDate`/`lowDate` become nullable; **`sunrise`/`sunset` change from `number` to `string \| null`** and **`daylightMinutes: number \| null` is added** (§6.3) |
| `App.tsx:101` | pass `site={station.radarSite}` to `RadarCard` |
| `AlmanacCard.tsx` | **delete `formatTime` and render `almanac.sunrise`/`sunset` directly**; rewrite `daylightDuration` to split `daylightMinutes`; render `—` for null temps/times/labels; suppress the daylight line when either bound is null |
| `hooks/useWeatherData.test.ts:82` | `moonPhaseName: 'Full'` → `'Full Moon'`, per §7.1's pinned name table — the one fixture that disagrees with the other three |
| `api/tempestApi.ts` | update the file header — the "best-effort WeatherFlow passthrough, may not match declared types" comments on `fetchStationMeta`/`fetchStationAlmanac` are no longer true |
| `hooks/useWeatherData.ts:208–212` | update the "only the core observation works without a TOKEN" comment |
| `components/formatCoord.ts:13` | update the doc comment — `/api/station` is no longer a WeatherFlow passthrough. **The guard itself stays**: §5.1 omits coordinates when unset, so `station` can still lack them |

`ForecastStrip.tsx` is **untouched** and stays in the tree, unreachable while
`capabilities.forecast` is false. #81 re-enables it.

`hasCoordinates` at `App.tsx:92` becomes redundant for the radar mount, since §9
guarantees `capabilities.radar` is false without coordinates. It stays — it
still narrows `station` from `StationMeta | null` for the type checker.

---

## 11. Known limitations

1. **Postgres-only deployments have no dashboard at all** — broader than the
   almanac. `selectStore` disables SQLite when Postgres is the sole store
   (`main.go:148–155`), so `deps.Observations` is nil and
   `/api/observations/current` 503s too; `useWeatherData`'s core fetch rejects
   and `App.tsx:50` renders "Connection Error". The almanac adds nothing to an
   already-dead UI. Pre-existing, out of scope, and now at least **logged** at
   startup rather than silently 503ing (§9). SQLite is the default store.
2. **The forecast card does nothing** until #81.
3. **Station identity is operator-supplied and unvalidated against reality.** A
   typo'd latitude that parses and is in range yields a confidently wrong
   sunrise. There is no tokenless source to cross-check against.
4. **`STATION_TIMEZONE` defaults to UTC**, so an operator who sets coordinates
   but not a timezone gets calendar windows on UTC boundaries. Correct per the
   documented default, and surprising; the docs must say so plainly.

---

## 12. Documentation to update

Cited by section rather than line number, because line numbers drift:

- **`CLAUDE.md`** — the `ENABLE_RADAR`/`ENABLE_FORECAST`/`ENABLE_ALMANAC`
  descriptions; the "Enabling a flag does not yet make its card work" block
  citing #62 and #61; the operational-modes table note; the new `STATION_*`
  vars; a migration note per §9.
- **`deploy/.env.example`** — the `TOKEN` block; the `RADAR_SITE`
  "documented but not read" caveat (now false); the forecast/almanac
  "pending #62 and #61" block.
- **`deploy/docker-compose.yml`** — the same three caveats in its `environment:`
  block and the radar profile comment near the bottom.
- **`docs/designs/2026-07-17-unified-weather-platform-design.md`** — §11 decision
  B3 and the §11 endpoint table's "Sunrise/sunset/moon" row are superseded. Add
  a pointer to this document rather than rewriting history.
- **`internal/radar/proxy.go:226`** — its doc comment cross-references
  `internal/tempestapi`'s `scrubTransportError`, which §8 deletes.

---

## 13. Testing

**Go**

- `internal/astro`: table tests driven directly by **Appendix A** — 16
  sunrise/sunset vectors from USNO (both hemispheres, equator, both polar
  cases, both adjacent-day cases, the polar-night boundary pair, and two US
  DST-transition dates, and the **half-populated** pair at row 17) at ±90 s,
  and 10 moon vectors — illumination and phase angle from JPL Horizons, four of
  the ten instants being USNO phase events — at ±0.5 pp illumination and
  ±0.005 phase, **compared circularly** (§A.4). Assert `MoonPhase`'s convention matches
  `AlmanacCard`'s `phase <= 0.5` waxing branch. Assert phase **names** against
  §7.1's band table, never against USNO's `curphase` strings.
- `internal/sqlite`: `TemperatureExtremes` over — a populated window; an empty
  window; a single-row window; **a window whose `temp_air` is entirely NULL**;
  **a window mixing NULL and non-NULL `temp_air`, asserting the NULL row is not
  returned as the minimum**; and **ties, asserting the earliest timestamp wins**.
- **Window arithmetic** (its own unit, not folded into handler tests): each of
  the four boundaries; a Sunday, asserting week-to-date; a month and a year
  whose 1st is not a Sunday, asserting `week` may precede `month`/`year`; a
  midnight-DST-transition zone.
- `internal/config`: `LoadStation` — valid; out-of-range; unparseable; a
  half-set coordinate pair; unknown timezone; **multiple simultaneous errors,
  asserting all are reported in one error**; and the all-unset default.
- `internal/httpserver`: `/api/station` emitting and omitting each optional
  field per §5.1 — in particular asserting `latitude`/`longitude` are **absent**,
  not `0`, when unconfigured; `/api/almanac` with a fake reader covering
  populated, empty, all-NULL and partial windows; the §6.6 timeout;
  `/api/almanac` and `/api/forecast` unregistered per §9, with
  `/api/capabilities` agreeing.
- `main`: each §9 row — the process starts, logs ERROR, and continues to ingest.

**Web**

- Trimming `StationMeta` breaks **four** literals, all of which must be updated:
  `App.test.tsx:77`, `Header.test.tsx:100`, `RadarCard.test.tsx:84`,
  `useWeatherData.test.ts:54`. (The `as unknown as StationMeta` casts at
  `Header.test.tsx:49,75` and `App.test.tsx:191` survive.)
- Retyping `sunrise`/`sunset` from epoch to `string | null` (§6.3) breaks
  `AlmanacCard.test.tsx:14,15` (which set epochs) and `:28,32` (which do
  `new Date(almanac.sunrise * 1000)` arithmetic). Those are **fixes**, not
  additions, and the rewritten assertions must check that the rendered text is
  the server's string verbatim — the old test asserted the platform locale
  formatter, which is exactly the behaviour §6.3 removes.
- `useWeatherData.test.ts:82` must change `'Full'` → `'Full Moon'` (§7.1).
- New: a `RadarCard` site-wiring test; `AlmanacCard` null-path tests including
  a **half-populated** sunrise/sunset pair (§6.4); a `daylightMinutes` split
  test; a `Header` test asserting the `Tempest Station` and `—` fallbacks fire
  on omitted fields — and specifically that `latitude`/`longitude` **absent**
  does not render `0.0000°N, 0.0000°E` (§5.1).

`task ci` runs `node:typecheck` (`npx tsc -b --noEmit`, `strict: true`, over
`src` including tests), which is exactly the gate that catches the four literals
and the two arithmetic lines. All of the above runs under `task ci`.

Per `subagent-development-workflow.md`, every implementation subagent uses TDD
regardless of what a task says.

---

## 14. Resolved questions

**No open questions remain.** Every question this design raised has been
researched and settled; each answer is recorded in the section that acts on it,
with its evidence. Nothing is deferred to "decide during implementation".

| # | Question | Answer | Decided by | §|
|---|---|---|---|---|
| Q1 | `STATION_*` or `TEMPEST_*` prefix? | `STATION_*` | The repo groups env vars by subsystem prefix (31 vars, 6 families). `TEMPEST_SERIAL` is read at one site, inside the `ENABLE_OTEL` branch only, purely to populate the `tempest.serial` OTel resource attribute — its prefix mirrors that namespace, not station identity. It is not renamed: breaking a documented surface with no defect | §4 |
| Q2 | Week boundary: Sunday or ISO Monday? | Sunday, week-to-date | The operator is US-based, US convention is Sunday, and Monday is a one-line change plus a config var if ever needed. **Two earlier justifications for this same answer were wrong and are both withdrawn in §6.2** — the `DAY_NAMES` argument (invalid: the array is indexed by `getDay()`, so its order is forced by ECMAScript) and the "only US-only features need coordinates" argument (false: the almanac needs them and is not US-specific) | §6.2 |
| Q3 | `highDate` as a server string or an epoch? | Server-formatted string | The browser's timezone is the *viewer's*, not the station's — a Colorado station viewed from Tokyo would mislabel the date and break the "Today" test outright | §6.7 |
| Q4 | Is trimming five `StationMeta` fields too aggressive? | No, trim | NWS `/points/{lat},{lon}` takes no station identifier, key or account (`weather.gov/documentation/services-web-api`); `RadarCard` reads only lat/lon. No identified future consumer wants any of the five, and re-adding one later is additive | §5.2 |
| Q5 | Which astronomy algorithms, to what accuracy? | NOAA/Meeus solar equations; Meeus 48.4 for the moon | Mean-synodic moon phase measured **16.8 h** worst-case error against 50 USNO events vs **49 min** for Meeus 48.4, and 7.69 pp vs 0.31 pp illumination error against 1459 JPL Horizons samples. Rejected on measurement | §7.1, App. A |
| Q6 | Does the runtime image carry tzdata? | Yes | `tzdata=2026c-r0` and 1242 zoneinfo files in the pinned `cgr.dev/chainguard/static` digest. `time/tzdata` is imported anyway, so a base-image bump cannot regress it | §2.1, §4.1 |

Three claims in §2.4 remain unverifiable, and are closed by argument rather
than research: after §8 no WeatherFlow response field is decoded, so their
shape cannot affect shipped code.

### 14.1 Deliberate deferrals

Not open questions — decisions to *not* build something, each with its trigger:

| Deferred | Trigger to revisit |
|---|---|
| Distinguishing polar night from midnight sun in the UI (§6.4) | A deployment above the Arctic Circle |
| A configurable week start (§6.2) | A non-US deployment, or #81 gaining a non-NWS provider |
| Persisting `firmware_revision` to fill `StationHealth`'s blank field (§5.2) | Someone wants the field populated; needs a schema change |
| Postgres parity for the read endpoints (§11, item 1) | A deployment that genuinely wants Postgres-only *and* a UI |
| A `ForecastProvider` interface (§1) | #81, alongside its first implementor |

---

## 15. Gate 1 disposition

Reviewed cold by `sr-eng-review`; every `file:line` citation in §2.1 was
independently re-checked and found exact. All findings are addressed above.

| Finding | Disposition |
|---|---|
| **B1** `/api/station` omission semantics unspecified; zero values render a blank name and Null Island | Fixed — §5.1 specifies every field; §4.1 uses pointers because `omitempty` on `float64` cannot express unset |
| **B2** fatal startup on a cosmetic flag stops ingest; §9's justification was a false dichotomy | Fixed — §9 rewritten to degrade loudly and never exit, reusing #145's mechanism. Decision confirmed with the owner |
| **S1** NULL `temp_air` breaks the natural argmax | Fixed — §6.5 specifies the SQL form and the `.Valid` contract; §13 adds NULL cases |
| **S2** tie behaviour untestable because unspecified | Fixed — earliest occurrence (§6.5) |
| **S3** week can exceed month/year; Sunday ambiguity; DST midnight; no boundary tests | Fixed — §6.2 states all three; §13 adds a window-arithmetic unit |
| **S4** no query timeout, dropping `handleSummary`'s convention | Fixed — §6.6 |
| **S5** §8 read as pure deletion but requires additions | Fixed — §3 and §8 name the `Deps.Station` field and both new registrations |
| **S6** web test list incomplete; `task ci` would fail | Fixed — §13 names all four literals and the two arithmetic lines |
| **S7** `LoadStation` validated after backing services; aggregation unspecified | Fixed — §4.1 moves it to the top of `main()` and requires aggregated errors |
| **S8** Postgres-only understated; inconsistent with §9 | Fixed — §11 item 1 states the full effect; §9 applies one rule to both |
| Minor: tzdata already answered | Fixed — §2.1, §14 |
| Minor: 18 vs 19 direct dependencies | Fixed — §7 |
| Minor: `radar/proxy.go:226` and `formatCoord.ts:13` stale comments | Fixed — §12, §10 |
| Minor: useless "grep for `WeatherFlowProxy`" test | Dropped from §13 — the compiler catches it |
| Minor: `Deps.Forecast` becomes unsettable | Fixed — §8 removes it |
| Minor: §6.1/§6.5 implied two sources | Fixed — §6.1 |
| Minor: §12 line citations drifted | Fixed — §12 cites sections, not lines |
| Minor: `hasCoordinates` redundancy; `RecordsCard`/`AlmanacCard` divergence; constant `today` label; filename propagates to the plan | Noted in §10, §6.2, §6.7 and the preamble |
| Minor: "16 env vars" undercounts | Dropped — the load-bearing claim (`RADAR_SITE` unread) is retained in §2.1 without the count |
| Could not verify: the WeatherFlow evidence cited no sources | Fixed — §2.2 carries the URLs |

### 15.1 Scoped re-review disposition

After Gate 1's findings were addressed the document grew by roughly 40% — §7.1,
Appendix A and the §14 resolutions are all new — so a second, **scoped**
`sr-eng-review` pass ran against the new and changed material only. It
transcribed A.1 and A.2 into runnable code, re-queried every cited primary
source, and returned five blocking findings. All are addressed.

| Finding | Disposition |
|---|---|
| **B1** "sunrise and sunset are always nil together" is **false** — step 14 refines each event at its own JD, so near the polar boundary a day can have a sunrise and no sunset (Utqiaġvik 2026-05-10, confirmed against USNO) | Fixed — §6.4 reverses the claim and explains why; §7 and §7.1 item 3 corrected; A.3 row 17 added as the guarding vector. **This reverses a "correction" the first revision made**; the original "either may be nil" was right |
| **B2** §6.5's argmax `ORDER BY` is a **no-op** — an aggregate with no `GROUP BY` returns one row, and SQLite documents the bare column as arbitrary among ties, so the promised earliest-occurrence tie-break was not implemented and §13's tie test would fail | Fixed — §6.5 now uses `WHERE temp_air IS NOT NULL ORDER BY temp_air DESC, timestamp ASC LIMIT 1`, which is deterministic and returns zero rows for empty/all-NULL windows |
| **B3** A.4 had **no expected-`phase` column**, yet §13 mandated a ±0.005 phase assertion; deriving it from the published phase angle fails 3 of 10 vectors near syzygy | Fixed — A.4 gains an `expected phase` column, labelled as model output rather than published, plus the circular-comparison rule |
| **B4** the eight phase-name **strings were never enumerated**, and the repo's fixtures disagree (`'Full'` vs `'Full Moon'`) on user-visible output | Fixed — §7.1 pins all eight; §10 and §13 require `useWeatherData.test.ts:82` to change |
| **B5** the almanac handler's **date argument was unspecified** — UTC date (§7) vs station-local (§6.2, §6.7) | Fixed — §7.1 item 1 and A.1's preamble specify station-local |
| **S1** "illumination is **exact**… confirmed against Horizons" contradicted A.4's own admitted 0.13 pp bias | Fixed — §7.1 now claims only that the transformation is lossless, and states the composite bias with the measured 49.976 % vs 50.127 % |
| **S2** the "31 s worst case over 20 values" measurement predated rows 15/16 and understated by 42% | Fixed — §7.1 quotes 44 s over every non-nil value and notes the earlier scope |
| **S3** "±90 s is tight enough that a dropped term fails" is **false** — 7 of 10 solar terms can be dropped undetected | Fixed — §7.1 reframes the tolerance as an accuracy bound only, with the measured per-term table |
| **S4** Q2's replacement argument ("only US-only features need coordinates") is falsified by §4, which lists the almanac | Fixed — §6.2 withdraws it and rests the decision on operator preference plus reversibility |
| **S5** `sunrise`/`sunset` were epochs rendered by `toLocaleTimeString` with **no `timeZone`** — the exact viewer-timezone defect Q3 was resolved to prevent, one field over, with Q4 removing the client-side fix | Fixed — §6.3 preformats them server-side and adds `daylightMinutes`; §10 and §13 updated |
| **S6** A.1 step 14's singular `timeUTC` was ambiguous; the wrong reading shifts sunset up to 32 s and passes every vector | Fixed — step 14 specifies per-event refinement explicitly |
| **S7** §6.5's doc comment described the rejected SQL form, contradicting its own prose | Fixed — the comment now matches the specified query |
| **S8** §2.3 stated the platform-design correction flatly where the evidence supports a hedge, and §2.4 omitted "in UDP mode" | Fixed — both requalified; §12 instructed to use the hedged wording |
| **S9** "no open questions remain" was unsupportable while B1–B5 stood | Resolved by fixing B1–B5 |
| Minors: A.1 step 3/11 `L0` contradiction · `POSTGRES_*` counted 9 not 10 · A.3 not regenerable from its own recipe · `julianDay`/`euclideanMod` undefined · the coefficient note mischaracterised two series as three ports · `Location` pointer rationale · `RadarSite`/`ENABLE_RADAR` join unspecified · §13 miscredited 4 USNO instants to Horizons · a `§11.1` reference to a flat list | All fixed in place |

Two items were **not** changed. The reviewer suggested `Location` could be a
value type; it cannot idiomatically, since `time.LoadLocation` returns a pointer
and `time.UTC` is one — §4.1 now says so. And A.4 vector 5's thin 0.00535 name
margin is kept, with a warning, because it is a genuine boundary case worth
testing.

---

## Appendix A — pinned astronomy equations and test vectors

Research output backing §7.1. Reproduced here so the implementation plan can be
executed without re-deriving anything, and so the design survives NOAA taking
its calculator down (§7.1).

### A.1 Sunrise / sunset — equation sequence

Transcribed from `gml.noaa.gov/grad/solcalc/main.js` (`calcTimeJulianCent` …
`calcSunriseSet`). Inputs: `lat`, `lon` in degrees, **east-positive**; `Y, M, D`
are the calendar fields of `t` **in `t`'s own location** (`t.Date()`, not
`t.UTC().Date()` — see §7.1 item 1). Trigonometric arguments in radians;
`sin`, `cos`, `tan` take radians, and every quantity below is in degrees unless
noted, so convert at each call.

```
1.  Julian Day at 00:00 UT (Meeus 7.1)
      if M <= 2 { Y -= 1; M += 12 }
      A  = floor(Y/100);  B = 2 - A + floor(A/4)
      JD = floor(365.25*(Y+4716)) + floor(30.6001*(M+1)) + D + B - 1524.5
2.  T   = (JD - 2451545.0) / 36525.0
3.  L0  = 280.46646 + T*(36000.76983 + T*0.0003032)          // normalise to [0,360)
4.  M☉  = 357.52911 + T*(35999.05029 - 0.0001537*T)          // NOT normalised
5.  e   = 0.016708634 - T*(0.000042037 + 0.0000001267*T)
6.  C   = sin(M☉)*(1.914602 - T*(0.004817 + 0.000014*T))
        + sin(2*M☉)*(0.019993 - 0.000101*T)
        + sin(3*M☉)*0.000289
7.  O   = L0 + C
8.  Ω   = 125.04 - 1934.136*T
    λ   = O - 0.00569 - 0.00478*sin(Ω)
9.  sec = 21.448 - T*(46.8150 + T*(0.00059 - T*0.001813))
    ε0  = 23.0 + (26.0 + sec/60.0)/60.0
    ε   = ε0 + 0.00256*cos(Ω)                                 // same Ω as step 8
10. δ   = asin( sin(ε) * sin(λ) )
11. y      = tan(ε/2)^2
    // L0 here is the step-3 NORMALISED value and M☉ the step-4 UN-normalised
    // one, exactly as NOAA does it. L0's normalisation is numerically inert --
    // sin 2L0, sin 4L0 and cos 2L0 are all 360-periodic in L0 -- so either
    // works; M☉ must NOT be normalised before step 6 uses it.
    Etime  = y*sin(2*L0) - 2*e*sin(M☉) + 4*e*y*sin(M☉)*cos(2*L0)
           - 0.5*y*y*sin(4*L0) - 1.25*e*e*sin(2*M☉)
    eqTime = degrees(Etime) * 4.0                             // MINUTES
12. cosH = cos(90.833°)/(cos(lat)*cos(δ)) - tan(lat)*tan(δ)
    if cosH >  1 -> polar night  (return nil, nil)
    if cosH < -1 -> midnight sun (return nil, nil)
    H    = degrees(acos(cosH))
13. sunriseMin = 720 - 4.0*(lon + H) - eqTime                 // may be <0 or >1440
    sunsetMin  = 720 - 4.0*(lon - H) - eqTime                 // DO NOT CLAMP
14. Refinement -- run SEPARATELY FOR EACH EVENT, using that event's own
    timeUTC from step 13:
        sunrise: recompute steps 2-13 with JD' = JD + sunriseMin/1440.0
        sunset:  recompute steps 2-13 with JD' = JD + sunsetMin /1440.0
    Take each event's second result. Exactly ONE refinement pass each,
    matching NOAA's calcSunriseSet, which calls calcSunriseSetUTC per event.
    A single shared recomputation is WRONG and shifts sunset by up to 32 s
    -- inside the +/-90 s tolerance, so the vectors would not catch it.
    Because each event refines at a different JD, step 12's guard is
    re-evaluated per event and the two can disagree near the polar boundary
    (§6.4) -- that is correct, not a bug to smooth over.
15. result = midnightUTC(Y,M,D) + minutes
```

### A.2 Moon phase — equation sequence

Two helpers this sequence needs, defined here because A.1 does not supply them:

```
// Continuous JD INCLUDING time of day -- A.1 step 1 gives only the 00:00 UT
// form. Omitting the fractional day costs ~5 pp illumination (6 of the 10
// A.4 vectors are at non-midnight instants and would catch it).
julianDay(t) = jd0(t.UTC()) + (hh*3600 + mm*60 + ss) / 86400.0
               where jd0 is A.1 step 1 applied to t.UTC()

// Euclidean modulo. Go's math.Mod returns NEGATIVE results for negative
// input, and pre-J2000 dates drive ψ negative (A.4 vector 10 exercises this).
euclideanMod(x, m) = r := math.Mod(x, m); if r < 0 { r += m }; return r
```

```
jde = julianDay(t)                  // continuous JD, including time of day
T   = (jde - 2451545.0) / 36525.0

D  = 297.8501921 + 445267.1114034*T - 0.0018819*T^2 + T^3/545868   - T^4/113065000
M  = 357.5291092 +  35999.0502909*T - 0.0001536*T^2 + T^3/24490000
M' = 134.9633964 + 477198.8675055*T + 0.0087414*T^2 + T^3/69699    - T^4/14712000

ψ  = D + 6.289*sin(M') - 2.100*sin(M) + 1.274*sin(2D - M')
       + 0.658*sin(2D) + 0.214*sin(2M') + 0.110*sin(D)

phase        = euclideanMod(ψ, 360) / 360     // 0 new, 0.25 first qtr, 0.5 full
illumination = (1 - cos(2π * phase)) / 2      // exact, reduces to Meeus 48.1
name         = NAMES[ int(floor(phase*8 + 0.5)) % 8 ]
```

**Sign trap.** Meeus 48.4 is published for the *phase angle*
`i = 180° − D − 6.289 sin M' + 2.100 sin M − …`. Because `ψ = 180° − i`, every
correction term **flips sign** in the elongation form above. Getting this
backwards produces ~15 pp illumination errors that still look plausible.

**Modulo trap.** `euclideanMod` is required — Go's `math.Mod` returns negative
results for negative input, and pre-J2000 dates (vector 10) drive `ψ` negative.

**ΔT is deliberately ignored** (~69 s in 2026 ⇒ ~0.008 pp, some 20× below the
model's own error). Comment it so nobody "fixes" it.

**Coefficient note.** A.1's `M☉` and A.2's `M` differ in their `T²` term
(−0.0001537 vs −0.0001536). **This is not two ports disagreeing — they are two
different published series.** A.1 uses Meeus 25.3, the low-accuracy solar
series (`357.52911 + 35999.05029T`), as NOAA does. A.2 uses Meeus 47.3, the
lunar-theory solar argument (`357.5291092 + 35999.0502909T + … + T³/24490000`).
Use each as written for its own algorithm.

(Published ports do also differ marginally on 47.3's `T²` term — `pymeeus` and
`astronomia` give −0.0001536, `soniakeys/meeus` −0.0001535. The spread is ~1e-5
degrees, utterly negligible; A.2 takes the majority value.)

### A.3 Sunrise / sunset vectors — US Naval Observatory

Source: `https://aa.usno.navy.mil/api/rstt/oneday?date=DATE&coords=LAT,LON&tz=0&dst=false`
· definitions `aa.usno.navy.mil/faq/RST_defs`. All values UTC, whole minutes,
height 0. **Tolerance ±90 s**; nil rows are exact comparisons.

> **Regenerating these rows takes one extra step, and the recipe alone will not
> reproduce them.** USNO returns the events falling within the *requested UTC
> day*, whereas this table wants the pair bracketing that day's *solar noon*.
> For row 3, `date=2026-03-20` returns `Set 01:11` (that morning's set, from the
> previous solar day); the tabulated `2026-03-21T01:12Z` comes from the
> `date=2026-03-21` response. Every row below is correct as tabulated — but a
> maintainer who re-queries naively will "correct" several of them by a minute
> and by a day. Cross-check against `Upper Transit` to identify the right pair.

| # | Location | lat | lon | date passed | sunrise (UTC) | sunset (UTC) | exercises |
|---|---|---|---|---|---|---|---|
| 1 | Denver | 39.74 | −104.98 | 2026-06-21 | 2026-06-21T11:32Z | 2026-06-**22**T02:31Z | June solstice; sunset on D+1 |
| 2 | Denver | 39.74 | −104.98 | 2026-12-21 | 2026-12-21T14:17Z | 2026-12-21T23:39Z | December solstice |
| 3 | Denver | 39.74 | −104.98 | 2026-03-20 | 2026-03-20T13:03Z | 2026-03-**21**T01:12Z | March equinox |
| 4 | London | 51.5074 | −0.1278 | 2026-06-21 | 2026-06-21T03:43Z | 2026-06-21T20:22Z | both on D |
| 5 | Sydney | −33.8688 | 151.2093 | 2026-06-21 | 2026-06-**20**T21:00Z | 2026-06-21T06:54Z | S. hemisphere; sunrise on D−1 |
| 6 | Sydney | −33.8688 | 151.2093 | 2026-12-21 | 2026-12-**20**T18:41Z | 2026-12-21T09:05Z | S. hemisphere summer |
| 7 | Quito | −0.1807 | −78.4678 | 2026-03-20 | 2026-03-20T11:18Z | 2026-03-20T23:24Z | equator, west |
| 8 | Singapore | 1.3521 | 103.8198 | 2026-09-23 | 2026-09-**22**T22:54Z | 2026-09-23T11:00Z | equator, east |
| 9 | Utqiaġvik | 71.2906 | −156.7887 | 2026-12-21 | nil | nil | polar night (`cosH > 1`) |
| 10 | Utqiaġvik | 71.2906 | −156.7887 | 2026-06-21 | nil | nil | midnight sun (`cosH < −1`) |
| 11 | Longyearbyen | 78.2232 | 15.6469 | 2026-01-15 | nil | nil | high Arctic, no sunrise |
| 12 | Longyearbyen | 78.2232 | 15.6469 | 2026-06-21 | nil | nil | high Arctic, no sunset |
| 13 | New York | 40.7128 | −74.0060 | 2026-03-08 | 2026-03-08T11:19Z | 2026-03-08T22:55Z | US DST spring-forward |
| 14 | New York | 40.7128 | −74.0060 | 2026-11-01 | 2026-11-01T11:26Z | 2026-11-01T21:52Z | US DST fall-back |
| 15 | Utqiaġvik | 71.2906 | −156.7887 | 2026-11-18 | 2026-11-18T21:42Z | 2026-11-18T22:42Z | last sunrise before polar night |
| 16 | Utqiaġvik | 71.2906 | −156.7887 | 2026-11-19 | nil | nil | first polar-night day |
| 17 | Utqiaġvik | 71.2906 | −156.7887 | 2026-05-10 | 2026-05-10T10:58Z | **nil** | **half-populated pair** — sunrise with no sunset (§6.4, B1) |

**Row 17 is the vector that guards §6.4's corrected contract** and the one an
implementation is most likely to get wrong, because the natural reading of
step 12 ("one `cosH` governs both") says it cannot happen. USNO for that day:
`Rise 10:58`, `Upper Transit 22:24`, no set; the following day is "Object
continuously above the Horizon". A build that collapses asymmetric results to
`nil, nil` fails here and passes everything else.

Rows 13/14 are deliberately unremarkable: UTC rise/set is *unaffected* by DST,
and the assertion's stated intent is to prove the implementation never consults
`t.Location()`. Say so in the test name, or a reviewer will read them as filler.

Rows 15/16 bracket a boundary and are the most fragile pair here. If either ever
flakes, delete it rather than loosening the tolerance on rows 9–12.

### A.4 Moon vectors — JPL Horizons (illumination) and USNO (phase instants)

Sources: `ssd.jpl.nasa.gov/api/horizons.api` with `COMMAND='301'`,
`CENTER='500@399'`, `QUANTITIES='10,43'` · `aa.usno.navy.mil/api/moon/phases/year?year=2026`.
**Tolerance ±0.5 pp illumination, ±0.005 phase.** The `Illu%` column is the
**authority** — it is published by Horizons. The `expected phase` column is
**not** published: it is the output of a literal A.2 implementation, supplied
because §13 mandates a phase assertion and no external source expresses phase
in A.2's elongation convention.

> **Do not derive expected phase from the published phase-angle column.** Near
> syzygy the relation `i ≈ 180° − ψ` breaks down (it ignores the Moon's
> ecliptic latitude), and three of these ten vectors — 1, 3 and 9 — miss by
> 0.0059 to 0.0110, more than the ±0.005 tolerance. The phase-angle column is
> here to document the Horizons query, not to generate expectations.

> **The phase comparison must wrap at 1.0.** Vectors 1 and 10 sit at
> `phase ≈ 0.9999`; a naive `abs(got − want)` against a `want` just above 0
> yields ~0.99 and fails. Compare circularly:
> `d := math.Abs(got-want); if d > 0.5 { d = 1 - d }`.

| # | instant (UTC) | Horizons `Illu%` | phase angle | expected `phase` | published event | expected name |
|---|---|---|---|---|---|---|
| 1 | 2026-01-18 19:52 | 0.08678 | 176.6238° | 0.99987 | New Moon (USNO) | New Moon |
| 2 | 2026-03-25 19:18 | 50.12661 | 89.8549° | 0.24992 | First Quarter (USNO) | First Quarter |
| 3 | 2026-06-29 23:56 | 99.87539 | 4.0460° | 0.49978 | Full Moon (USNO) | Full Moon |
| 4 | 2026-11-01 20:28 | 50.12906 | 89.8521° | 0.74922 | Last Quarter (USNO) | Last Quarter |
| 5 | 2026-04-08 00:00 | 70.48836 | 65.8098° | 0.68215 | — | Waning Gibbous |
| 6 | 2026-06-21 12:00 | 45.83584 | 94.7773° | 0.23717 | — | First Quarter |
| 7 | 2026-08-14 00:00 | 2.15411 | 163.1206° | 0.04664 | — | New Moon |
| 8 | 2026-10-05 00:00 | 33.95809 | 108.7136° | 0.80181 | — | Last Quarter |
| 9 | 2030-07-15 00:00 | 99.91922 | 3.2573° | 0.49685 | out-of-year check | Full Moon |
| 10 | 2000-01-06 18:14 | 0.02022 | 178.3704° | 0.99995 | Meeus ch.49 epoch | New Moon |

Vector 5 sits **0.00535** from the Last-Quarter band edge at `11/16`, against a
±0.005 phase tolerance — its *name* assertion is guaranteed by only 7 %. Do not
widen the phase tolerance without re-checking it. (Vector 8's margin is
0.0107.)

Vector 7 is the convention trap, kept deliberately: `phase ≈ 0.047` sits in the
New Moon band at 2.1% illumination, where USNO would say "Waxing Crescent".
Comment it — it documents §7.1's naming divergence rather than contradicting it.
Vector 10 exercises the negative-modulo path.

**Known systematic bias, do not "fix".** A.2 derives `phase` from *elongation*,
not the phase angle, so at first quarter it returns exactly 50.000% where the
truth is 50.127%. Correcting the 0.13 pp needs the Moon's distance and the full
Meeus ch. 47 series; it is well inside the 0.31 pp budget.

### A.5 Provenance and its limits

The equation sequences are corroborated by three independent published ports
agreeing (`soniakeys/meeus` Go, `pymeeus`, `commenthol/astronomia`) **and** by
numerical agreement with JPL Horizons and USNO. Meeus's *Astronomical
Algorithms* itself was **not** consulted — strong corroboration, not the primary
text.

The accuracy figures quoted in §7.1 for Meeus 48.4 are **measured**, not
published: no stated bound from Meeus could be found. NOAA's ±1 minute figure
*is* published and quoted verbatim.

Two of those measurements were re-run independently during the scoped
re-review and did not reproduce exactly:

| Figure | First measurement | Re-measurement | Disposition |
|---|---|---|---|
| Meeus 48.4 phase-instant error over 50 USNO 2026 events | max 49 min, mean 17 min | max 45.8 min, mean 17.2 min | Same order, mean agrees; the max differs by ~3 min because the root-finding method was not pinned. Immaterial to the Q5 decision — the rejected alternative is 16.8 **hours** |
| Illumination error vs Horizons | max 0.31 pp over 1459 samples | max 0.19 pp over the 10 appendix vectors | Consistent; the 1459-sample run was not reproduced. Treat 0.31 pp as the working bound |

Both are reported as measured ranges rather than single figures for that
reason. The ±0.5 pp tolerance clears either.

---

## Appendix B — corrections measured during implementation planning

Four statements above are wrong or stale. They are corrected here rather than
edited in place, so the original record stands. Each was measured by
transcribing the appendix algorithms and running them.

1. **§13 says "16 sunrise/sunset vectors". A.3 contains 17.** The count
   predates row 17, which the scoped re-review added to guard the
   half-populated pair — the same §13 sentence goes on to mention row 17.
2. **§7.1's "at most 44 s … the worst being row 15" is stale.** Measured over
   every non-nil value in the 17-row table the worst case is **65.07 s**, at
   row 17 (Utqiaġvik 2026-05-10, sunrise); row 15 measures 43.6 s. Row 17 sits
   at 71.29° latitude, outside NOAA's own ±1 min claim, which NOAA scopes to
   |lat| < 72°. All 17 rows still pass at ±90 s.
3. **A.2's modulo note and A.4's row-10 annotation are wrong.** Vector 10 does
   **not** exercise the negative-modulo path: at `2000-01-06T18:14Z` the
   elongation ψ is **+359.98** and `T` is **+0.000144**, so the instant is not
   even pre-J2000. **No A.4 vector produces a negative ψ**, and a `math.Mod`
   implementation passes all ten. ψ first goes negative between 1999-12-07
   (−10.34) and 1999-12-08 (+0.54). `euclideanMod` needs its own test.
4. **§6.4 says `sunrise`/`sunset` become `number | null` in `weather.ts`.**
   §6.3 and §10 say `string | null`, which is correct — §6.4's line was not
   updated when server-side preformatting was introduced.
