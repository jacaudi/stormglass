# Design: an actionable `RADAR_SITE` rejection, and tests that prove the startup diagnostics are emitted

**Status:** proposed (revised after the Gate 1 adversarial review)
**Date:** 2026-08-16
**Branch point:** `ca8386e` (`main`, clean, v4.0.0 released)
**Issues:** #169, #170, and #171 as a rider
**Extends:** `docs/designs/2026-08-15-astro-anchor-and-startup-diagnostics-design.md`,
whose §9 degrade-loudly contract these changes serve rather than alter.

> **Revision note.** The first draft of this document was reviewed adversarially by
> implementing it in a scratch tree and running it. Five findings were blocking. Three
> claims in the original were **measured false** and are corrected here, marked
> **[corrected]** where they appear; §10 records them so the errors are not silently
> absorbed. Two design decisions changed as a result (§3.3 and §5).

---

## 1. Summary

Three follow-ups deferred from PR #168's fix wave, taken as one branch because
two of them collide.

- **#169** makes the rejected-`RADAR_SITE` diagnostic actionable. Today it
  points a container operator at `internal/radar/sites.go`, a file they cannot
  open. It will instead name the nearest WSR-88D site and its distance. The
  same change retires `radar.NearestSite`, which is dead code — its only caller
  is its own test (`internal/radar/select_test.go:9`).
- **#170** closes a hole in the test suite: deleting either log loop in
  `startAPIServer` breaks nothing today, because every assertion in
  `TestDecideUI` is on `decideUI`'s *return value*. The entire user-visible
  deliverable of #165 is that log line.
- **#171** adds the two dateline test vectors (Chatham, Tongatapu) that were
  blocked on USNO's API being down.

**Why one branch.** #169 rewrites the exact reason string #170 asserts is
emitted. Landed separately, whichever merges second rewrites the other's
assertions. #171 does not collide with either — it is a rider taken to avoid
spending a whole branch/PR/CI cycle on two table rows.

**Behaviour changes are confined to the diagnostics.** The card is still not
mounted on rejection, the capability still reports false, the route is still
unregistered, and nothing gains a path that can exit. §3.3 does change *how many*
reasons an operator sees in one specific case; that is deliberate and scoped
there.

---

## 2. Constraints carried forward

Settled by prior designs and reviews. Not reopened here.

1. **`decideUI` must never exit.** `startAPIServer` runs before
   `listenAndPushWithSink`; `log.Fatal` exits past the deferred
   `cleanupResources`; compose sets `restart: unless-stopped`
   (`deploy/docker-compose.yml`). A fatal path for a flag that only decides
   whether a card renders is a crash loop that stops UDP ingest into SQLite and
   Litestream. Gate 1 on the #163 design caught a proposal to make this fatal;
   the rejection stands.
2. **No `RADAR_SITE` normalisation.** Do not case-fold, do not strip a leading
   `K`. `internal/radar/sites.go` contains `KJK` and `KSG`, valid site codes
   beginning with K. Settled in the #163 design.
3. **The message must still name the offending value.**
4. **A malformed value stays fatal in `config.LoadStation`.** Absent means "the
   operator did not configure this feature"; malformed means "the operator tried
   and got it wrong". Unchanged.

---

## 3. #169 — an actionable rejection message

### 3.1 `NearestSite` returns the distance it already computes

```go
// internal/radar/select.go

// NearestSite returns the Code of the WSR-88D site closest to (lat, lon) and
// the great-circle distance to it in kilometres.
//
// It assumes lat/lon are finite and in range; config.LoadStation
// (internal/config/station.go:158-163) rejects NaN, +/-Inf and out-of-range
// values before any coordinate reaches decideUI. On a non-finite input every
// haversine comparison is false and the zero Site is returned as ("", +Inf).
func NearestSite(lat, lon float64) (code string, distanceKm float64)
```

`NearestSite` already computes `minDist` and discards it. Returning it is the
whole change, and it is what gives the function a production caller for the
first time. No new function, no exported `haversineKm`, no threshold constant.

