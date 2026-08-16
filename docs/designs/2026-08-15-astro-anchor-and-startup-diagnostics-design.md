# Design: solar-noon anchoring, a single obliquity, and two startup diagnostics

**Status:** proposed
**Date:** 2026-08-15
**Branch point:** `017fdfa` (`main`, clean)
**Issues:** #166, #167, #163, #165, #164 — the five follow-ups deferred from PR #162
**Extends:** `docs/designs/2026-08-13-weatherflow-token-and-shaping-design.md` (§9 in particular)

---

## 1. Summary

PR #162 made the UDP-mode appliance tokenless. Its final adversarial review
returned zero blocking issues and five findings that were deliberately left out
of that branch to keep it inside its approved scope. They ship here as one
branch, correctness first.

Two are defects in `internal/astro`; two add startup diagnostics that extend the
degrade-loudly contract in the previous design's §9; one is an inconsistency in
`deploy/.env.example`.

**Two things in this document contradict the issues they implement.** Both were
measured, and §2 records how:

- **#166's suggested fix is wrong.** It repairs the four Pacific anomalies and
  breaks Auckland and McMurdo. The correct anchor is the *solar* meridian, not
  the local clock.
- **#167's fix is necessary but not sufficient.** Deduplicating ε does not stop
  the bug from returning; a test has to pin ε, and this design adds one.

### 1.1 Scope and classification

| # | Kind | Changes a contract? |
|---|---|---|
| #166 | bug fix | **No.** `SunriseSunset`'s doc comment already promises "the calendar date that t falls on IN t's OWN LOCATION". The code did not deliver that. Output changes wherever skew ≥ +12 or < −12 — the four IANA zones the issue names, **plus `lon = −180` at any offset**, plus any `(coordinate, timezone)` pair a operator can configure into that band. See §3.1.1. |
| #167 | refactor + one new test | **No.** The two ε copies agree to ≤1e-12 over 1900–2100 (measured), so the refactor is behaviour-preserving by construction. |
| #163 | feature | **Yes.** A new §9 row. Today any non-empty `RADAR_SITE` mounts the card. |
| #165 | feature | **Yes.** A new §9 row *and* a new severity §9 does not currently have: mounted-but-degraded. |
| #164 | docs / example only | No. |

### 1.2 Non-goals

