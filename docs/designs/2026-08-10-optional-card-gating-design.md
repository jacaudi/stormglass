# Optional card gating — design

**Issue:** [#145](https://github.com/jacaudi/tempestwx-utilities/issues/145)
**Date:** 2026-08-10
**Status:** implemented on `feat/optional-card-gating` (revised after Gate 1 review)
**Branch point:** `73352e1`

## Goal

Make the Forecast, Radar and Almanac cards opt-in and off by default, so a
deployment that configures none of them renders a dashboard with no empty shells
in it — and so their visibility is an operator decision rather than a side effect
of whether an API call happened to return data.

## What #145 gets right, and what needs correcting

#145 already carries one self-correction: its first draft claimed all three cards
render unconditionally, and it now records the exact mount conditions instead.
Those conditions are accurate. Verified at `web/src/App.tsx:85-98`:

| Card | Mount condition | Empty-shell defect? |
|---|---|---|
| `ForecastStrip` | `:85`, unconditional | **Yes** — renders a titled bar with an empty strip |
| `AlmanacCard` | `:86`, `{almanac && …}` | No — already data-gated, never leaves a shell |
| `RadarCard` | `:87-98`, `{station && …}` | **Yes** — `station` is truthy (see §2), so it mounts and degrades internally |

Three further facts were verified against the tree during this design, and each
changes what the issue is buying.

### 1. The 503 the issue proposes to infer from is unreachable

#145's option 2 is "infer disabled-ness from the existing
`503 weatherflow client not configured`". That response cannot occur in a running
deployment.

`main.go:469` sets `WeatherFlow: tempestapi.NewClient(token)` unconditionally, and
`tempestapi.NewClient` always returns a non-nil `*Client`
(`internal/tempestapi/client.go:56-66`). The 503 is written by `proxy.go:53-57`
only when that dependency is nil, which happens only in tests that construct
`Deps` without it.

### 2. The web UI is only ever served with `TOKEN` empty — and `/api/station` still returns 200

This is the process-wide operational mode, not anything to do with the UDP data
path, and the mode's common name ("UDP mode") invites the wrong reading. The
chain:

- `main.go:538` — `token := os.Getenv("TOKEN")`
- `main.go:551` → `resolveModeAndValidate` (`main.go:433-437`) — a non-empty token
  selects `ModeAPIExport`; otherwise `ModeUDP`
- `main.go:554` → `startAPIServer` (`main.go:463-466`) — **returns `nil`
  immediately unless `mode == ModeUDP`**
- `main.go:557-561` — with a token the process runs `exportWithSink` and exits;
  without one it runs the UDP listener as a daemon

So there is no configuration in which the HTTP server is up *and* `TOKEN` is set.
Whenever the UI is being served, `token` is `""`, and every WeatherFlow-proxied
call sends `Authorization: Bearer ` with nothing after it
(`internal/tempestapi/proxy.go:33`). All three proxied routes share the one client
(`internal/httpserver/proxy.go:34-42`).

**The three routes do not, however, behave alike.** Probed directly against
`https://swd.weatherflow.com/swd/rest` with the exact header the proxy sends:

| Request | Result |
|---|---|
| `GET /stations`, `Authorization: Bearer ` (empty — the running config) | **200** `{"status":{"status_code":0,"status_message":"SUCCESS"}}` |
| `GET /better_forecast`, `Authorization: Bearer ` | **401** `{"status":{"status_code":401,"status_message":"UNAUTHORIZED"}}` |

This is one probe against one host on 2026-08-10; it is not a guarantee of
WeatherFlow's future behaviour. But it explains both reported symptoms exactly,
and it corrects issue [#62](https://github.com/jacaudi/tempestwx-utilities/issues/62)'s
blanket "upstream 401s":

- `/better_forecast` genuinely 401s, so `getJSON` rejects (`tempestApi.ts:50-53`)
  → `forecast` stays `[]` (the empty strip) and `almanac` stays `null` (absent).
- `/stations` **succeeds**. `getJSON` rejects only on `!res.ok`, so a 200 passes
  and `station` becomes a **truthy object with no station fields at all** —
  `latitude`, `longitude`, `name` and `elevation` are every one `undefined`.

That truthy-but-empty object, not "station is present in normal operation", is
why `{station && …}` at `App.tsx:87` mounts the Radar shell. It has a second
consequence the issue never noticed: `Header.tsx:32-37` renders
`formatCoord(station.latitude, station.longitude)`, and `formatCoord` does
`Math.abs(latitude).toFixed(4)` (`web/src/components/formatCoord.ts:5`), so the
default dashboard displays **`NaN°N, NaN°E · m`**. (Derived from the code and the
probe above; not observed in a running browser.)

**Radar shares none of this.** `/api/radar/{site}` reaches the Python NEXRAD
sidecar via `RADAR_SIDECAR_URL` (`main.go:479-480`) — no WeatherFlow client, no
`TOKEN`, no bearer token anywhere in the path.

### 3. Enabling a flag does not currently produce a working card

- **Radar:** `App.tsx:96` mounts `<RadarCard station={station} />` with no `site`
  prop. `RadarCard.tsx:142` therefore initialises `status` to `'not-configured'`
  and `:253-260` returns the "Radar not configured for this station." shell
  *without ever calling* `/api/radar/{site}`. `ENABLE_RADAR=true` produces that
  same shell today. The missing client-side site lookup is the RADAR_SITE→UI
  follow-up deferred from Workstream 2.
- **Forecast and Almanac:** blocked on #62 (a token source usable while the UI is
  served) and [#61](https://github.com/jacaudi/tempestwx-utilities/issues/61)
  (server-side WeatherFlow→Contract-C shaping — the proxy is a raw passthrough,
  so even an authenticated response would not match `ForecastDay[]`).

**Consequence, and the scope decision it forces.** All of #145's present value is
in the default-off direction: deleting shells. Enabling a flag is, for now, a
switch that reveals a non-working card. This design deliberately **does not**
absorb #61, #62 or the radar site wiring; it names them as prerequisites for the
enabled path to mean anything, and ships the gate. That was an explicit scope
decision, not an oversight.

## Approach

Three candidate channels for communicating enabled features to the UI.

### A. `GET /api/capabilities` — chosen

A small static JSON document, fetched once on UI load, with a present concrete
consumer (`App.tsx` deciding what to mount). It states capability directly
instead of asking the UI to infer it from a failure, and it keeps three genuinely
different questions on three different surfaces:

| Question | Endpoint |
|---|---|
| Is the process alive? | `/healthz` (exists, unchanged) |
| Should it receive traffic? | not needed — the process exits instead of reporting not-ready |
| What features are enabled? | `/api/capabilities` (new) |

### B. Infer from proxy errors — rejected

Rejected on facts, not taste. The signal #145 names does not occur (§1 above).
It could be made to occur by nil-ing `deps.WeatherFlow` when both features are
disabled — but that requires the same server-side flag plumbing option A needs,
and then still leaves the UI inferring from two *different* codes: a 503 for
forecast/almanac and a 404 for radar (`registerRadar` leaves the route
unregistered, `radar.go:37-40`, so it falls through to `server.go:161`). Both
codes also occur for non-disabled reasons — a 503 is what a transient upstream
outage looks like, a 404 is what a wrong site code looks like. More UI logic, for
a weaker signal.

### C. Inject state into `index.html` — rejected

Avoids the round trip, but `registerStatic` serves the SPA entry point straight
from an `fs.FS` via `http.ServeFileFS` (`server.go:186-187`); injection means
templating it on every request. The self-only CSP has no `unsafe-inline` for
scripts (`server.go:36`), so an inline data island would need a nonce, and the UI
would lose the ability to refetch. More machinery than a 60-byte endpoint.

## Server contract

```
GET /api/capabilities → 200 application/json
Cache-Control: no-store

{"forecast": false, "radar": false, "almanac": false}
```

Always registered. No auth, no dependency checks, no failure mode — it marshals a
three-field struct via the existing `writeJSON` (`observations.go:436`).
`no-store` so an operator's config change is visible on the next page load rather
than after a cache expiry.

**Header ordering matters:** `writeJSON` calls `w.WriteHeader` immediately
(`observations.go:436-438`), so `Cache-Control` must be set on the
`http.ResponseWriter` *before* the `writeJSON` call. No other `/api/*` handler
sets any `Cache-Control` today — the only two occurrences are the static-asset
paths at `server.go:173` and `:186`.

**The three JSON key names are an external contract** in the same sense
`tempestApi.ts:28-30` already documents for Contract C's URLs: they are written
once in Go and once by hand in TypeScript, with no code generation between them.
Rather than assert the same literal in two places, both sides are pinned to **one
committed golden fixture** — see §Testing. Real generation across the whole
Contract C surface is tracked in
[#149](https://github.com/jacaudi/tempestwx-utilities/issues/149) and is not this
change's job; the fixture pattern used here extends to `CurrentObservation`
without new tooling, and #149 records that as a candidate answer in its own right.

### One value, two readers

`httpserver.Deps` gains two fields:

```go
// Forecast and Almanac gate GET /api/forecast and GET /api/almanac and the
// matching entries in GET /api/capabilities. main.go sets them from
// ENABLE_FORECAST / ENABLE_ALMANAC.
Forecast bool
Almanac  bool
```

Radar's capability is **derived, not stored**. `deps.Radar != nil` is already the
single source of that opt-in (`radar.go:38`, set only when `ENABLE_RADAR=true` at
`main.go:478-481`); adding a parallel `Radar bool` would be a second
representation of one fact, free to drift from the route it describes.
`radar.NewProxy` returns `&Proxy{…}` and never a typed nil
(`internal/radar/proxy.go:77-84`), so the interface-holding-a-nil-pointer trap
does not apply.

```go
type capabilities struct {
	Forecast bool `json:"forecast"`
	Radar    bool `json:"radar"`
	Almanac  bool `json:"almanac"`
}

func newCapabilities(deps Deps) capabilities {
	return capabilities{
		Forecast: deps.Forecast,
		Radar:    deps.Radar != nil,
		Almanac:  deps.Almanac,
	}
}
```

`New` computes this once for the capabilities handler:

```go
registerHealthz(mux)
registerCapabilities(mux, newCapabilities(deps))
registerObservations(mux, deps)
registerProxy(mux, deps)
registerRadar(mux, deps)
registerStatic(mux, deps)
```

`registerProxy` keeps its existing `(mux, deps)` signature and reads
`deps.Forecast` / `deps.Almanac` directly — it registers `GET /api/forecast` and
`GET /api/almanac` **only when the corresponding field is true**, mirroring
`registerRadar`'s existing nil-guard. An unregistered `/api/*` route falls
through to `registerStatic`'s reserved-prefix branch and 404s
(`server.go:161-164`, already pinned by `server_test.go:134-145`). Passing the
computed `capabilities` value into `registerProxy` as well would be redundant:
`caps.Forecast` *is* `deps.Forecast`, and the no-drift property comes from both
readers hitting the same `Deps` field, not from threading a struct around.

`GET /api/station` stays registered unconditionally. It backs no card of its own,
but note that `station` **is** a mount precondition for the Radar card, so it is
not unrelated to this change — see §"UI data flow" for the validity guard that
relationship requires.

This is the additive seam: a fourth optional card is one `Deps` field, one struct
field, one registration line — its siblings are not touched.

## Configuration

Two new variables, following the existing `ENABLE_*` convention and read with
`config.ParseBoolEnv` in `startAPIServer` alongside `ENABLE_RADAR`
(`main.go:474-477`):

| Variable | Default | Effect |
|---|---|---|
| `ENABLE_FORECAST` | unset ⇒ `false` | registers `GET /api/forecast`; `"forecast": true` |
| `ENABLE_ALMANAC` | unset ⇒ `false` | registers `GET /api/almanac`; `"almanac": true` |

`ParseBoolEnv` returns `false` with no error for an unset or empty value and a
fatal error for an unparseable one (`internal/config/env.go:15-25`) — a typo like
`ture` stops the process at startup rather than silently disabling a feature.
That is existing fail-loud behaviour inherited unchanged, not something this
design adds.

### Startup warning when an enabled flag cannot work

Because settled scope leaves #61/#62 unfixed, `ENABLE_FORECAST=true` today
produces a registered route that proxies an unauthenticated request and a card
that renders nothing, with no signal anywhere. `startAPIServer` logs a warning
naming the cause:

```go
if (enableForecast || enableAlmanac) && token == "" {
	slog.Warn("forecast/almanac enabled but no WeatherFlow token is available "+
		"while the UI is served; upstream calls will be unauthenticated (see issue #62)")
}
```

`token` is necessarily `""` in this function today (§2), so the condition is
always true when a flag is set — but writing the real predicate means the warning
stops firing on its own once #62 supplies a token, rather than needing to be
remembered.

### Documentation surfaces

`ENABLE_RADAR` is documented in **`deploy/.env.example`** and
**`deploy/docker-compose.yml`**, and in neither `README.md` nor `CLAUDE.md`
(verified by grep). The two new flags go where their sibling already lives, so
all three UI flags stay in one place:

- `deploy/.env.example` — alongside `ENABLE_RADAR` / `RADAR_SIDECAR_URL`.
- `deploy/docker-compose.yml` — same block.
- `CLAUDE.md`'s environment-variable list — add all **three**, `ENABLE_RADAR`
  included. Its absence there is a pre-existing gap in the same blast radius, and
  documenting two of three siblings would be worse than documenting none.

**Not** CLAUDE.md's operational-modes matrix. That table's axes are
`ENABLE_PROMETHEUS_PUSHGATEWAY | ENABLE_PROMETHEUS_METRICS | ENABLE_POSTGRES |
TOKEN | Behavior`, and these flags are orthogonal to every row — they change which
cards render, not which mode the process runs in. Adding two columns across eight
rows would encode a dependency that does not exist. A single footnote under the
table stating that UI cards are gated separately is the correct treatment.

## UI data flow

`useWeatherData` owns the capabilities fetch and returns it alongside the rest of
the data. Its **signature is unchanged** —
`useWeatherData(stationId?: number, recordsWindowDays: RecordsWindowDays = 7)` —
so `App.tsx:25-26`'s call site is untouched; `WeatherData` simply gains a
`capabilities: Capabilities | null` field.

The alternative — a standalone `useCapabilities` hook passed in as a third
parameter — was rejected because the `null → resolved` transition changes
`loadData`'s identity mid-flight, and the effect's cleanup
(`useWeatherData.ts:183`) then **aborts the in-flight initial load** and restarts
it. Depending on which request wins the race that either delays first paint by a
whole round or bounces the rendered dashboard back to the "Connecting to
station…" spinner (`App.tsx:33-40`, because `loadData` v2 calls
`setIsLoading(true)` at `:99`). Owning both inside one hook lets the load simply
wait.

1. `web/src/types/weather.ts` gains a `Capabilities` interface
   (`forecast`/`radar`/`almanac`, all `boolean`).
2. `web/src/api/tempestApi.ts` gains `ENDPOINTS.capabilities` and
   `fetchCapabilities()`, using the existing `getJSON` helper.
3. Inside `useWeatherData`, two pieces of state:
   `caps: Capabilities | null` and `capsSettled: boolean`. The mount effect
   fetches capabilities, sets `caps` to the document on success or leaves it
   `null` on any failure, and sets `capsSettled` in both cases. `null` means
   **unknown**, treated exactly like all-false. A failure never reaches the error
   screen — capabilities are not the core slice, and an unreachable
   optional-feature document must never blank the dashboard.
4. **The first load waits for capabilities to settle**, not to succeed:

```ts
const forecastEnabled = caps?.forecast === true;
const almanacEnabled  = caps?.almanac  === true;

const loadData = useCallback(async () => { … }, [stationId, forecastEnabled, almanacEnabled]);

useEffect(() => {
  if (!capsSettled) return;
  loadData();
  return () => abortRef.current?.abort();
}, [capsSettled, loadData]);
```

   The two booleans **must be hoisted to consts**. Writing
   `[stationId, caps?.forecast === true]` inline fails this repo's lint —
   `react-hooks/use-memo` errors with *"Expected the dependency list to be an
   array of simple expressions"*, plus two `exhaustive-deps` warnings, and
   `.taskfiles/node.yml` runs `npx eslint --max-warnings 0`. Verified: exit 1.
   Hoisting keeps the intended semantics (a stable boolean, so `null → false` is
   a no-op) and passes.

   Because `caps` and `capsSettled` are set in the same continuation, React
   batches them into one render, so `loadData`'s identity is already final when
   the effect first runs — one load, no restart, in every configuration.

5. `loadData` **skips the disabled fetches.** Without this a default deployment
   issues two requests per load that are now guaranteed 404s. Disabled slices
   resolve to their empty value (`[]` for forecast, `null` for almanac) instead
   of being fetched, leaving the `Promise.allSettled` array shape and its index
   destructuring (`useWeatherData.ts:101-108`) unchanged. Hoist those empty
   values to module constants so a fresh `[]` per load does not defeat React's
   `Object.is` bail-out.

6. **Capabilities are retried while they remain unknown.** A single transient
   failure must not hide all three cards for the lifetime of a page — this is a
   kiosk-style appliance whose tab may stay open for weeks, and every other slice
   recovers on the next poll tick (`useWeatherData.ts:186-189`). So: while
   `caps === null`, the 30-second poll also re-attempts `fetchCapabilities`, and
   `refresh` re-attempts it too. Once a document is held — including an all-false
   one, which is a `Capabilities` object and not `null` — no further capability
   fetches are made.

   The capability fetch carries its own deadline
   (`AbortSignal.any([signal, AbortSignal.timeout(…)])`). Without one, a request
   that *hangs* rather than fails would hold the settle-gate closed and leave the
   dashboard on the loading screen indefinitely — including the core observation
   path, which previously had no such dependency. A timeout rejects, which the
   catch treats as unknown, which fails closed and lets the load proceed.

   **One narrow exception to the no-double-load property:** if capabilities were
   unknown and the user presses Retry *and* the capability re-attempt then
   succeeds, the enabled-flags flip changes `loadData`'s identity and the effect
   fires a second load that aborts the one `refresh` just started, briefly
   showing the loading screen. It costs one wasted round trip on a path the user
   explicitly asked to retry, and removing it would need state this design does
   not otherwise want, so it is accepted rather than fixed.

7. **A truthy `station` is not a usable one.** Per §2 it may be WeatherFlow's 200
   error envelope with every field `undefined`. Add one type-predicate helper
   beside `formatCoord`, single-sourcing that knowledge for its two consumers:

```ts
export function hasCoordinates(station: StationMeta | null): station is StationMeta {
  return station != null &&
    typeof station.latitude === 'number' && typeof station.longitude === 'number';
}
```

8. `App.tsx` mount conditions become:

```jsx
{capabilities?.forecast && <ForecastStrip forecast={forecast} unit={prefs.temperatureUnit} />}
{capabilities?.almanac && almanac && <AlmanacCard almanac={almanac} unit={prefs.temperatureUnit} />}
{capabilities?.radar && hasCoordinates(station) && (
  <ErrorBoundary><RadarCard station={station} /></ErrorBoundary>
)}
```

   The almanac keeps its existing data gate. The capability answers "may this card
   exist"; `almanac` still answers "is there anything to draw". Dropping the
   second would reintroduce the shell the flag is meant to prevent.

9. `Header.tsx:32` guards the location span with the same helper, replacing
   `{station && (…)}` with `{hasCoordinates(station) && (…)}`. This removes the
   `NaN°N, NaN°E · m` line from the default dashboard. `station?.name ??
   'Tempest Station'` at `:30` already falls back correctly and needs no change.

The guard is not scope creep: it is the same root cause as the Radar shell, one
line in each of two files, and it also stops `RadarCard` from being handed
`center: [undefined, undefined]` (`RadarCard.tsx:151-157`) the moment the
deferred site wiring lands.

## Behaviour when disabled

**Not mounted at all.** No empty bar, no placeholder text, no wrapper element —
nothing in the DOM. This is the acceptance criterion the tests assert directly.

## Backward compatibility

- **A deployment setting none of the three variables** (the default, and what
  #145 reproduces against) loses the empty 7-Day Forecast bar, the "Radar not
  configured" card, and the `NaN°N, NaN°E · m` header line. Almanac was already
  absent. Everything else is unchanged. The UI gets *simpler*, not different.
- **`ENABLE_RADAR=true`** no longer guarantees a Radar card at all. As
  implemented, `App.tsx` gates radar on `capabilities?.radar && hasCoordinates(station)`,
  and `/api/station` answers an empty bearer with a 200 and a status-only
  envelope — so on the UI's normal (`TOKEN` unset) deployment the card is not
  mounted, rather than mounted showing "Radar not configured for this station."
  That placeholder is one of the three empty shells #145 exists to delete, so
  this is intended; it is a behaviour change from the pre-#145 build, not a
  no-op. A working card still needs the deferred RADAR_SITE→UI wiring (no
  `site` prop is passed) plus issue #62 (a UDP-mode token source for
  `/api/station`); #61 (response shaping) may also apply. Both out of scope
  here.
- **`/api/forecast` and `/api/almanac` return 404 when disabled.** Nothing that
  worked stops working: both are WeatherFlow-backed, the UI is only ever served
  with `TOKEN` empty (§2), and `/better_forecast` answers an empty bearer with a
  401 today. Radar is unaffected — it never touches WeatherFlow. `/api/station`
  is likewise unaffected: it stays registered and keeps returning exactly what it
  returns now.
- **No new required configuration.** Every variable is optional and defaults to
  the behaviour #145 asks for.

## Health surfaces — unchanged, deliberately

Capability state does **not** go in `/healthz`, and no readiness endpoint is
added. `/healthz` is the liveness endpoint (`server.go:135-145`): it returns a
hardcoded `{"status":"ok"}` with zero dependency checks and sits on the container
`HEALTHCHECK` hot path via the `healthcheck` subcommand (`main.go:176`). Putting
feature state there invites something to key on it, and then an unconfigured
decorative card could make the container look unhealthy and take UDP ingest down
with it. A readiness endpoint is equally unnecessary: the process exits at
startup when the SQLite store cannot be opened, so it never needs to report
not-ready.

Mirroring the capability set into a verbose operator info response would be
acceptable, provided it can never influence a health verdict. Nothing in this
design does so.

## Error handling

| Failure | Behaviour |
|---|---|
| `/api/capabilities` fetch rejects or returns non-OK | `caps` stays `null`; all optional cards stay hidden; core dashboard unaffected and shows no error; retried on the next poll tick and by `refresh` |
| Capabilities not yet settled | First load is held; the existing "Connecting to station…" screen already covers this window |
| A capability key is missing from the response | Treated as `false` |
| `/api/forecast` or `/api/almanac` requested while disabled | 404, from the reserved-`/api/` fallthrough |
| `/api/station` returns a 200 with no station fields | `hasCoordinates` is false → no location line, no Radar mount |
| Capabilities handler itself | Has no failure mode; it marshals a three-field struct |

Fail-closed is the deliberate choice throughout: the worst case is a dashboard
identical to a correctly-configured default deployment, whereas fail-open would
reintroduce the empty shells in precisely the situation where something is
already wrong. The retry in §6 is what keeps "closed" from becoming permanent.

## Testing

Baselines to hold: `go test ./internal/httpserver/ -count=1` is green, and
`npm test` in `web/` is 14 files / 83 tests passing.

### Go — `internal/httpserver`

- **Capabilities document.** Table test over flag combinations asserting the
  exact JSON body — keys and values — for `Deps` with each combination of
  `Forecast`, `Almanac` and a nil/non-nil `Radar`.
- **Golden fixture.** A second test marshals the default (all-false) value and
  compares it, after `bytes.TrimSpace`, against the committed
  `web/src/types/__fixtures__/capabilities.json`, rewriting that file when run
  with `-update` (the golden-file pattern go-standards §8.1 prescribes). Normal
  runs only read it, so no test writes to the working tree.
- **Cache header.** `/api/capabilities` responds with `Cache-Control: no-store`.
- **Radar derivation.** The same `Deps` with `Radar: nil` yields both
  `"radar": false` *and* a 404 from `GET /api/radar/{site}`; with a fake proxy it
  yields both `"radar": true` and a served route. This pins the derived value to
  the route it describes. It does **not** make a future redundant `Radar bool`
  impossible — a test cannot detect a redundant field, and someone who adds one
  can make the table green again by setting both. What it buys is a forced
  conversation at the moment of that change, which is worth having; the actual
  no-drift guarantee comes from `newCapabilities` and `registerRadar` reading the
  one `deps.Radar` field.

**Three existing tests break — they are edits, not extensions.**
`testDepsWithWeatherFlow` (`proxy_test.go:17-23`) sets only `StaticFS` and
`WeatherFlow`, leaving the new fields `false`:

| Test | Line | Would fail because |
|---|---|---|
| `TestProxy_NoTokenInResponseOrLogs` | `:75-81` | GETs `/api/forecast` expecting 200 → route unregistered → 404 |
| `TestProxy_ForecastAndAlmanac` | `:107-121` | GETs both expecting 200 → 404 |
| `TestProxy_NilWeatherFlow` | `:143-151` | expects **503** for all three → forecast/almanac now 404 |

The fix is one edit: `testDepsWithWeatherFlow` sets `Forecast: true, Almanac:
true`. That helper exists to exercise the proxy, so enabling both is the correct
default, and it restores `TestProxy_NilWeatherFlow` exactly — with the routes
registered and `WeatherFlow` nil, all three endpoints hit the `wf == nil` guard
and 503 again, preserving the SGE review M7 regression guard the test was written
for. New gating tests construct `Deps` explicitly with the flags they need rather
than going through the helper.

### Vitest — `web/`

- **The wire keys are pinned to the Go-generated fixture, not to a second
  literal.** A rename or typo on either side of the contract must fail a build
  rather than silently hide every card, and the two sides must be pinned to each
  other rather than to two independent copies of the same string:

```ts
import capabilitiesJson from './__fixtures__/capabilities.json';

// Assignment is the pin: a key renamed or dropped on the Go side changes the
// fixture and fails this typecheck; a key renamed in Capabilities fails it
// against an unchanged fixture.
const fixture: Capabilities = capabilitiesJson;
expect(Object.keys(fixture).sort()).toEqual(['almanac', 'forecast', 'radar']);
```

  **No tsconfig change is needed.** `resolveJsonModule` does not appear in
  `web/tsconfig.app.json`, but `moduleResolution: "bundler"` already implies it:
  probed against the unmodified config, the import resolves *and* is precisely
  typed as `{ forecast: boolean; radar: boolean; almanac: boolean }` (a bogus
  property access errors with TS2339, and forcing `"resolveJsonModule": false`
  errors with TS2732). An earlier draft of this design claimed the option had to
  be added; that was wrong.

  The fixture must live under `web/src/` because that tsconfig's `include` is
  `["src"]`, which is also why it sits in `src/types/__fixtures__/` rather than
  in a Go `testdata/` directory: importing from outside the Vite root would
  fight `server.fs.allow`, and a runtime `readFileSync` would give `any` and
  lose the compile-time check entirely.

  Note that this pin is **typecheck-only**. Vite resolves `.json` natively at
  runtime and `import type` is erased by esbuild, so the vitest run alone cannot
  fail on a renamed key — `npx tsc -b --noEmit` is what catches it.

  `fetchCapabilities` keeps a separate test driving the real `getJSON` path for
  its 200 / non-OK / network-error behaviour; that one is about transport, not
  about key names.
- **`useWeatherData`:** returns the document on 200 and `null` on a non-OK status
  or a network error; with forecast/almanac disabled *or* unknown, `fetch` is
  never called with `/api/forecast` or `/api/almanac`, and with them enabled it
  is; a failed capabilities fetch is retried on the next poll tick and a
  successful one is not refetched.

  **Its module mock must be updated deliberately.** `useWeatherData.test.ts:15-22`
  is a `vi.mock` factory listing each API function explicitly; `fetchCapabilities`
  must be added **and defaulted to an all-true document in `beforeEach`**. Left
  unmocked it resolves `undefined` → treated as unknown → fail closed, and all
  nine `renderHook` call sites would silently start exercising the disabled path.
  The test named *"keeps current populated when only the WeatherFlow-backed
  fetches fail"* (`:127`) would still pass while no longer testing two of the
  three fetches it names.

- **`App.test.tsx`:** for each card, capability `false` ⇒ absent from the DOM
  (`queryBy… toBeNull`, not merely empty), capability `true` plus its data ⇒
  present. Because capabilities now arrive through `useWeatherData`, which the
  file already mocks wholesale (`:79-81`), the fix is to add `capabilities` to
  `mockWeatherData` — no new module mock, no global `fetch` stub. TypeScript
  forces this update, since `WeatherData` gains a required field. The existing
  "Records before 7-Day Forecast" ordering test needs `forecast: true` there so it
  keeps exercising what it was written for.
- **`hasCoordinates` / Header:** a station object with `undefined` coordinates
  renders no location line and no Radar card; a valid one renders both.

### Coverage this design does not claim

The env→`Deps` wiring inside `startAPIServer` has **no test seam** — `main.go`
reads the environment directly and constructs `Deps` inline, the same gap
`ENABLE_RADAR` has today. `task ci` does not reach it: `ci` is `repo:ci`,
`go:ci`, `node:ci`, `python:ci`. The repo's `task smoke` is not a substitute — it
`requires: vars: [IMAGE]`, `docker run`s a built image, asserts only `/healthz`
and the UI root, and runs as a separate CI stage that legitimately skips when
there is no open PR. This design **accepts the gap** rather than inventing a gate
that does not exist.

One cheap, optional addition: extend `task smoke`'s script to assert
`GET /api/capabilities` returns `{"forecast":false,"radar":false,"almanac":false}`
for a default container. It is a two-line change in a file that already boots the
image — but it is not load-bearing, and it does not run under `task ci`.

### Verification gates

`task ci` is the whole gate. On the UI side the repo's contract is
`npx tsc -b --noEmit` (`.taskfiles/node.yml`) — a bare `npx tsc --noEmit` silently
no-ops against this repo's solution-style root tsconfig and reports success while
checking nothing (verified: it lists zero files). After any local `npm run build`
in `web/`, restore `web/dist/.gitkeep` (Vite's `emptyOutDir` deletes it, and
`//go:embed all:dist` needs the directory non-empty).

## Out of scope

Named explicitly so nobody implements this expecting them:

- **#61** — server-side WeatherFlow→Contract-C response shaping.
- **#62** — a WeatherFlow token source usable while the UI is served.
- **RADAR_SITE→UI wiring** — the client-side site lookup that would let the Radar
  card call `/api/radar/{site}` at all. Deferred from Workstream 2.
- **Gating `/api/station`** — it stays registered and unchanged. This design only
  stops the UI from *trusting* its response shape.
- **Any health or readiness endpoint change.**
- **#149** — single-sourcing the Contract C JSON shapes between Go and
  TypeScript. This design pins its own three keys with a golden fixture; the
  25-field `CurrentObservation` is the real exposure and is #149's job.

The first three are prerequisites for an *enabled* card to display real data.
This design ships the gate; it does not pretend to fix them.

## Corrections folded in from the Gate 1 review

Recorded so the next reader knows which claims were wrong before they were right:

1. The prescribed `[caps?.forecast === true]` dependency form failed this repo's
   ESLint (`--max-warnings 0`). Now hoisted to consts.
2. "Extends `proxy_test.go`" understated three breaking tests. Now enumerated with
   the exact fix.
3. The cited "run-the-binary smoke gate" does not exist as described and is not
   part of `task ci`. The gap is now stated honestly.
4. Fail-closed with a single fetch made one transient failure permanent for the
   life of the page. A bounded retry was added.
5. `/api/station` returns **200**, not 401, with an empty bearer — so `station` is
   truthy-but-empty. This corrects §2, explains the Radar mount and the `NaN`
   header line, and contradicts the earlier claim that `/api/station` "backs no
   card".
6. `ENABLE_RADAR` is documented in `deploy/`, not in README.md or CLAUDE.md; the
   original three documentation surfaces were wrong.
7. The drift-guard test's claim was overstated and is now downgraded.
8. `registerProxy` no longer takes a redundant `capabilities` argument.
9. The forward-compatibility rationale ("an older UI against a newer server")
   described an impossible deployment — the UI is `go:embed`ed into the same
   binary (`web/embed.go`). The absent-key behaviour is retained; only its
   justification changed, to a stale already-open browser tab.

## Corrections folded in from the Gate 2 review

The golden fixture and its tsconfig claim were added *after* Gate 1 and got their
first adversarial pass at Gate 2. Both needed correcting:

10. **`resolveJsonModule` was never required.** The option is absent from
    `web/tsconfig.app.json` but already implied by `moduleResolution: "bundler"`,
    verified by probe. The earlier "it is not set today (verified)" conflated
    *absent from the file* with *not in effect*. No tsconfig change is made.
11. **The fixture pin is typecheck-only.** Vite resolves JSON natively and
    `import type` is erased, so a vitest run alone cannot catch a renamed key.
    Stated explicitly now, because it changes which command is the actual gate.
12. **A hanging capability fetch would have blocked first paint indefinitely.**
    The settle-gate handles rejection but not a request that never returns. A
    timeout was added.
13. **The retry predicate and the no-double-load property have one gap each** —
    documented above rather than papered over: `capabilities === null` is
    load-bearing, and the Retry-then-success path costs one extra load.