`TestNearestSite` updates to the two-value form and asserts both the code and
the distance.

The degenerate-input note is in the doc comment rather than a runtime guard
because the precondition is enforced at the boundary and now lives across a
package boundary on an exported two-value signature — a reader of
`internal/radar` cannot see `config.LoadStation`.

### 3.2 The two message shapes

**With coordinates** (the normal case — the radar card requires them anyway):

```
ENABLE_RADAR is true but RADAR_SITE="KTLX" is not a known WSR-88D site code.
Codes are three uppercase letters, usually not the ICAO form (TLX, not KTLX).
The nearest site to your coordinates is FTG, 38 km away. The radar card will
not be mounted.
```

**Without coordinates** — the same text minus the nearest-site sentence:

```
ENABLE_RADAR is true but RADAR_SITE="KTLX" is not a known WSR-88D site code.
Codes are three uppercase letters, usually not the ICAO form (TLX, not KTLX).
The radar card will not be mounted.
```

Distance renders as whole kilometres (`%.0f`). The phrasing is deliberately
neutral — it states a fact, it does not say "try this" — so that a station far
from the NEXRAD network gets a self-evidently useless number rather than bad
advice. See §3.4.

The `internal/radar/sites.go` pointer is removed from the runtime message. It
survives in `deploy/.env.example`, whose reader has the repository open.

> **[corrected] Two different Denvers.** The `38 km` above is the
> `deploy/.env.example` pair (39.7392, −104.9903 → 38.4 km). The `TestDecideUI`
> fixture this design modifies is `lat, lon := 39.74, -104.98`
> (`main_test.go:235`) → **37.5 km → renders `37`**. Both measured. Do not
> transcribe `38` into a test assertion. §6 avoids the trap by asserting on
> `"FTG"` rather than on a distance.

### 3.3 Bad site *and* no coordinates: report both **[decision changed]**

`decideUI`'s radar block is currently a `switch` with no `fallthrough`
(`main.go:537-558`), so **exactly one arm ever runs**. The unknown-code arm
precedes the coordinate arm, so an operator with both problems reaches it with
no coordinates from which to compute a hint — and learns about only one of their
two problems.

The first draft kept that ordering and justified it as avoiding "two restarts to
learn two things". **That justification was false**: with a single-select
`switch`, either ordering costs the same two restarts, just in the opposite
order. The review measured `len(Reasons) == 1` for the bad-site case and named
an option the draft never considered.

**Resolution: report every unmet radar precondition, in one startup.**

```go
if flags.Radar {
    ok := true
    if station.RadarSite == nil {
        ok = false
        d.Reasons = append(d.Reasons, /* RADAR_SITE is not set */)
    } else if !radar.IsValidSite(*station.RadarSite) {
        ok = false
        d.Reasons = append(d.Reasons, /* unknown code, + hint when hasCoords */)
    }
    if !hasCoords {
        ok = false
        d.Reasons = append(d.Reasons, /* no centre for the map */)
    }
    if ok {
        d.Radar = true
        d.RadarSite = station.RadarSite
    }
}
```

`RadarSite == nil` and `!IsValidSite(*RadarSite)` remain mutually exclusive, so
they stay chained; the coordinate check becomes independent. This is the
property `TestDecideUI_ReportsEveryUnmetPrecondition` asserts across the three
flags, now holding *within* the radar flag too.

**Blast radius: no existing test row changes its reason count.** Each of
`radar_without_a_site`, `radar_with_an_unknown_site` and
`radar_without_coordinates` has exactly one unmet precondition. Only the new
bad-site-without-coordinates row produces two.

**[corrected] Where the nil-coordinate guard lives.** An earlier revision of
this section claimed the `hasCoords` check "is what prevents `*station.Latitude`
from nil-dereferencing". That was true only of a sketch that built the hint
inline. In the shape that ships, the message builder takes `lat, lon *float64`
and nil-checks them itself — which it must, because the coordinate reason and
the unknown-site reason are now independent, so the builder is called with no
coordinates whenever both are wrong. `hasCoords` in `decideUI` therefore guards
no dereference; it exists solely to decide whether the coordinate reason is
appended. Gate 2 caught the claim by observing that `decideUI` never
dereferences `station.Latitude` at all.