- No change to `decideUI`'s never-exits property, and no new fatal path. See §7.
- No normalisation of `RADAR_SITE` (no case folding, no ICAO `K`-stripping). §5.
- No change to the ±90 s solar tolerance or the 1e-9 structural thresholds.
- No forecast provider (#81 owns that), and no change to `TOKEN`'s meaning.
- No new Go or npm dependency. `CGO_ENABLED=0` is unaffected.

---

## 2. Verification log

Everything below was run first-hand in this session against a copy of
`internal/astro` in a scratch module, plus the repo's own 46 astro tests copied
in unmodified. **Scope is stated per claim**; §2.4 lists what was *not* checked.

### 2.1 #166 exists, and is worse than "an edge case"

A sweep of **39 IANA zones × 365 days of 2026** (14,235 zone-days; 13,999 of
them with a datable event, the remaining 236 being polar nil/nil days), each
zone paired with one plausible real settlement coordinate. Two independent
success criteria were applied:

- **Criterion A** — the returned event's local calendar date equals the asked
  local date. Undefined on nil/nil days, so those are excluded.
- **Criterion B** — solar noon on the anchored UTC date carries the asked local
  date. Well defined on *every* day, polar included.

| Variant | B: wrong anchor-days | A: wrong event-days |
|---|---|---|
| current (`main`) | **1460** — Apia, Chatham, Kiritimati, Tongatapu, 365/365 each | 1460 |
| #166's suggested fix | **380** — Pacific/Auckland 190, Antarctica/McMurdo 190 | 260 |
| solar-anchor (this design) | **0** | **0** |

Both criteria agree on all three variants, which is the reason for running two:
criterion A alone could have been an artefact of dating a polar-partial event
that legitimately sits far from solar noon. There were **zero** polar-partial
days in this zone set, so that concern does not arise — but it was checked
rather than assumed.

### 2.2 Why #166's suggested fix fails

The issue proposes deriving the UTC anchor from `t`'s **local clock noon**:

```go
y, mo, d := time.Date(y, mo, d, 12, 0, 0, 0, t.Location()).UTC().Date()
```

That rolls the anchor back a day whenever the **legal** offset exceeds +12,
regardless of where the sun actually is. Two real zones do exactly that while
their **solar** offset stays under 12:

| Zone | Legal offset | Solar offset (lon/15) | Skew | Suggested fix |
|---|---|---|---|---|
| Pacific/Auckland (NZDT) | +13.00 | +11.65 | +1.35 | anchors a day early, 190 days/yr |
| Antarctica/McMurdo (summer) | +13.00 | +11.11 | +1.89 | anchors a day early, 190 days/yr |

The review that produced #166 verified its fix against **Denver (skew +1.00)**
and **Sydney (skew −0.08 in AEST, +0.92 in AEDT)** — two zones where the
local-clock and solar anchors coincide, so the check could not have separated
them. This is not a criticism
of the finding, which is real; it is why the acceptance criteria for this design
required re-verification rather than inheritance.

### 2.3 The three defects' fixes, measured

**#166 solar anchor.** Bit-identical to today's output on **12,775 of 12,775**
non-anomalous zone-days, and on **all 17 Appendix A.3 vectors** driven exactly
as the shipped suite drives them (noon UTC, `time.UTC` location). The only
changed days are the four anomaly zones. Independent USNO confirmation:

| Site / date | USNO (`aa.usno.navy.mil/api/rstt/oneday`) | current | #166's fix | solar anchor |
|---|---|---|---|---|
| Kiritimati 2026-08-14, tz +14 | Rise 06:29, Set 18:40 | **08-15** 06:29 / 18:39 | 08-14 ✓ | **08-14 06:29 / 18:39 ✓** |
| Apia 2026-08-14, tz +13 | Rise 06:43, Set 18:21 | **08-15** 06:42 / 18:21 | 08-14 ✓ | **08-14 06:42 / 18:20 ✓** |
| Auckland 2026-01-15, tz +13 | Rise 06:18, Set 20:42 | 01-15 06:17 / 20:42 ✓ | **01-14** ✗ | **01-15 06:17 / 20:42 ✓** |

All deviations are ≤1 min, inside the ±90 s bound.

**USNO is the source of record, and the three values above are already
captured.** They were queried live from `aa.usno.navy.mil/api/rstt/oneday`
earlier in this session. **That API subsequently began returning HTTP 500 to
every request, including its own documented example** — Gate 1 could not
re-query it. So the plan must transcribe the three rows from this document
rather than re-fetching, exactly as the prior design's Appendix A is
transcribed.

**Do not substitute another provider for a new row.** `api.sunrise-sunset.org`
was calibrated against five shipped A.3 rows and reports day lengths
**160–230 s longer** than USNO (Denver rise −83 s / set +120 s; London −124 s /
+104 s; Sydney −86 s / +73 s; Quito −69 s / +99 s; Singapore −73 s / +94 s). A
row sourced from it would very likely breach the ±90 s tolerance that §1.2
forbids relaxing. It is adequate for confirming which *day* an event falls on —
which is all #166 is about — and was used that way, but it cannot arbitrate a
clock value. If a further vector is wanted and USNO is still down, defer the row
rather than sourcing it elsewhere.

**Boundary robustness, for real zones.** `shift` changes only where skew crosses
±12 h. Across all 39 zones × 365 days × 4 times of day, the closest any
*realistic* pair comes to that boundary is **9.22 h** (America/Adak). So for a
sanely-configured station the result is insensitive both to DST and to what hour
of day the caller passes — the almanac handler passes `now.In(loc)`, an
arbitrary instant.

**Correctness over the whole reachable input space.** The 9.22 h margin above is
a statement about plausible deployments, and on its own it would be too weak:
`STATION_LONGITUDE` and `STATION_TIMEZONE` are set independently, so an operator
can pair *any* longitude with *any* zone — lon 0 with `Pacific/Kiritimati`
(skew +14), lon 180 with a UTC−12 zone (skew −24). The formula was therefore
driven over an arbitrary grid: **105 quarter-hour offsets (−12:00 … +14:00) ×
73 longitudes (−180 … +180 step 5) × 9 latitudes (−89 … +89) × 7 dates =
482,895 combinations.**

**Anchor errors: 0, at every latitude.** Criterion B holds everywhere, so the
derivation genuinely makes no assumption that the pairing is geographically
sensible.

Criterion A does report events landing on an adjacent local date under such
pairings — but always with a *correct* anchor, and at every latitude including
the equator (13,536 cases at lat 0). That is inherent rather than a defect: pair
UTC+14 with longitude 0 and local midnight is nowhere near solar midnight, so
sunrise legitimately falls outside the local calendar day that contains solar
noon. This is also why criterion A alone is not a sufficient test at high
latitude, and why §2.1 uses both.

**The degenerate boundary is benign.** At exactly skew = ±12 h — reachable only
through an implausible pairing — `floor` resolves to 0 on one side and +1 on the
other, but both branches produce **identical instants** (verified at
`off −12/lon 0`, `off +12/lon 0`, `off 0/lon ±180`). So no tie-break rule is
needed and none is specified.

**#167 the two ε copies agree today.** Max
`|ε_declination − ε_recovered_from_y|` over Julian Days spanning 1900–2100
(step 37 d) is **≤1e-12°** (measured 7.11e-15° on re-run). Gate 1 strengthened
this: comparing `math.Float64bits` of the outputs before and after the refactor
over **197,433 samples** across 1900–2100 gives **zero differences** — the
deduplication is not merely within tolerance, it is *bitwise* identical.

**#167 the blind spot, quantified.** Mutation-probing the repo's real 46-test
astro suite, one perturbation at a time:

| Mutation | Suite result |
|---|---|
| ε in `solarPosition`'s declination copy `+1e-6°` | **caught** |
| ε in `solarIntermediates`' equation-of-time copy `+0.05°` | passes |
| … `+0.2°` | passes |
| … `+0.3°` | passes |
| … `+0.5°` | **caught** |

A five-order-of-magnitude asymmetry, exactly as #167 predicts: the anchor's
`referenceDeclination` is computed independently, while `referenceEqTime`
consumes the same `y` the implementation does, so `got` and `want` move together.

**#167 the refactor alone does not close it.** With `eps` returned and the
duplicate deleted, perturbing the single ε by `1e-6°` is caught. But the
*future* regression is not: re-inline a **correct** obliquity for declination
while the shared ε drifts `+0.2°`, and **all 46 tests pass**. This is why §4
adds a test rather than relying on the existing anchor. That new test catches
the re-inlined mutant and produces no false positive on correct code — both
verified.

**#164 the distances.** Parsed all **163** entries from
`internal/radar/sites.go` and computed great-circle distance to the example
coordinates (39.7392, −104.9903): **FTG 38 km**, CYS 158 km, PUX 158 km, GLD
285 km, GJX 287 km, **TLX 838 km**. Both figures in the issue confirmed.

**#165 the blast radius is Go-only.** `station.Location` has exactly one
consumer family — `internal/httpserver/almanac.go` (`almanacClock`,
`toTempRecord`, `almanacDateLabel`) and `windows.go` (`almanacWindows`).
`astro.SunriseSunset` has exactly one caller, `almanac.go:102`. On the client,
`AlmanacResponse.sunrise`/`sunset` are `string | null` preformatted values
(`web/src/types/weather.ts:140–142`) and `AlmanacCard` performs no date math.

Correction from Gate 1: an earlier draft said "the only `toLocale*` call in
`web/src` is in `Header.tsx`". There are two — `Header.tsx:17`
(`toLocaleTimeString`) and `SolarUVCard.tsx:85` (`toLocaleString()` on a
*number*, so not a date at all) — and `ForecastStrip.tsx:18` does client-side
date math on the dead forecast card. None touches the almanac, so the conclusion
holds; the enumeration was wrong.

### 2.4 Not verified

- **Chatham and Tongatapu were not checked against USNO**, only against the
  sweep's internal criteria. Kiritimati, Apia and Auckland were. The plan may
  add USNO rows for the other two; nothing in this design depends on them.
- **The 39-zone coordinate set is representative, not exhaustive**, so the
  9.22 h figure is a measured margin across the zones sampled rather than a
  proof about all real deployments. The *correctness* claim does not rest on it:
  the 482,895-combination arbitrary grid in §2.3 covers the full reachable input
  space independently, including the pairings no real place uses. (Gate 1
  reproduced the 9.22 h at `America/Adak` exactly, and independently confirmed
  the anchor over 378,105 `(offset, lon)` pairs at 0.1° resolution.)

- **"Self-consistent with what they configured" does not hold across a DST
  transition within 1 h of the ±12 h boundary.** `t.Zone()` is read at the
  *request* instant, not at solar noon, so with
  `STATION_TIMEZONE=Pacific/Auckland` and `STATION_LONGITUDE=15` two requests on
  the same local date return different days' sun times across the 2026-04-05
  transition (`00:30` → offset +13, `shift = 1`; `12:30` → offset +12,
  `shift = 0`). This needs a pairing whose skew sits within an hour of ±12,
  which no real deployment has (measured margin 9.22 h), so it is **not** a
  reason to change the design — but the claim is scoped rather than flat.
- **The `task ci` baseline is closed for Go only.** Gate 1 ran `task go:ci` on
  `main`@`017fdfa`: **exit 0** — fmt clean, `go mod tidy -diff` clean,
  golangci-lint 0 issues, govulncheck "No vulnerabilities found", and
  `go test ./... -race -tags integration` green in every package. The
  `node:ci` and `python:ci` halves were **not** run (they can write into the
  tree, and the reviewer is read-only). The implementation plan must still run
  the full `task ci` bare before task 1.
- The four astronomy claims in the prior design's **Appendix B** were not
  re-measured; nothing here depends on them.

---

## 3. #166 — anchor on the solar meridian

### 3.1 The defect

`SunriseSunset` reads Y/M/D from `t`'s own location and then uses them as a
**UTC** date:

```go
y, mo, d := t.Date()
jd := julianDay0(y, int(mo), d)
midnightUTC := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
```

Solar noon at longitude `lon` on UTC date `U` falls at `U + (12 − lon/15)` hours,
which is inside UTC date `U` for `lon ∈ (−180, 180]` — the interval is **open at
the lower end**, because `lon = −180` puts solar noon at exactly `U + 24 h`, the
first instant of the next UTC day. Expressed in the station's zone, its local
date is `U + floor((12 + skew)/24)` where `skew = offset − lon/15`. The current
code assumes that floor term is zero. It is zero exactly when `−12 ≤ skew < 12`.

### 3.1.1 The defect is not confined to four zones

`shift` is a function of skew, not of zone identity, so the shipped code is
wrong wherever skew leaves that band. Three distinct populations:

1. **The four IANA dateline anomalies** the issue names (Apia, Chatham,
   Kiritimati, Tongatapu) — skew ≈ +24.5.
2. **`lon = −180`, at any offset including UTC.** Measured, `lat 0`,
   `2026-06-21`, `time.UTC`:
   `shift = 1`; shipped returns `2026-06-21T17:58:14Z / 2026-06-22T06:05:37Z`,
   the fix returns `2026-06-20T17:58:01Z / 2026-06-21T06:05:24Z`, and the fix is
   the value the contract requires. `lon = −179.9999999` and `lon = +180` both
   give `shift = 0` and are unaffected — the discontinuity sits precisely on the
   closed endpoint.
3. **Configured pairings** in the band, which `LoadStation` accepts because it
   validates each variable's range independently and cannot check their mutual
   consistency. `STATION_TIMEZONE=Etc/GMT+12` (a valid IANA name) with
   `STATION_LONGITUDE=179` gives skew −23.93 → `shift = −1`; the fix returns the
   asked local date, the shipped code returns the day before.

Population 2 matters out of proportion to its likelihood, because it is
reachable **in the existing UTC-driven vector table** and therefore costs
nothing to pin. See §9.1.

### 3.2 The fix

```go
func SunriseSunset(lat, lon float64, t time.Time) (sunrise, sunset *time.Time) {
	y, mo, d := t.Date()
	// Anchor on the UTC date whose SOLAR noon at lon carries t's LOCAL date.
	// shift is 0 wherever the legal offset is within 12 h of the solar
	// offset, which is every ordinary zone. It is:
	//   +1 for the dateline anomalies whose legal calendar runs a day ahead
	//      of the sun (Kiritimati, Apia, Tonga, Chatham), AND at lon == -180
	//      exactly, where solar noon lands on the first instant of the next
	//      UTC day -- note the interval is open at the lower end;
	//   -1 for a configured pairing that runs a day behind, e.g.
	//      STATION_TIMEZONE=Etc/GMT+12 with STATION_LONGITUDE=179. The two
	//      variables are validated independently, so such pairs are reachable.
	//
	// Deriving the anchor from t's local CLOCK noon instead looks equivalent
	// and is not: that rolls back whenever the LEGAL offset exceeds +12,
	// which breaks Auckland in NZDT and McMurdo in summer, whose SOLAR
	// offsets are under 12. See design §2.2.
	_, offsetSec := t.Zone()
	shift := int(math.Floor((float64(offsetSec)/3600.0 - lon/15.0 + 12.0) / 24.0))

	// shift is in {-1, 0, +1} for every input LoadStation can produce, because
	// parseFloatEnv rejects a non-finite or out-of-range lon. time.Date then
	// normalises d-shift across month, year and leap boundaries, so no guard
	// is needed. (A caller passing an unvalidated lon of 1e9 yields a huge
	// shift and an absurd year -- no panic, and unreachable through config.)
	midnightUTC := time.Date(y, mo, d-shift, 0, 0, 0, 0, time.UTC)
	ay, amo, ad := midnightUTC.Date()
	jd := julianDay0(ay, int(amo), ad)

	return refineEvent(jd, midnightUTC, lat, lon, true),
		refineEvent(jd, midnightUTC, lat, lon, false)
}
```

`math` is already imported. Nothing downstream of `midnightUTC` changes:
`refineEvent`, the no-clamp contract on the minute offset, and the polar guard
are all untouched.

### 3.3 Doc comment

The existing comment's *contract* is already correct and stays. Two additions:
that the anchor is derived from the solar meridian and why, and that
`shift` is 0 for every ordinary zone. The paragraph about either event being
independently nil is unchanged — that property is a `refineEvent` concern.

### 3.4 Rejected alternatives

- **Local clock noon** (the issue's suggestion). Wrong; §2.2.
- **Trial-and-select** — compute for `D−1`, `D`, `D+1` and keep whichever result
  lands on local date `D`. Correct, and robust to pathological offsets, but
  triples the work and replaces a closed form with a search for a case with
  9.22 h of margin. KISS: the closed form earns its place; the search does not.
- **Change the signature** to take an explicit UTC date and push the problem to
  the caller. Moves the defect into `almanac.go` rather than fixing it, and
  breaks the function's documented local-date contract.

---

## 4. #167 — one obliquity, and a test that pins it

### 4.1 The change

`solarIntermediates` returns ε alongside `l0, m, e, y`; `solarPosition` consumes
it and its duplicate block (`sun.go:94–97`) is deleted. Net ≈ −5 lines.

```go
func solarIntermediates(jd float64) (l0, m, e, y, eps float64) {
	...
	eps = eps0 + 0.00256*math.Cos(rad(omega))   // note: '=', not ':=' --
	y = math.Tan(rad(eps/2)) * math.Tan(rad(eps/2))  // eps is a named return
	return l0, m, e, y, eps
}
```

Four existing call sites in `sun_test.go` (`:162`, `:193`, `:211`, `:218`) gain a
fifth `_`. This is the DRY criterion met exactly: one piece of knowledge — the
corrected mean obliquity of the ecliptic — with one authoritative representation.

### 4.2 The test the refactor needs

Deduplication alone does not prevent recurrence (§2.3). The guard is a new
anchor over the same `probeDates` the existing anchor uses:

```go
// referenceObliquity is an INDEPENDENT transcription of A.1 step 9, written
// from the design rather than derived from sun.go. Reconciling it to match a
// changed implementation defeats the check -- see the anchor's own warning.
func referenceObliquity(jd float64) float64 {
	tc := (jd - 2451545.0) / 36525.0
	omega := 125.04 - 1934.136*tc
	sec := 21.448 - tc*(46.8150+tc*(0.00059-tc*0.001813))
	return 23.0 + (26.0+sec/60.0)/60.0 + 0.00256*math.Cos(rad(omega))
}

func TestSolarIntermediates_ObliquityIsAnchored(t *testing.T) {
	for _, jd := range probeDates {
		_, _, _, y, eps := solarIntermediates(jd)
		if want := referenceObliquity(jd); math.Abs(eps-want) > 1e-9 { ... }
		// Ties the equation-of-time path to the SAME eps. Without this a
		// second obliquity series feeding y would hide behind +-90 s.
		tan := math.Tan(rad(eps / 2))
		if want := tan * tan; math.Abs(y-want) > 1e-12 { ... }
	}
}
```

**Both assertions are load-bearing, and they catch different mutants.** Do not
drop either — this is the specific way an implementer could satisfy §9.2's
letter and lose half the coverage:

| Mutant | Existing 46 tests | Which assertion fires |
|---|---|---|
| **M1** shared ε drifts `+1e-6°` | FAIL (declination anchor) | **assertion 1** |
| **M2** a *correct* obliquity re-inlined for declination, shared ε drifts `+0.2°` — the recurrence of the original bug | pass | **assertion 1** |
| **M3** returned ε correct, but `y` built from a *second* series (nutation term dropped) | pass | **assertion 2** |

Assertion 1 pins ε's *value* and is what catches the recurrence (M2).
Assertion 2 pins the fact that `y` — and therefore the whole equation of time —
is built from **that** ε rather than from a second series, and is the only thing
that catches M3. An earlier draft of this design claimed assertion 2 was the one
guarding M2; that is inverted, and Gate 1 caught it.

**Threshold note.** `1e-9` matches the existing anchor's threshold. `1e-12` on
`y` is not tight: the measured maximum `|y − tan²(ε/2)|` is **1.1e-18** over
`probeDates` and **3.5e-18** over 1900–2100, and even that is an FMA-contraction
artifact of the test's own subtraction on arm64 rather than a real difference.
Six orders of headroom, so it is not a flaky test. Neither threshold is the
±90 s accuracy tolerance and neither may be relaxed to accommodate a change.

---

## 5. #163 — validate `RADAR_SITE` at startup

A `RADAR_SITE` that is not a WSR-88D code is **malformed**, not absent, so it
must be reported. It is still not fatal, because it is a UI flag precondition
(§7). `main.go` already imports `internal/radar`.

A fourth case in `decideUI`'s radar switch, after the `nil` case and before the
coordinate case:

```go
case !radar.IsValidSite(*station.RadarSite):
	d.Reasons = append(d.Reasons, fmt.Sprintf(
		"ENABLE_RADAR is true but RADAR_SITE=%q is not a known WSR-88D site "+
			"code. It must match one of the 163 codes in internal/radar/sites.go "+
			"exactly -- three uppercase letters, and usually not the ICAO form "+
			"(TLX, not KTLX). The radar card will not be mounted.",
		*station.RadarSite))
```

Ordering matters: the site check precedes the coordinate check so an operator
with both problems is told about the one they can see in their own config file
first.

A Go expression-less `switch` runs only the first matching case, so an operator
with an invalid site *and* absent coordinates learns about them one restart at a
time. That is pre-existing (the radar switch already behaves this way for
absent-site vs absent-coordinates) and this design does not change it — but it
does sit against the philosophy `TestDecideUI_ReportsEveryUnmetPrecondition`
states at `main_test.go:345`, which is about reporting every unmet precondition
**across cards** in one startup. §9's aggregation rule likewise applies to
`LoadStation`'s fatal errors, not to `decideUI` reasons. Noted, deliberately not
fixed here: collapsing the switch into independent `if`s is a separate change
with its own message-ordering consequences.

**No normalisation.** `RADAR_SITE` accepts exactly the 163 codes in
`internal/radar/sites.go`, matching `radar.IsValidSite` — which is already what
the per-request handler enforces at `internal/httpserver/radar.go`. Folding case
or stripping a leading `K` would make the accepted set differ from the site
table and send the sidecar a value the operator never typed. The diagnostic
names the likely mistake instead.

**Why the message says "usually" not the ICAO form.** The table *does* contain
two valid codes beginning with `K` — `KJK` and `KSG` (`sites.go:157–158`). A flat
"strip the leading K" hint would mislead their operators, and a
strip-then-validate implementation would be outright wrong for them. The message
therefore points at the table as the authority and offers `KTLX` only as an
example.

---

## 6. #165 — warn when coordinates are set and the timezone is not

### 6.1 Why this needs a new channel

`config.LoadStation` defaults `Location` to `time.UTC`. An operator who sets
coordinates and forgets `STATION_TIMEZONE` gets a Denver station rendering
**"Sunrise 2:17 PM · Sunset 11:39 PM"** on the December solstice — the exact
wrong-timezone display the server-side preformatting decision exists to prevent,
reintroduced from the default side.

(Issue #165 states this as "Sunset 1:39 AM". That is wrong and was inherited
here before Gate 1 caught it: A.3's Denver December-solstice sunset is
`2026-12-21T23:39:00Z`, which `almanacClock`'s `"3:04 PM"` layout renders in UTC
as `11:39 PM`. Measured with `time.Format`. The corrected value must be what
reaches `CLAUDE.md` and any commit message.)

The almanac card **mounts and works**. So this cannot be a `decideUI` reason:
those are logged as `"optional UI card not mounted"` at ERROR, and here the card
*is* mounted. Reusing them would make a degraded card indistinguishable from an
absent one in logs.

### 6.2 The change

`uiDecision` gains a second slice with a distinct severity:

```go
type uiDecision struct {
	Almanac   bool
	Radar     bool
	RadarSite *string
	// Reasons are logged at ERROR: an enabled card that will NOT be mounted.
	Reasons []string
	// Warnings are logged at WARN: a card that IS mounted, but degraded by a
	// default the operator probably did not intend. Never gates routing.
	Warnings []string
}
```

In the almanac's `default` branch, where `d.Almanac = true` is set:

```go
default:
	d.Almanac = true
	if !station.TimezoneConfigured {
		d.Warnings = append(d.Warnings,
			"ENABLE_ALMANAC is true and coordinates are set, but STATION_TIMEZONE "+
				"is not: sunrise and sunset will render as UTC clock times, the "+
				"Today/This Week/This Month/This Year windows will use UTC calendar "+
				"boundaries, and the record date labels (\"Today\", \"Jan 2\") will "+
				"be UTC-dated. Set STATION_TIMEZONE to the station's IANA zone "+
				"(e.g. America/Denver).")
	}
```

The date labels are named explicitly because `almanacDateLabel`
(`internal/httpserver/almanac.go:144`) renders in `loc` too, and both the issue
and the current docs mention only the windows.

In `startAPIServer`, the warning loop runs **after** the existing reason loop, so
a startup that has both prints every not-mounted card first and then every
degraded one — severity-ordered rather than interleaved:

```go
for _, reason := range decision.Reasons {
	slog.Error("optional UI card not mounted", "reason", reason)
}
for _, w := range decision.Warnings {
	slog.Warn("optional UI card degraded", "warning", w)
}
```

`Warnings` never influences `deps` or capability reporting — the almanac is
mounted and `capabilities.almanac` is true, which is correct: the card works, it
is just on the wrong clock.

### 6.3 Distinguishing unset from explicitly UTC

`StationConfig` gains `TimezoneConfigured bool`, set in the existing
`STATION_TIMEZONE` branch of `LoadStation`. An operator at a genuinely-UTC
station (Reykjavík, Accra) who sets `STATION_TIMEZONE=UTC` gets no warning,
which is right — they made a choice.

A boolean rather than making `Location` nillable: the #162 design deliberately
established that `Location` is never nil and never serialised, and every
*serialised* field is a pointer because unset must be distinguishable from a
legitimate zero. `Location` is not serialised, so it is not in that family.
Adding one unexported-in-effect boolean is the smaller change and preserves the
invariant that every consumer can dereference `Location` unguarded.

`TimezoneConfigured` is set only on a **successful** `LoadLocation`. A malformed
zone is already fatal, so the false case unambiguously means "not configured".

### 6.4 Scope

The warning fires only when the almanac is actually mounted. `Location` has no
other consumer (§2.4 records how that was checked): the radar card does not use
it, and `/api/station` does not serialise it. Warning a Postgres-only deployment
that will never render an almanac would be noise.

---

## 7. The degrade-loudly contract, extended

This extends §9 of `2026-08-13-weatherflow-token-and-shaping-design.md`. The
governing rule is unchanged and non-negotiable:

> **No UI feature flag can prevent the appliance from starting or ingesting.**

`startAPIServer` runs before `listenAndPushWithSink`; `log.Fatal` exits past the
deferred `cleanupResources`; compose sets `restart: unless-stopped`. A fatal
path for a card flag is a crash loop that stops UDP ingest into SQLite and
Litestream. **An invalid `RADAR_SITE` is a `decideUI` reason, never a fatal.**

The table below restates the prior design's five rows, adds the **two bold new
rows**, and makes explicit one row that is real pre-existing behaviour but was
never written down (`ENABLE_*` boolean malformed → `log.Fatal` via
`ParseBoolEnv`). It is *not* a new behaviour and must not be described as one.

It is still not the complete set of radar failure modes: a **missing radar
sidecar** is deliberately not checked at startup and surfaces as failing tile
requests at runtime, as `CLAUDE.md` already documents. This design does not
change that, and the row is omitted here rather than silently folded in.

| Condition | Behaviour |
|---|---|
| `ENABLE_FORECAST=true` (no provider until #81) | ERROR naming #81; route unregistered; `capabilities.forecast=false` |
| `ENABLE_ALMANAC=true`, coordinates absent | ERROR naming the missing vars; route unregistered; `capabilities.almanac=false` |
| `ENABLE_ALMANAC=true`, no observation store (Postgres-only) | ERROR at startup; route unregistered; `capabilities.almanac=false` |
| **`ENABLE_ALMANAC=true` and mountable, `STATION_TIMEZONE` unset** | **WARN naming the variable; route registered; `capabilities.almanac=true` — the card is mounted and works, on UTC** |
| `ENABLE_RADAR=true`, `RADAR_SITE` or coordinates absent | ERROR naming the missing vars; route unregistered; `capabilities.radar=false` |
| **`ENABLE_RADAR=true`, `RADAR_SITE` present but not a known WSR-88D code** | **ERROR naming the offending value; route unregistered; `capabilities.radar=false`** |
| Any `STATION_*` value present but **malformed** | fatal at startup — operator error, not an unconfigured feature |
| Any `ENABLE_*` boolean malformed | fatal at startup — same rule *(pre-existing; documented here for the first time, not introduced)* |

The new WARN row is the only entry that is neither "not mounted" nor "fatal", and
it is deliberately a third severity: absent configuration that has a *usable*
default still deserves to be visible, because the default is silently wrong for
most operators who reach it.

**Migration.** Nothing that starts today becomes unstartable. A deployment
running `ENABLE_RADAR=true` with `RADAR_SITE=KTLX` goes from "card mounted, every
tile 400s, no diagnostic" to "card not mounted, ERROR names the value" — it stays
up and keeps ingesting either way. A deployment with coordinates and no
`STATION_TIMEZONE` behaves exactly as before and gains one WARN line.

---

## 8. #164 — `deploy/.env.example`

`RADAR_SITE=TLX` (Oklahoma, 838 km away) is replaced by `RADAR_SITE=FTG`
(38 km), so the shipped example is internally consistent with the Denver
coordinates the same file offers. An operator who uncomments the `STATION_*`
block and sets `ENABLE_RADAR=true` then gets a working card rather than an empty
map at `DEFAULT_ZOOM = 7` — **provided they also start the sidecar**, which is
behind a compose profile and does not come up by default
(`docker compose --profile radar up -d`, `deploy/docker-compose.yml:11–12`). The
comment in `.env.example` already says this; the design notes it so "working
card on first run" is not read as one step when it is two.

The issue leans the other way — comment the line out so #163's new diagnostic
fires. Rejected: the documented happy path should work when followed literally.
The diagnostic still exists for operators who set their own site and get it
wrong, which is the case it was written for.

The comment above it gains a pointer to `internal/radar/sites.go` and a note
that the value must match the example coordinates, not just be valid.

---

## 9. Test plan

### 9.1 New astronomy vectors

Two groups, and the distinction matters because only one of them needs new
machinery.

**Group 1 — the antimeridian row, which the existing table already supports.**
An earlier draft claimed `shift` is "always 0" in a `time.UTC` location and that
a non-zero-shift row therefore *cannot* be expressed in the existing `vectors`
table. That is false: at `lon = −180` exactly, `shift = 1` with offset 0
(§3.1.1). So a row at `lat 0, lon −180, 2026-06-21` pins the new branch with **no
zone machinery at all** — add it to `vectors` as-is. Expected under the fix:
`2026-06-20T17:58:01Z / 2026-06-21T06:05:24Z`.

**This row needs no external source, which matters while USNO is down.** `−180`
and `+180` are the *same meridian*, so the two must return identical instants —
a physical invariant, not a transcribed expectation. Today they do not:

| `lat 0, 2026-06-21`, UTC | sunrise | sunset |
|---|---|---|
| `lon = +180`, shipped **and** fixed | `2026-06-20T17:58:01Z` | `2026-06-21T06:05:24Z` |
| `lon = −180`, **shipped** | `2026-06-21T17:58:14Z` | `2026-06-22T06:05:37Z` |
| `lon = −180`, **fixed** | `2026-06-20T17:58:01Z` | `2026-06-21T06:05:24Z` |

So the strongest form of this test is a **paired assertion** — drive both
longitudes and require equality — rather than a hard-coded pair. It is
self-validating, immune to a tolerance argument, and fails loudly on today's
code.

**Group 2 — a real dateline row, which does need a zone.** The reason is not
that non-zero `shift` is unreachable in UTC; it is that reproducing the
Kiritimati/Apia failure requires a **+13/+14 legal offset**, which no `time.UTC`
row can carry.

Use `time.FixedZone("LINT", 14*3600)` rather than `time.LoadLocation`. The file
already establishes that pattern at `sun_test.go:111`
(`time.FixedZone("AKST", -9*3600)`); `SunriseSunset` reads only `t.Zone()`, so a
fixed zone exercises the identical path; it needs no `_ "time/tzdata"` import;
and — matching A.3's pinned-vector discipline — it cannot change meaning when a
tzdata update revises a zone's rules, which for these very locations is not
hypothetical (Samoa moved across the dateline in 2011).

**Table mechanics.** Adding a field to the anonymous struct at
`sun_test.go:32` makes all 17 existing positional literals fail to compile —
`too few values in struct literal`. "Preserving all 17 rows verbatim" is
therefore not achievable; the plan must choose explicitly between appending
`, nil` to each of the 17 rows, converting them to keyed literals, or adding a
separate zoned table with its own driver. State which, and why.

**Auckland is the load-bearing row.** It is the one that fails under #166's
suggested fix and passes under this design, so it is what stops a future
"simplification" back to the local-clock derivation.

All 17 existing A.3 rows must keep passing unmodified — verified inert in §2.3,
to be re-verified in execution rather than inherited from this document.

### 9.2 New obliquity anchor

`TestSolarIntermediates_ObliquityIsAnchored` per §4.2. Its acceptance evidence is
a mutation probe, not a green run — a green run alone proves nothing here, which
was the whole defect. **Two probes are required, and each must be shown firing
the assertion bound to it in §4.2's table**, because one probe alone would let an
implementer ship a single assertion and lose the other's coverage:

| Probe | Must fail via |
|---|---|
| Re-inline a *correct* obliquity for declination; drift the shared ε by `+0.2°` | assertion 1 (`eps` vs `referenceObliquity`) |
| Leave the returned ε correct; rebuild `y` from a second series (drop the nutation term) | assertion 2 (`y` vs `tan²(ε/2)`) |

Record both exit codes. Neither probe may be satisfied by the existing 46 tests —
both pass them today, which is exactly why the new test exists.

### 9.3 `TestDecideUI` cases

| Case | Expected |
|---|---|
| `ENABLE_RADAR=true`, `RADAR_SITE="KTLX"`, coordinates set | `Radar=false`, `RadarSite=nil`, one reason containing `KTLX` |
| `ENABLE_RADAR=true`, `RADAR_SITE="TLX"`, coordinates set | `Radar=true` (unchanged; guards against over-rejecting) |
| `ENABLE_ALMANAC=true`, coordinates set, store present, `TimezoneConfigured=false` | `Almanac=true`, `Reasons` empty, one warning naming `STATION_TIMEZONE` |
| `ENABLE_ALMANAC=true`, coordinates set, store present, `TimezoneConfigured=true` | `Almanac=true`, `Reasons` and `Warnings` both empty |
| `ENABLE_ALMANAC=true`, coordinates **absent**, `TimezoneConfigured=false` | one reason (not mounted), **no** warning — an unmounted card is not degraded |

The last row matters: the warning must not fire for a card that was never
mounted, or the log says two contradictory things about the same card.

**The existing harness must be restructured first, or two of these cases will
pass without ever running.** `main_test.go:327–332` ends the empty-reasons branch
with a bare `return`:

```go
if len(tc.wantReasons) == 0 {
    if len(got.Reasons) != 0 { t.Errorf(...) }
    return          // <-- anything added below is unreachable for these cases
}
```

Rows 3 and 4 above have **no** reasons, so a `wantWarnings` assertion added in
the natural place — after the reasons block — never executes for them. TDD does
not catch this on its own: before the field exists the test fails to *compile*,
and once the field exists but the `append` does not, the case goes green with the
assertion skipped. The plan must (a) remove the early `return` or hoist the
warnings assertion above it, and (b) require the new case to be demonstrated
**failing** against an implementation that has the `Warnings` field but not the
`append`.

Two smaller harness notes: row 2 duplicates the existing `everything_configured`
case (`main_test.go:236` already uses `site := "TLX"`), so it can be dropped or
folded in; and row 1's `RadarSite=nil` expectation needs a new assertion, because
the existing one only checks `RadarSite` when `!tc.flags.Radar`
(`main_test.go:325`).

### 9.4 Config

`LoadStation` tests for `TimezoneConfigured`: unset → false; `UTC` → true with
`Location == time.UTC`; a valid zone → true; a **malformed** zone → still fatal,
aggregated with any other malformed value, and `TimezoneConfigured` **false**
(it is set only in the success branch, `station.go:122–124`).

The `UTC` case is the one that justifies the field's existence:
`time.LoadLocation("UTC")` returns the `time.UTC` pointer itself, so no
comparison on `Location` can distinguish "unset" from "explicitly UTC".

### 9.5 Standing constraints

- Every implementation subagent uses TDD (`subagent-development-workflow.md`
  Addition 1), regardless of what a task says.
- **Never assert the four almanac windows are distinct.** `today.From ==
  week.From` every Sunday, `== month.From` on the 1st, and the year joins on
  1 Jan — an "all four differ" assertion fails ~1 day in 6. Compare positionally
  against `almanacWindows`' own output.
- Run `task ci` **bare** and capture `$?` on the next statement. `${PIPESTATUS[0]}`
  expands to empty in this shell; never read an exit status through a pipe.
- Run the baseline `task ci` before task 1. `main` has been red before.

---

## 10. Documentation

| File | Change |
|---|---|
| `CLAUDE.md` | Add the two §9 rows to the degrade-loudly prose. **Both** `RADAR_SITE` mentions need the validation note — `:135` under `ENABLE_RADAR` (which currently says only that a *missing* site is checked, accurate today and incomplete after this change) and `:162` under Station Identity ("WSR-88D site code (e.g. `TLX`)"). `STATION_TIMEZONE` (`:163`): say the UTC default affects **sunrise/sunset clock strings and the record date labels**, not only calendar windows — the doc currently understates this, and the corrected example is "Sunset 11:39 PM", not the issue's "1:39 AM". |
| `main.go` — `decideUI`'s doc comment | It currently describes the mechanism as "ERROR log / route unregistered / capability false" and will be incomplete once a WARN channel exists that leaves the route registered. Add the second severity. |
| `deploy/.env.example` | `RADAR_SITE=FTG` + pointer to `internal/radar/sites.go`; the same timezone wording fix. |
| `docs/designs/2026-08-13-weatherflow-token-and-shaping-design.md` | A pointer noting §9 is extended by this document. Do not edit §9's table in place — the prior design is the record of what #162 shipped. |

---

## 11. Open questions

None. The three decisions that were open — #165's channel, #163's strictness,
#164's direction — were settled before this document was written and are
recorded in §6.1/§6.3, §5 and §8 respectively.

---

## 12. Gate 1 disposition

`sr-eng-review` (opus/max, read-only) reviewed this document cold, rebuilt the
astronomy in scratch modules, and independently replicated §2's measurements. It
returned **3 blocking, 9 significant, 12 minor**. All are addressed above. I
re-verified every blocking finding and the significant ones that asserted a
number, rather than accepting them from the report.

### Falsified claims, corrected

| Was | Now | Where |
|---|---|---|
| "In a `time.UTC` location … `shift` is always 0", so a dateline row cannot live in the existing table | False — `shift = 1` at `lon = −180`. The shipped code is wrong there too, and the row **can** live in the existing table | §3.1.1, §9.1 |
| "(empty meaning UTC, preserving all 17 rows verbatim)" | Impossible in Go — positional literals must supply every field | §9.1 |
| "solar noon … always inside UTC date `U` because `lon ∈ [−180, 180]`" | Interval is `(−180, 180]` | §3.1 |
| "The second assertion is the load-bearing one" | Inverted — assertion 1 catches the recurrence mutant, assertion 2 catches a different one. Both required | §4.2, §9.2 |
| "Sunset 1:39 AM" (inherited from #165) | **11:39 PM** | §6.1 |
| "Sydney (skew −0.90)" | −0.08 AEST / +0.92 AEDT | §2.2 |
| "the only `toLocale*` call in `web/src` is in `Header.tsx`" | Two exist; neither touches the almanac | §2.3 |
| `shift` is ±1 "only for … Kiritimati, Apia, Tonga, Chatham" | Also `lon = −180`, and `−1` via configurable pairings | §3.2 doc comment |
| Nonsensical pairs are "self-consistent with what they configured" | Not across a DST transition within 1 h of the boundary | §2.4 |

The `1:39 AM` and `always 0` errors are the two that would have propagated into
shipped artefacts — the first into `CLAUDE.md`, the second into a plan that
built zone machinery it did not need while leaving a free regression row
unwritten.

### Confirmed by independent replication

Nine of §2's quantitative claims reproduced exactly: 1460, 380/260, 236 polar
days, 12,775 identical zone-days, 9.22 h at Adak, all 17 A.3 vectors inert, the
five-row mutation table, 163 sites, FTG 38 km / TLX 838 km. The reviewer also
built the full proposed change and ran it: `go build` OK, 47/47 tests pass,
`golangci-lint run` 0 issues under the repo's own config.

### Still open, carried into the plan

- Chatham and Tongatapu have no USNO vector, and USNO is currently down (§2.3).
- `node:ci` / `python:ci` baselines unmeasured (§2.4).
- Whether `internal/astro`'s test binary genuinely lacks tzdata could not be
  demonstrated on macOS. §9.1's move to `time.FixedZone` makes the question moot
  rather than answering it.