**Owned explicitly:** the operator with no coordinates still gets no hint, and
#169 removes the `sites.go` pointer that was previously their only lead, so that
one message carries strictly less than today. Under this resolution they at
least learn, in the same startup, that their coordinates are missing — which is
the thing they must fix before a hint is possible at all.

### 3.4 Why there is no distance ceiling

Considered and rejected. A ceiling requires a number, and no defensible one
could be established: the coverage radius of the `N0B`/`N0Q` products the
sidecar serves (`internal/radar/proxy.go:99-101`) is **unverified**. Five
sources were attempted across the design and its review; the reachable ones
disagreed with each other about which figure attaches to which product. No
number appears in this design as a result. See §9.

A stated distance conveys the same information without the constant. An
operator reading "the nearest site is PLA, 2540 km away" draws the correct
conclusion unaided; a second message shape would only restate the number in
words. `decideUI` also has no business ruling on radar coverage physics.

`decideUI`'s signature is unchanged — it already receives `station.Latitude`
and `station.Longitude`.

---

## 4. #170 — a logger seam, and a test that fails when the loops are deleted

### 4.1 The seam

```go
func startAPIServer(mode Mode, station config.StationConfig, sw *sqlite.Writer, logger *slog.Logger) *http.Server
```

`main` passes `slog.Default()` at the existing call site (`main.go:709`).
`configureOTel` runs before it (`main.go:703`), so when `ENABLE_OTEL=true` the
value passed is the `teeHandler`-backed logger.

All four `slog.*` calls inside the function move to the injected logger
(`main.go:602, 605, 629, 632`) — the two diagnostic loops, the `http server`
error inside the serve goroutine, and the `http server listening` line.
Injecting a logger and then leaving half the function reaching for the global
would be worse than not injecting one.

**The equivalence, stated as an invariant rather than as "byte-identical".**
Passing `slog.Default()` once at the call site is equivalent to the current
implicit package-level calls *only while nothing calls `slog.SetDefault` after
`startAPIServer` returns*. That holds today: the sole production `SetDefault` is
`main.go:425` inside `configureOTel`, and `runBackfill`/`runHealthcheck` never
reach `startAPIServer`. It is recorded here because a future `SetDefault` would
break it silently and no test would notice.

**Why injection rather than `slog.SetDefault` into a buffer.** The alternative
needs no production change, but it mutates a process global, forecloses
`t.Parallel`, and leaks: `slog.SetDefault` also calls `log.SetOutput` and
`log.SetFlags(0)`, and restoring the original logger does **not** undo them,
because the original's handler is the unexported `*defaultHandler` and the
restore takes `SetDefault`'s skip branch (`log/slog/logger.go:62-73`). Verified
against the stdlib source, not inferred from the doc comment. Nothing in the
suite asserts on stdlib `log` state today, so the leak is latent rather than
active — but it is a trap set for the next test author, and the parameter costs
one line at one call site.

### 4.2 The test

`TestStartAPIServer_EmitsDecisionDiagnostics`, driving the real function with a
real listener.

**Inputs come from two different places, and the first draft conflated them.**
`startAPIServer` reads only four names from the environment; `STATION_*` is
**not** among them — station identity arrives as the `station` parameter, filled
by `config.LoadStation()` at `main.go:646`.

| Input | Kind | Value | Why |
|---|---|---|---|
| `mode` | **parameter** | `ModeUDP` | anything else returns nil at `main.go:577` before logging |
| `station` | **parameter** | `config.StationConfig{Latitude: &lat, Longitude: &lon}` | coordinates set, `TimezoneConfigured` false |
| `sw` | **parameter** | real `*sqlite.Writer` | `hasStore` true, so the almanac mounts and warns |
| `logger` | **parameter** | `slog.New(slog.NewTextHandler(&buf, nil))` | observable output |
| `HTTP_ADDR` | env | `127.0.0.1:0` | ephemeral port; no conflict with a running container |
| `ENABLE_FORECAST` | env | `true` | one ERROR reason, requiring no other configuration |
| `ENABLE_ALMANAC` | env | `true` | with the `station` above, one WARN warning |
| `ENABLE_RADAR` | env | `false` | **hermeticity** — unset, an ambient `ENABLE_RADAR=yes` makes `ParseBoolEnv` error and `log.Fatal` kills the test binary with no output (`main.go:592-595`) |

> **[corrected]** The first draft listed `STATION_LATITUDE`/`STATION_LONGITUDE`
> as environment inputs "producing" a met almanac precondition. Run that way it
> produces **two ERRORs and zero WARNs**, and the warning assertion is
> unsatisfiable.

The warning path is only reachable with a store, so the test constructs a real
writer: `sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"),
sqlite.LoadConfig(func(string) string { return "" }))` — verified to yield
`BatchSize=100, FlushInterval=10s, BusyTimeout=5s`, no hang, no panic — then
`sqlite.NewWriter(ctx, db, cfg)`. `NewWriter` starts a goroutine, so `t.Cleanup`
calls `Close(ctx)` and then closes the DB.

**Tradeoff, stated rather than left implicit:** a bare `&sqlite.Writer{}` also
passes, because nothing dereferences it at construction — `httpserver.New` only
stores it in `Deps.Observations`. The real writer is chosen anyway so the test
does not silently depend on that remaining true. The cost is one temp file and
two cleanup steps.

**The listener is unavoidable, not chosen.** `startAPIServer` calls
`ListenAndServe` unconditionally. (`TestRunHealthcheck` also uses a real
listener, but for the different reason that the healthcheck contract *requires*
dialing a socket.)

Assertions, after an explicit `srv.Close()` — see below:

1. A record at `level=ERROR`, message `optional UI card not mounted`, with a
   `reason` attribute mentioning `ENABLE_FORECAST`.
2. A record at `level=WARN`, message `optional UI card degraded`, with a
   `warning` attribute mentioning `STATION_TIMEZONE`.
3. **Ordering**: the ERROR record's index in the buffer precedes the WARN
   record's, pinning "every not-mounted card before every degraded one".

**Ordering by buffer index is sound, with one caveat that must be closed.** The
three synchronous emissions pass through one `commonHandler` whose `Handle`
serialises on a shared mutex; `sqlite.Writer`'s goroutine uses package-level
`slog` and never reaches the buffer; `slog.NewTextHandler(&buf, nil)` defaults
to `LevelInfo`, so all three levels appear. **But** the serve goroutine's
`http server` error (§4.1) writes the same unsynchronised `bytes.Buffer`. It
only fires if `net.Listen` fails, so it is not observed in practice — the test
calls `srv.Close()` before reading `buf.String()`, and no explicit wait is
needed because once `Close` has run every path through
`ListenAndServe`/`Serve` returns `http.ErrServerClosed`, which the goroutine
filters out, so it never writes.

### 4.3 The non-vacuity proof

A passing test is not evidence the test is non-vacuous. During execution, the
loops are mutated and each result recorded:

| Mutation | Expected | Which assertion catches it |
|---|---|---|
| Delete the `decision.Reasons` loop | FAIL | 1 |
| Restore; delete the `decision.Warnings` loop | FAIL | 2 |
| Restore; **swap the two loops** | FAIL | 3 |
| Restore all | PASS | — |

The swap row is what makes assertion 3 non-vacuous; neither deletion can fail
it. Each run's output is captured in the task report. This is the acceptance
criterion for #170, not a nice-to-have.

---

## 5. #171 — two dateline vectors **[scope changed]**

Two rows added to `TestSunriseSunset_DatelineZones` in
`internal/astro/sun_test.go`: Chatham (+12:45) and Tongatapu (+13). The test's
doc comment (`sun_test.go:147-148`), which records that Tonga and Chatham lack a
row, is updated.

### 5.1 The table's offset field must change first

The table's field is `offsetHours int` (`sun_test.go:172`), consumed as
`time.FixedZone("TEST", tc.offsetHours*3600)` (`:208`). Chatham is **+12:45**,
so `offsetHours: 12.75` is a compile error — `cannot use 12.75 (untyped float
constant) as int value in struct literal`. "Add rows only" is not sufficient.

**Rename the field to `offsetSec int`** and rewrite the three existing rows'
values in `N * 3600` form:

```go
offsetSec int          // was: offsetHours int
loc := time.FixedZone("TEST", tc.offsetSec)   // was: tc.offsetHours*3600

offsetSec: 14 * 3600,        // kiritimati_utc_plus_14
offsetSec: 13 * 3600,        // apia_utc_plus_13
offsetSec: 13 * 3600,        // auckland_nzdt_must_not_shift (control)
offsetSec: 12*3600 + 45*60,  // chatham_utc_plus_12_45      (new)
offsetSec: 13 * 3600,        // tongatapu_utc_plus_13       (new)
```

This does not violate #171's constraints. The frozen struct that rule names is
the 18-row `vectors` table (`sun_test.go:34-95`), which is untouched; these rows
are the separate local `DatelineZones` table. The rows are **keyed** literals,
so this is a field rename, not the positional-literal breakage #171 warns about.
No row's *expected values* change — no vector is edited to make a test pass.

### 5.2 What the Chatham row actually proves **[corrected]**

The first draft claimed Chatham "exercises the `shift` arithmetic on a
fractional `offsetSec/3600`". **Measured, that is false.** Chatham
(−43.95, −176.55) on 2026-08-14 returns byte-identical instants at all three
candidate offsets:

| offset | rise (UTC) | set (UTC) |
|---|---|---|
| +12:00 | `2026-08-13T18:43:47.210954380Z` | `2026-08-14T04:58:44.620453731Z` |
| +12:45 | *identical* | *identical* |
| +13:00 | *identical* | *identical* |

`sun.go:58` computes `shift := int(math.Floor((offsetSec/3600.0 - lon/15.0 +
12.0)/24.0))`; for this longitude all three offsets land inside the same `floor`
bucket (1.490, 1.522, 1.532 → all `1`). The assertion cannot distinguish them,
so the row is not evidence about fractional-offset handling — and an implementer
who fell back to `offsetHours: 12` would get a green test.

**The row is still worth adding**, for what it does prove: Chatham is one of the
four zones whose legal calendar runs a day ahead of the sun, and this row is a
regression guard on anomaly-zone *date selection* — pre-#166 code returned local
2026-08-15, the shipped code returns 2026-08-14. That is the coverage gap #171
was filed to close.

A test that genuinely exercises fractional-offset arithmetic needs an
`(offset, lon)` pair whose `shift` expression straddles a `floor` boundary
within 0.03125. Finding one is **not in scope here**; it is recorded in §8 as a
candidate follow-up.

### 5.3 Coordinates, and re-measurement

**Coordinates must be pinned in this design**, because #171's table gives only
zone/date/times and a USNO query needs a lat/lon. Recovered by matching #171's
quoted deltas against the shipped code, and independently reproduced:

| Point | Coordinates | rise Δ vs #171 | set Δ vs #171 |
|---|---|---|---|
| Chatham | −43.95, −176.55 | −12.8 s | −15.4 s |
| Tongatapu | −21.13, −175.20 | −8.3 s | −26.5 s |

Both match #171's quoted deltas exactly, which establishes that the issue's
numbers were computed against *this* code at *these* coordinates. A nearby
alternative (Chatham at −44.03, −176.57) yields +1.4 s / −19.9 s — still inside
tolerance, but it would disagree with the issue's table.

**The USNO values are re-measured, not transcribed.** Copying another agent's
measurement into a test that exists to *be* evidence is a failure mode this
project has hit repeatedly. The plan re-queries USNO live at the coordinates
above and compares against #171's table; a disagreement is a blocking finding,
not something to reconcile by picking one.

**If USNO is unreachable at execution time, #171 drops out of the branch** and
stays open. It touches a different package from #169 and #170.

### 5.4 Constraints, all from #171 and all load-bearing

- **Never edit an existing row's expected values to make a test pass** — a
  vector is evidence, not a knob. A failing row means the code is wrong. (The
  §5.1 rename changes field *names*, not expectations.)
- Do not touch the 18-row `vectors` struct; adding a field there breaks all 18
  positional literals.
- Do not tighten the ±90 s tolerance or the `1e-9`/`1e-12` thresholds.
- `time.FixedZone`, not `time.LoadLocation`, matching the existing rows.
- Do not substitute another provider for USNO; `api.sunrise-sunset.org`
  measured 160–230 s wide on day length, which would breach the bound.

---

## 6. Test and documentation changes outside the new tests

**`TestDecideUI` needs a negative-assertion field.** The table has exactly two
assertion fields (`main_test.go:251-252`), both positive substring containment.
There is no way to express "must not contain". Add:

```go
notWantReasons []string // substrings no ERROR reason may contain
```

with its loop alongside the existing one at `main_test.go:364-371`. Without it
the new row in §3.3 silently degenerates into a duplicate of
`radar_without_a_site`.

**`radar_with_an_unknown_site` changes.** It currently asserts the reason
contains `"sites.go"`. That substring is deliberately removed, so `wantReasons`
becomes `{"ENABLE_RADAR", "KTLX", "FTG"}` — asserting the site *code*, not a
distance, so the 38-vs-37 km trap in §3.2 cannot bite.

This is worth stating plainly because it superficially resembles the thing §5.4
forbids. It is not: that rule protects *astronomical vectors*, external evidence
about the physical world. This is an assertion about a message string whose
specification is the thing #169 changes. Nothing outside this repository knows
what the message says.

**A new row is added** for bad-site-without-coordinates:
`wantReasons: {"ENABLE_RADAR", "KTLX", "STATION_LATITUDE"}` (both reasons now
appear, per §3.3), `notWantReasons: {"nearest site"}`.

**`CLAUDE.md`** describes the rejection as "an ERROR names it", which stays
true. A sentence noting the nearest-site hint is optional polish.

---

## 7. Verification

1. `task ci` green from a clean tree. It is the entire static gate and exactly
   what `ci-test.yml` runs. If a worktree was removed during the branch,
   `golangci-lint cache clean` first — stale entries report phantom findings
   against deleted paths.
2. The #170 mutation matrix in §4.3 — all three mutations run, all three
   failures observed and recorded, plus the restored PASS.
3. `rtk proxy go test ./internal/astro/ -count=1 -run
   TestSunriseSunset_DatelineZones -v` shows **5 subtest `--- PASS:` lines**, up
   from 3, named `kiritimati_utc_plus_14`, `apia_utc_plus_13`,
   `auckland_nzdt_must_not_shift`, `chatham_utc_plus_12_45`,
   `tongatapu_utc_plus_13`. Count the *subtests* — with `-v` the parent test
   prints its own `--- PASS`, so a naive count is 6. `rtk` rewrites `go test`
   stdout, so `--- PASS:` markers do not survive a plain pipe; `rtk proxy` is
   required.
4. A branch-wide diff check that **no row in the 18-row `vectors` table**
   (`sun_test.go:34-95`) was modified. Scoped to `vectors` deliberately: the
   `DatelineZones` table *is* modified, by §5.1.
5. Per-job CI conclusions read on the PR, never the `ci` rollup.

Exit-code discipline throughout: `cmd; echo "EXIT=$?"` as **one** unpiped
invocation, reading the echoed text. `$?` does not survive between tool calls
and `${PIPESTATUS[0]}` is empty in this shell.

---

## 8. Non-goals

- No `RADAR_SITE` normalisation (constraint 2).
- No fatal path anywhere; `decideUI` and `startAPIServer` stay non-fatal for
  card-flag problems (constraint 1).
- No distance ceiling and no coverage threshold (§3.4).
- **No warning for a *valid* site that is far from the station.** A valid but
  distant code mounts a card centred on the station showing a radar that does
  not cover it. Arguably a real gap, but it is a new diagnostic on a path that
  works, not part of making a rejection actionable. Not filed.
- **No fractional-offset `shift` test.** §5.2 establishes that the Chatham row
  does not provide one and describes what would. Candidate follow-up issue; not
  built here.
- No change to `deploy/.env.example`'s `sites.go` reference (§3.2).
- **No OTel work** — filed separately as #172. These diagnostics already reach
  the OTel log bridge whenever `ENABLE_OTEL=true`, via the tee.
- No engagement with PR #44 (kochj23's `logging.Init()`). The logger parameter
  in §4.1 is orthogonal: it changes how `startAPIServer` obtains a logger, not
  how the process-wide default is configured, which is the decision #44 forces.

---

## 9. Claims this design does not verify

Recorded rather than flattened into facts.

- **The coverage radius of the `N0B`/`N0Q` products is not established.**
  Attempted sources: `roc.noaa.gov` product list (404), `weather.gov/tg/radprod`
  (404), NCEI C00708 landing page (no mnemonic table), plus two secondary
  sources that contradicted each other — one attaching 460 km to super-resolution
  reflectivity and 230 km to legacy velocity, another attaching the 460 km figure
  to *composite* reflectivity (`N0Z`), which this appliance does not request.
  **No figure is quoted anywhere in this design**, and §3.4 does not depend on
  one.
- **#171's USNO values have not been checked against USNO.** What *is* verified
  is that the shipped code reproduces #171's quoted deltas exactly at the §5.3
  coordinates — which shows the deltas were computed against this code, not that
  the USNO times are right. §5.3's re-measurement step remains necessary.
- **`rtk proxy`'s stdout rewriting was not tested**; §7.3 transcribes it from
  #171.
- **`task ci` has not been run.** `go build ./...`, `go test ./...` and
  `golangci-lint run ./...` were run against a scratch copy with §3 applied: all
  clean apart from the single expected `TestDecideUI` failure, and `gocyclo` 15
  is not breached by the §3.3 restructure.

Measured directly, with a haversine matching `select.go:37-45` against the
parsed 163-entry table: Denver (`.env.example`)→FTG **38.4 km**; Denver
(`main_test.go` fixture)→FTG **37.5 km**; Denver→TLX **837.6 km**;
London→**PLA 2540.1 km**.

---

## 10. Corrections to the first draft

Kept so the errors are not silently absorbed.

1. **§7 of the draft asserted "London→PBZ 5986.6 km" as a nearest-site
   measurement, and §3.4 built its worked example on it.** The London→PBZ *pair*
   distance is correct, but `NearestSite(51.5074, -0.1278)` returns **`PLA`
   (Lajes Field, Azores) at 2540.1 km**. A measured pair distance was widened
   into an unmeasured nearest-site claim — inside the one section whose purpose
   is separating the two. The §3.4 conclusion survives; the example is corrected.
2. **§4.2's fixture listed `STATION_*` as environment inputs.**
   `startAPIServer` never reads them. Run as written the test produces two
   ERRORs and zero WARNs, making two of its three assertions unsatisfiable.
3. **§5 claimed the Chatham row exercises fractional-offset `shift`
   arithmetic.** All three candidate offsets return byte-identical instants
   (§5.2). The rationale is replaced with what the row actually proves.
4. **§3.3's justification for the switch ordering did not hold** — a
   single-select `switch` costs the same two restarts either way, and it cited
   `TestDecideUI_ReportsEveryUnmetPrecondition` for a property that test does not
   have. The design decision changed as a result (§3.3).
5. **§3.4 quoted 230 km / 460 km figures** while §7 declared the same fact
   unverified. The figures are removed.
