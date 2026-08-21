# Dashboard responsive layout — design

- **Date:** 2026-08-19
- **Status:** proposed (revised after Gate 1)
- **Closes:** #177, #178, #179, #180, #181, #182 (narrow half only — see §5.1), #183, #184 (worker only — see §6.10), #188
- **Explicitly out of scope:** #185, #186, #187 — see §3.2
- **Follow-ups filed during design:** #190, #191
- **Branch point:** `main` @ `fb52e3f`

**Revision note.** Gate 1 found six blocking defects, all real, and the largest was that §6.4
prescribed one mechanism while quoting numbers produced by another. Every number in §6 and §7 has
been re-measured against the CSS this document actually specifies. §14 records the corrections.

---

## 1. Summary

The dashboard was never designed across a width range. It has two breakpoints, eight independent
implementations of "a label above a value", and a grid whose span classes are absolute while its
column count is variable. The result is silent data loss between 561px and 900px, 24% of a laptop
screen unused, and cards that align differently from one another for no reason but the order they
were written in.

Three root causes produce all nine filed issues. Fixing them ticket-by-ticket would rewrite the
same CSS three times, which is why they are designed as one system.

Measured result of the whole change, at nineteen widths from 2560 to 320:

| | today | after |
|---|---|---|
| widths with clipped text | 8 of 19 (up to 30 leaves) | **0 of 19** |
| hero content overflow | up to 296px | **0px at every width** |
| dead gutter at 1512 | 360px (23.8%) | **54px (3.6%)** |
| lopsided empty space at 1512 | 216.5px | **117.7px** |
| page horizontal overflow at 320 | 17px | **0px** |
| row height spread | 0 (uniform) | **0 (uniform — unchanged)** |

---

## 2. Evidence and method

Measurements come from the running W0 stack (k8s namespace `selfhosted`, real station data)
driven by Playwright.

**Widths measured (19):** 2560, 1920, 1512, 1280, 1100, 1001, 1000, 900, 756, 660, 641, 640, 620,
561, 504, 430, 390, 360, 320. The pairs 1001/1000 and 641/640 exist to prove the breakpoints fire
where intended; 660 and 641 exist to validate the 640px choice (§6.2).

Three probe details are load-bearing and must survive into the implementation:

1. **`waitUntil: 'networkidle'` never settles** on this dashboard — it polls observations
   continuously and retries radar. Use `domcontentloaded` + `waitForSelector` + a fixed settle
   delay.
2. **Clipping is measured on leaf elements, not child boxes.** A wrapper that grows to fill its
   card reads as "no slack" while its text still sits at the top. The probe keeps only nodes with
   no element children and compares their border boxes against the card's padding box.
3. **Empty space is measured as asymmetry, not bottom slack** —
   `max(0, (padBottom − inkBottom) − (inkTop − padTop))`, summed over cards. Bottom slack alone
   punishes deliberately centred content: it scored the hero at 70px when the hero already has
   ~70px above its content too.

**Horizontal overflow is measured as `document.documentElement.scrollWidth − window.innerWidth`.**
The definition matters: `web/src/index.css:49` sets `body { overflow-x: hidden }`, which suppresses
a body-level scrollbar, and `:47` sets `min-width: 320px`, which is why 320 is the floor. Measuring
on `body` would report zero everywhere and hide the defect.

Getting (2) and (3) wrong produced two false conclusions during this design, both caught and
corrected. They are called out because §12's verification depends on them.

---

## 3. Scope

### 3.1 In scope

| # | Title | Root cause | Section |
|---|---|---|---|
| 177 | Icons and titles never scale | C | §6.8 |
| 178 | Hero stats clipped, then lost entirely | A + B | §6.2, §6.6 |
| 179 | 24% dead gutter at 1512px | C | §6.3 |
| 180 | Up to 121px empty inside a card | C | §6.4 |
| 181 | Unequal columns | A | §6.2 |
| 182 | Span collapse and hero shrink | A (narrow half only) | §6.6, §5.1 |
| 183 | Records card 3.9× taller on mobile | C | §6.7 |
| 184 | Worker 404 on every page load | D | §6.10 |
| 188 | Header renders `°W· 150m` | E | §6.9 |
| — | Almanac clips 7 leaves at 390px (**unfiled**) | C | §6.7 |
| — | Card primitive consolidation | C | §6.5 |

### 3.2 Out of scope, and why

- **#185** — the radar proxy's `io.LimitReader(resp.Body, 10<<20)` truncates large scans. Go.
- **#186** — the radar sidecar image has not booted since `4cca78b` (python 3.14; cartopy 0.25.0
  publishes no cp314 wheel). Dockerfile.
- **#187** — Pushgateway rejects the exporter's explicit timestamps. Go.

All three are Go or Dockerfile work in a branch that is otherwise entirely `web/`. **#184 is in
scope despite being radar-adjacent** because it is a Vite build defect.

**#184, #185 and #186 are three independent total failures.** Fixing any one alone leaves the card
broken. This branch makes the radar card *verifiable*, not *working*. §6.10 demonstrates this
directly: during the final verification run `/api/radar/ATX` returned 502 on every attempt,
because #185 is data-dependent and the scan had grown past 10 MiB.

### 3.3 Filed during design, deliberately not built

- **#190** — rotate the Station Almanac through metric families rather than temperature only.
- **#191** — give the temperature hero content worth its width (the half of #182 layout cannot fix).

### 3.4 Interaction with other workstreams

**W1 (repo re-org, branch `worktree-repo-reorg` @ `3e16539`).** Its committed range touches only
`.gitignore` and four documents — no Go and no `web/` source. Its *plan* additionally touches
`Dockerfile`, `taskfile.yml`, `deploy/*`, `internal/postgres/tunables.go`,
`internal/sqlite/writer.go` and **`web/README.md:60`**. That last one is the only overlap with this
branch's tree, and this branch does not edit `web/README.md`. Safe in practice; the earlier
blanket claim that "neither branch opens a file the other opens" was false and is withdrawn.

**W3 (#91, lightning card) collides directly and must be sequenced.** #91 reworks
`web/src/components/LightningCard.tsx` and removes the `.lightning-distance-rings` block — the
same file and the same CSS family that §6.5's consolidation rewrites. W1's design lists W3 as
running parallel to it. **Recommendation: #91 lands after this branch**, and is then implemented
in terms of `Stat`/`Readout` rather than against the classes it would otherwise remove by hand.
This must be agreed before either starts.

---

## 4. Root causes

### 4.1 Cause A — spans are absolute, column count is variable

`.card-span-2` and `.card-span-3` are fixed track counts; the column count is set by media
queries. When a span exceeds the current column count, CSS Grid **mints implicit columns**, sized
by `grid-auto-columns` (initial `auto`, i.e. content-sized).

Three components carry spans: `AlmanacCard.tsx:101` (`span={3}`), `ForecastStrip.tsx:24`
(`span={3}`), `RadarCard.tsx:263` (`span={2}`), plus `TemperatureHero.tsx:38` (`span={2}`). On the
measured deployment the almanac is mounted and the forecast is not (#81), so the almanac is the
active trigger — but the defect belongs to any of them.

Below 900px the grid has two explicit columns, so a third, implicit, content-sized track appears
— measured at **267.672px, and constant**. It never shrinks, so the two `1fr` tracks absorb the
entire loss:

| viewport | computed `grid-template-columns` |
|---|---|
| 901 | `271px 271px 271px` |
| 900 | `272.156px 272.172px 267.672px` |
| 756 | `200.156px 200.172px 267.672px` |
| 620 | `132.156px 132.172px 267.672px` |
| 561 | `102.656px 102.672px 267.672px` |

Proved by ablation, not inferred. Removing **only the class**, card still rendered:

| viewport | hero content overflow | tracks | records card |
|---|---|---|---|
| 900 | 0 → 0px | `272/272/268` → `416/416` | 564.3 → 852px |
| 756 | **101 → 0px** | `200/200/268` → `344/344` | 420.3 → 708px |
| 561 | **296 → 8px** | `103/103/268` → `246.5/246.5` | 225.3 → 513px |

The framing is the *relationship*, not the almanac card. Evidence: a `span → 1` clamp already
exists for the one-column grid at ≤560px, and **no equivalent `span → 2` clamp exists for the
two-column grid.** That asymmetry is the defect.

**A second, unfiled defect follows.** `.records-card { grid-column: 1 / -1 }` resolves `-1`
against the **explicit** grid's end line, so with an implicit third column present the records
card spans two of three visible columns — 420.3px inside a 708px grid.

**A third contributor was claimed here and is false — recorded so it is not re-derived.** Today's
`repeat(3, 1fr)` is indeed shorthand for `minmax(auto, 1fr)`, and that `auto` is a content-based
minimum. But per CSS Grid Level 1 §6.6 the automatic minimum size applies only when the item is
**not a scroll container**, and `.glass-card` sets `overflow: hidden` (`App.css:75`), which makes
every grid child a scroll container. Measured, identical content, three variants:

| variant | resolved tracks |
|---|---|
| `repeat(3, 1fr)`, items `overflow: hidden` | `290.656 290.672 290.656` |
| `repeat(3, minmax(0, 1fr))`, items `overflow: hidden` | `290.656 290.672 290.656` — **identical** |
| `repeat(3, 1fr)`, items `overflow: visible` (control) | `502 185 185` — the minimum does bite |

So `minmax(0, 1fr)` is a **no-op for the current DOM**. It is kept as a seam, not a fix (§6.2).

### 4.2 Cause B — the hero cannot shrink

`.hero-content` is a flex row with no `flex-wrap`. `.hero-details` carries `flex-shrink: 0`
(`App.css:315`) and a fixed `grid-template-columns: 1fr 1fr` (`:312`), so it measures **202.2px at
every viewport tested, including 2560**. `.hero-temp-block` (`:285`) has no `min-width: 0`.

`.hero-content` therefore has a minimum content width of **463px** — a 521px card. Below that it
overflows, and `.glass-card { overflow: hidden }` (`App.css:75`) hides it. The 463px figure is a
model, not a single reading: it predicts the measured overflow at four independent widths
(390 → 179px, 620 → 237px, 561 → 296px, 756 → 101px), each within 1px.

Failure is silent: there is no page-level horizontal overflow at any width above 320px, so nothing
signals that data is missing. At 390px `WIND CHILL` and `UV INDEX` are simply absent.

Cause B is independent of Cause A: it accounts for none of the 561–900px damage and all of the
≤560px damage.

### 4.3 Cause C — every card was hand-built

A census of `web/src/App.css`:

| concept | independent implementations |
|---|---|
| a label above a value | **8** — `.detail-*`, `.stat-*`, `.humidity-stat-*`, `.rain-stat-*`, `.lightning-label`/`.lightning-count`, `.health-*`, `.rstat-*`, `.almanac-hl-*` |
| the row holding them | **7** — `.wind-stats`, `.humidity-stats-row`, `.solar-section`, `.rain-grid`, `.lightning-stats-row`, `.records-grid`, `.almanac-records` |
| a scale bar with tick labels | **2** — `.gauge-track`/`.gauge-labels`, `.uv-bar-track`/`.uv-bar-labels` |
| **the card's headline number** | **6 sizes for one concept** — see below |
| **a pill / badge** | **5** — see below |

**The headline number has no scale, only eight guesses.** `.hero-temp` 4rem (`:291`);
`.wind-speed-value` 2.2rem (`:398`); `.uv-number` 2rem (`:593`); `.lightning-count` 2rem (`:791`);
`.lightning-distance` 2rem (`:798`); `.pressure-value` 1.6rem (`:534`); `.humidity-value` 1.6rem
(`:482`); `.rstat-value` 1.6rem (`:1526`). Nothing distinguishes a 2.2rem number from a 1.6rem one
except the day it was written, and none of them relates to the card's width — which is why the
pressure readout looks undersized in a 471px card at 1512px.

`.lightning-distance` and `.lightning-count` sit in the **same** `.lightning-stats-row`, so any
scale that promotes one and not the other renders two different sizes side by side in one row.
`.rstat-value` is counted here *and* assigned to `Stat` in §6.5; §6.5 resolves which wins.

`.pressure-value-block` is `display: flex; align-items: baseline` (`:527-528`) with no
`justify-content`, so the headline sits hard left. `.health-grid` (`:1120-1121`) and `.rain-grid`
(`:686`) have the same omission. These three are the visible "not centred" defects.

**The badge is copy-pasted five times, with four different shapes:**

| class | radius | padding | font-size |
|---|---|---|---|
| `.status-badge` (`:171`) | 20px | `0.3rem 0.75rem` | 0.75rem |
| `.stale-badge` (`:213`) | 10px | `0.1rem 0.4rem` | 0.7rem |
| `.rain-active-badge` (`:674`) | 10px | `0.15rem 0.5rem` | 0.8rem |
| `.lightning-alert-badge` (`:756`) | 10px | `0.15rem 0.5rem` | 0.8rem |
| `.records-window` (`:1521`) | 999px | `4px 12px` | 0.8rem |

`.rain-active-badge` and `.lightning-alert-badge` share five declarations and differ in **three**,
not one:

| | `.rain-active-badge` (`:674-683`) | `.lightning-alert-badge` (`:756-765`) |
|---|---|---|
| `background` | `var(--info-color)` | `var(--warning-color)` |
| `color` | `#fff` | `#1a1a1a` |
| `animation` | `pulse 1.5s ease-in-out infinite` (`:199`) | `flashBadge 2s ease-in-out infinite` (`:767`) |

A `tone` prop covers the two colour pairs. It does **not** cover the animation: `pulse` and
`flashBadge` are distinct keyframes at distinct durations. **`Badge` therefore needs `tone` plus
an explicit decision on the animation** — unify on one keyframe, or carry it as a second axis.
An earlier draft called these "identical apart from the background colour" and invoked
`dry-principle.md`'s unify-on-sight tier on that basis; that was wrong, and the honest reading is
Tier B — similar shape, one real judgement call to make.

`.stat-label`/`.stat-value`, shared by the wind and solar cards, is the only reuse *among these
families*. (`.glass-card`, `.card-header`, `.card-icon` and `.card-title` are genuinely shared by
every card — the duplication is in card *bodies*, not card *shells*.)

**The alignment inconsistency, stated correctly.** Comparing like with like:

- items: `.humidity-stat` sets `align-items: center` (`:503-509`); `.lightning-stat` (`:784`) and
  `.rain-stat-block` (`:690`) set nothing.
- rows: `.humidity-stats-row` (`:495`) sets no `align-items`; `.lightning-stats-row` (`:778`) sets
  `flex-start`.

(An earlier draft compared an item against a row. The inconsistency is real; that comparison was
not evidence of it.)

**The strongest single piece of evidence for consolidation.** Three cards already implement, by
hand and separately, the exact interior idiom §6.4 adopts:

```css
.rain-card .rain-grid            { margin-top: auto; margin-bottom: auto; }  /* App.css:669  */
.lightning-card .lightning-content { margin-top: auto; margin-bottom: auto; }  /* App.css:747  */
.station-health-card .health-grid  { margin-top: auto; margin-bottom: auto; }  /* App.css:1114 */
```

each preceded by its own `display: flex; flex-direction: column` on the card. §6.4 does not invent
a mechanism; it **generalises one that was independently rediscovered three times** and never
applied to the other seven cards. Those three rules and their six companion declarations are
deleted by this design.

DRY gate: *the shared knowledge is "how this dashboard renders a measured quantity and its
label", and it changes when the dashboard's visual language changes.* Concretely completable,
which is what distinguishes this from shape-only similarity. Changing stat alignment today takes
eight edits.

### 4.4 Cause D — Vite does not emit maplibre's worker. §6.10.

### 4.5 Cause E — a JSX comment eats a space. §6.9.

---

## 5. Corrections to the filed issues

**5.1 — #182's stated causal chain is false.** #182 says *"The height drop is a consequence of
#178."* Measured, `.hero-content` is **116px tall at every viewport tested**. The hero card is
314.2px only when it shares a row with the WIND card, itself 314.2px; alone in a row it is 174px =
116 + 2×28 padding + 2×1 border, exactly. #182 is the loss of row-stretch, not a consequence of
clipping.

The 116px invariance is itself partly a *symptom* of horizontal overflow rather than wrapping.
Once the hero wraps (§6.6) its content height grows to 212.8px at 390px, so the narrow half of
#182 resolves. Only the desktop half survives, and that is #191.

**5.2 — #181's suggested fix would not work.** #181 proposes `repeat(3, minmax(0, 1fr))`. That
changes the *explicit* tracks and leaves `grid-auto-columns` at `auto`, so the implicit
content-sized track survives and the columns stay unequal. **Both** changes are required (§6.2).
(An earlier draft attributed a "560–900px band" claim to #181; #181 says "somewhere between" 756
and 1512. The band framing came from the handoff, not the issue.)

**5.3 — #184 is two unrelated things and the issue conflates them.** `/basemap/osm.pmtiles` 404
is **by design**: `web/public/basemap/PROVENANCE.md` states the basemap is operator-supplied,
`web/.gitignore` excludes `public/basemap/*.pmtiles`, and the document says in terms *"this is the
intended graceful degrade, not a bug."* Only the worker 404 is a defect, and only it is fixed.

**5.4 — #180 overstates by 21px.** The reported 142.4px inside `pressure-card` includes 20px of
bottom padding and 1px of border. True empty space is **121.4px** — 42.8% of the card, not 50%.

---

## 6. The design

The complete change is ~50 lines of CSS plus one import, one JSX character-level fix, and the
component extraction of §6.5.

### 6.1 Phasing — two reviewable units on one branch

The branch is a breakpoint overhaul *and* a ten-component extraction. That is not reviewable as
one diff, so it lands as two phases with a measurement gate between them.

- **Phase 1 — layout only, no component changes.** §6.2, §6.3, §6.4, §6.6, §6.7, §6.8, §6.9,
  §6.10, plus committing the harness (§11). Closes #177, #178, #179, #180, #181, #182 (narrow),
  #183, #184, #188 and the unfiled almanac clipping. **Every §12 threshold is met at the end of
  Phase 1** — the numbers in this document are Phase 1 numbers.
- **Phase 2 — §6.5's primitives,** one card per commit, each with a probe diff. Its acceptance
  criterion is that Phase 1's numbers do not move.

This ordering was chosen over interleaving because §6.8 requires no component change at all and
§6.4 is six lines on `.glass-card`, which is already the shared shell. Only §6.7's records floor
genuinely wants to live in a primitive, and it moves there in Phase 2.

### 6.2 Breakpoint strategy — three widths, each load-bearing

**Decision: an explicit column ladder at 1000px and 640px, with span classes clamped to the column
count at every level, `minmax(0, 1fr)` on the explicit tracks, and
`grid-auto-columns: minmax(0, 1fr)` for implicit ones.**

```css
.dashboard-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  grid-auto-columns: minmax(0, 1fr);
}
@media (max-width: 1000px) {
  .dashboard-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .card-span-2, .card-span-3 { grid-column: span 2; }
}
@media (max-width: 640px) {
  .dashboard-grid { grid-template-columns: minmax(0, 1fr); }
  .card-span-2, .card-span-3 { grid-column: span 1; }
}
```

**One part fixes the defect; two are seams.** The **span clamps** are the fix — they stop implicit
tracks arising at all, and they are what every measurement below reflects.

`minmax(0, 1fr)` and `grid-auto-columns: minmax(0, 1fr)` are measured **no-ops for the current
DOM** (§4.1): the former because every grid child is a scroll container, the latter because with
the clamps in place no implicit track is ever created. They are kept deliberately as Patchbay
seams — two lines, zero present cost — so that a future card without `overflow: hidden`, or a
future span the clamps miss, degrades to an equal track instead of a 268px content-sized one. An
earlier draft claimed all three were required; that was wrong, and the correction matters because
it means **the clamps alone must be got right**.

**Breakpoint choices, validated by measurement rather than asserted:**

| viewport | tracks after | clipped leaves before → after |
|---|---|---|
| 1001 | 3 (302.9px each) | 0 → 0 |
| 1000 | 2 (464.3px each) | 0 → 0 |
| 660 | 2 (296.3px each) | 14 → **0** |
| 641 | 2 (286.9px each) | 17 → **0** |
| 640 | 1 (592.6px) | 18 → **0** |
| 620 | 1 (572.8px) | 23 → **0** |

640px was chosen above the file's existing 560px because 561px was the worst point in the entire
range (30 clipped leaves across 5 cards). The table above resolves what an earlier draft listed as
an open question: two columns still clip nothing at 641px, so the 3→2→1 boundaries are safe where
placed.

**Alternatives measured and rejected.** *Intrinsic sizing* — `repeat(auto-fit, minmax(280px, 1fr))`
— does **not** clamp spans, so absolute spans still mint implicit tracks: measured `280/280/108` at
756px (17 clipped leaves) and `280/42` at 390px (**40 clipped leaves**, against a baseline of 15).
`grid-column: 1 / -1` does not rescue it, because `-1` is the explicit grid's end: at 620px
`auto-fit` resolved to one explicit track, `span 2` minted an implicit second, and `1 / -1` spanned
284px of a 572px grid. *Container queries for the grid* were rejected because under the ladder
every card is exactly 1, 2 or 3 equal tracks wide — a container-query grid adds a second mental
model without new information.

**Result: every track is equal at all nineteen widths** (`new Set(tracks).size === 1`), against
unequal at every width ≤900 today.

### 6.3 Container width

**Decision: raise `.dashboard { max-width }` from 1200px to 1600px.** Measured, not projected:

| viewport | gutter before → after | card width before → after |
|---|---|---|
| 2560 | 1408px (55%) → **1014px (39.6%)** | 370.7 → 500.3px |
| 1920 | 768px (40%) → **374px (19.5%)** | 370.7 → 500.3px |
| 1512 | 360px (23.8%) → **54px (3.6%)** | 370.7 → 471px |
| 1280 | 128px (10%) → **52.8px (4.1%)** | 370.7 → 394.4px |

A fixed cap is deliberate rather than fluid: a weather card 850px wide reads worse than one 500px
wide, and 1600px holds the widest card at ~500px. The residual gutter at 2560px is intentional
margin.

The after-values are 5–6px larger than a naive `1600 − 2×24` calculation because §6.8's type scale
enlarges `.dashboard`'s `1.5rem` padding at wide viewports. This is why the table is measured.

A fourth column above 1400px was measured (page height 2109 → 1672, cards ~373px) and is
**deferred, not adopted** — a fourth breakpoint and a fourth clamp level for density nobody asked
for. Recorded so it is not re-derived.

### 6.4 Card interiors — fill without breaking uniformity

**Decision: rows keep stretching to the tallest sibling. The card's first block pins to the top
and the remaining content is centred as a group, via auto margins.**

Uniform block heights are the design's signature and are kept. `align-items: start` was measured,
yields zero empty space, and is **rejected**: it makes rows ragged, with height spread up to
145.1px within a row.

```css
.glass-card { display: flex; flex-direction: column; }
.glass-card > :first-child { flex: 0 0 auto; }
.glass-card > :first-child + * { margin-top: auto; }
.glass-card > :first-child ~ :nth-last-child(1 of :not(.rain-animation)) { margin-bottom: auto; }
.radar-card > :first-child + * { margin: 0; flex: 1 1 auto; }
.radar-map-container { height: 100%; min-height: 280px; }
```

**`:first-child`, not `.card-header`.** `RecordsCard.tsx:38` heads with `.records-header`, not
`.card-header`, so a `.card-header`-anchored selector silently skips the records card entirely.
`.hero-card` has no header element at all and is already centred by
`App.css:264-269` (`justify-content: center`), so `:first-child + *` correctly matches nothing
there and the hero is left alone.

**`:nth-last-child(1 of :not(.rain-animation))`, not `:last-child` — this one is load-bearing and
state-dependent.** `RainCard.tsx:68` renders `.rain-animation` as a **third child, but only while
it is raining**. It is `position: absolute` (`App.css:714-723`), so it absorbs no free space, yet
it is structurally `:last-child`. A plain `:last-child` rule hands it the `margin-bottom: auto` it
cannot use and leaves `.rain-grid` with `margin-top: auto` alone — bottom-pinned. Measured in an
isolated fixture with the deleted rule genuinely absent:

| card state | `.rain-grid` margin-top | margin-bottom | result |
|---|---|---|---|
| not raining, `:last-child` | 135px | 135px | centred |
| **raining, `:last-child`** | **270px** | **0px** | **bottom-pinned** |
| raining, `:nth-last-child(1 of :not(.rain-animation))` | 135px | 135px | centred |
| not raining, same rule (regression check) | 135px | 135px | centred |

Today's `App.css:669-672` masks this, and §7 deletes it — so without this selector the branch
would ship the regression as an acceptance criterion.

**Two alternatives were tested and both fail:** moving `.rain-animation` to be the second DOM
child makes `.card-header + *` match the animation and top-pins `.rain-grid`; making it the first
child pushes the header itself down. The out-of-flow element has to be excluded, not relocated.

`:nth-last-child(… of S)` is Safari 9+, Chrome 111+, Firefox 113+ — comfortably inside this
project's Safari 17.4 baseline. If that selector is ever a problem, the fallback is one explicit
`.rain-card > .rain-grid { margin-bottom: auto; }`, measured equivalent. **Phase 2 removes the need
for either**, because the primitives give every card a single body element to centre.

**Deletions this rule makes mandatory** (§4.3): `App.css:663-672`, `:742-750` and `:1109-1117` —
three hand-rolled copies of this idiom plus their `display: flex; flex-direction: column`
preambles. Leaving them is exactly the duplication §4.3 is about.

Measured, at the widths where row-stretch is in play:

| viewport | lopsided empty space before → after | row height spread |
|---|---|---|
| 2560 / 1920 / 1512 | 216.5 → **117.7px** | 0 → 0 (uniform) |
| 1280 | 216.5 → 118.9px | 0 → 0 |
| 1001 | 217.1 → 110.8px | 0 → 0 |
| 1000 | 217.1 → **80.1px** | 0 → 0 |
| 900 | 198.5 → **78.8px** | 0 → 0 |
| 756 | 158.3 → 77.2px | 0 → 0 |

**Two mechanisms were measured and rejected.**

*`flex: 1 1 auto` on each block* reaches a slightly lower number (113.3px at 1512) but pushes the
first and last blocks to opposite ends, stranding the pressure gauge at the bottom of its card
with a void above it. Lower score, worse result — which is why §12's threshold is set against the
adopted mechanism's 117.7 and not against 113.3.

*Letting each card's visual grow into the height* — the literal reading of "use the space" — is
**self-defeating**: enlarging the humidity ring made its whole grid **row** taller, so
`PRESSURE` and `SOLAR & UV` beside it ended up hollower than they started. Lopsidedness 216.5 →
501.9px and page height +686px. Redistribution inside a card cannot create content when a tall
sibling sets the row height. This is why the residual ~118px is accepted rather than chased, and
why the real answer to it is content (#191).

**One trap, hit during design:** a blanket `display: flex` on card children **destroys
`display: grid`** on `.records-grid` and `.almanac-records`, blowing the records card from 238px
to 1014px. The adopted rule sets no `display` on children at all.

### 6.5 Card primitives — the consolidation (Phase 2)

**Decision: extract four shared primitives and compose every card from them.**

| primitive | replaces | responsibility |
|---|---|---|
| `Stat` | `.detail-*`, `.stat-*`, `.humidity-stat-*`, `.rain-stat-*`, `.lightning-label`, `.health-*`, `.rstat-*`, `.almanac-hl-*` | label, value, optional unit and sub-label; one alignment |
| `StatRow` | the 7 row wrappers in §4.3 | 2–3 `Stat`s across a card; one distribution rule and one wrap floor |
| `ScaleBar` | `.gauge-*` and `.uv-bar-*` | track, fill, optional indicator, tick labels |
| `Readout` | `.hero-temp`, `.pressure-value`, `.uv-number`, `.lightning-count`, `.humidity-value` | the card's headline number, its qualifier, **and its size** |
| `Badge` | `.status-badge`, `.stale-badge`, `.rain-active-badge`, `.lightning-alert-badge`, `.records-window` | one pill shape, `tone` = neutral / info / warning / danger |

`.lightning-count` belongs to **`Readout`** (it is the card's headline number), and
`.lightning-label` to `Stat`. An earlier draft assigned `.lightning-count` to both.

**`Readout` carries a size scale — two steps, not six.** This is the substantive addition over
"collapse the classes": today's six sizes become

```css
:root {
  --readout-hero:    4rem;   /* the temperature, and nothing else */
  --readout-primary: clamp(1.75rem, 1.35rem + 1.1vw, 2.5rem);
}
```

`--readout-hero` is deliberately **flat**, not clamped: §6.8's root scale already shrinks it with
the viewport, and a second clamp on top measured *smaller* than today at mobile (60.4px → 41.5px).
`--readout-primary` scales with the viewport, which is what fixes the undersized pressure readout.

**Scope: all eight of §4.3's sizes, not five.** `--readout-primary` applies to `.pressure-value`,
`.humidity-value`, `.uv-number`, `.lightning-count`, `.lightning-distance` and
`.wind-speed-value`. `.lightning-distance` is non-negotiable — it shares a row with
`.lightning-count`, so promoting one alone renders 40.9px beside 36px in the same row.
`.rstat-value` stays with `Stat`: `.rstat` is a bordered card-in-card of *equal* stats with no
headline among them, so it is a `StatRow` of `Stat`s, not a `Readout`.

**`Readout` owns numbers, not glyphs.** §6.8 defers the hero's `WeatherIcon` and the two header
SVGs to Phase 2; they belong to whatever renders them, **not** to `Readout`. An earlier draft said
"`Readout` owns the hero glyph", which contradicted this table.

Measured effect at 1512px (471px cards): pressure **28.8 → 40.9px**, UV **36 → 40.9px** — the same
size, because they are the same thing. At 390px both land at 26.4px and the hero stays at its
current 60.4px. Zero clipped leaves and row spread 0 at both widths.

**The trade, stated rather than buried.** `--readout-primary` makes the *smaller* readouts bigger
and the *larger* ones smaller. Below roughly 1000px the UV number and `.lightning-count` end up
below today's size:

| viewport | pressure today → after | UV today → after |
|---|---|---|
| 1512 | 28.8 → **40.93** | 36 → **40.93** |
| 1000 | 26.88 → 33.68 | 33.6 → 33.68 |
| 640 | 25.27 → 28.36 | **31.58 → 28.36** |
| 390 | 24.15 → 26.41 | **30.18 → 26.41** |
| 320 | 24 → 26.25 | **30 → 26.25** |

That is the point of a scale — one size for one role — but it is a visible reduction on phones and
§12 therefore constrains the floor as well as the ceiling.

Alignment moves with it: `Readout` centres in its card, and `Stat`/`StatRow` centre their content,
which closes the three visible defects named in §4.3 (`PRESSURE`, `PRECIPITATION`,
`STATION HEALTH` all rendering hard-left while `HUMIDITY` centres).

Alignment behaviour lives here, so cards get it by construction rather than from eight parallel
rules, and §6.4's interior treatment has exactly one place to be right.

A CSS-only preview of the target was rendered and reviewed. Its residue is diagnostic: the cards
still misaligned in the preview — `PRESSURE`'s readout and `STATION HEALTH`'s labels — are exactly
the ones whose classes were not in the preview's override list. That is the census reproducing
itself, and it is the argument for components over more overrides.

**Test debt this exposes (§13).** The suite currently contains **one** DOM-structure assertion in
total. Phase 2 must ship unit tests for the four primitives, or it ships the branch's largest
change with no automated regression gate.

### 6.6 The hero at narrow widths

**Decision: the secondary stats wrap below the temperature. They do not shrink and do not
relocate.**

```css
.hero-content { flex-wrap: wrap; row-gap: 1.25rem; }
.hero-temp-block { min-width: 0; }
.hero-details { flex: 1 1 200px; min-width: 0;
                grid-template-columns: repeat(auto-fit, minmax(88px, 1fr)); }
```

| viewport | hero overflow before → after | hero content height after |
|---|---|---|
| 900 | 0 → 0 | 119.8px |
| 756 | 101 → **0** | 116.8px |
| 641 | 216 → **0** | 114.5px |
| 620 | 237 → **0** | 114.1px |
| 561 | 296 → **0** | 112.9px |
| 504 | 65 → **0** | 217.2px *(wraps here)* |
| 390 | 179 → **0** | 212.8px |
| 320 | 249 → **0** | 302.3px |

**A side effect the earlier draft hid.** `flex: 1 1 200px` replaces today's `flex-shrink: 0`, so
`.hero-details` becomes the only growing item on the line and absorbs the free space. Its inner
`auto-fit` grid therefore reflows:

| viewport | `.hero-details` width | stat grid |
|---|---|---|
| 1512 | 202.2 → 625.2px | 2×2 → **1 row × 4** |
| 1000 | → 627.8px | **1 × 4** |
| 900 | → 532.7px | **1 × 4** |
| 756 | → 395.7px | 2 × 3 |
| 641 and below | → 286–220px | 2 × 2 |

This is **intended** — it is what spreads the four stats across the hero's width instead of
huddling them in a 202px column, and it is visible in every desktop render in the review. But the
decision line above says the stats "do not shrink and do not relocate", and `flex: 1 1 200px;
min-width: 0` sets `flex-shrink: 1` and removes the automatic minimum, so both halves of that
sentence are untrue of the CSS. Read the decision as: *the stats keep their content and their
place in the card; their column count follows the width they are given.*

*Shrink in place* was rejected: the 463px minimum means values truncate or the 4rem temperature
shrinks out of its hero role. *Relocate to a separate stats strip* was rejected as a structural
TSX change making the layout differ by more than reflow across widths.

This also resolves #182's narrow half — the hero stops being shorter than the WIND card below it.

**The hero centres as a whole in the one-column band.** Wrapping alone leaves it mixed-alignment:
icon and temperature centred as a flex row, then the stats grid left-aligned beneath, which reads
as misaligned rather than deliberate.

```css
@media (max-width: 640px) {
  .hero-content { justify-content: center; }
    .hero-temp-block { align-items: center; text-align: center; }
  .hero-details { text-align: center; }
}
```

`.hero-detail-item` is one of the eight `Stat` families, so in Phase 2 its centring comes from the
primitive and this block loses its last two rules.

### 6.6a The header at phone widths

Unfiled, found by the same probe. `.app-header` is `justify-content: space-between` with no
narrow-width rule, so at 390px `.station-name` wraps to two lines and `.station-location` to two
more: a **123.9px header on an 844px screen**, above the fold on every scroll.

**Decision: shrink the competing parts. Do not truncate and do not hide anything.**

```css
@media (max-width: 640px) {
  .app-header    { padding: 0.6rem 1rem; gap: 0.5rem; }
  .header-right  { gap: 0.5rem; flex-shrink: 0; }
  .station-name  { font-size: 1.05rem; gap: 0.4rem; }
  .logo-icon svg { width: 20px; height: 20px; }
  .status-badge  { padding: 0.25rem 0.5rem; font-size: 0.7rem; }
  .last-updated  { font-size: 0.7rem; }
}
```

| width | header height before → after | name lines | truncated | page overflow |
|---|---|---|---|---|
| 390 | 123.9 → **79px** | 2 → 1 | no | 0 → 0 |
| 360 | 123.3 → **78.6px** | 2 → 1 | no | 0 → 0 |
| 320 | 123.3 → 119.1px | 2 → 2 | no | **17 → 0** |

**These heights are a function of the station name, not of the CSS**, and any acceptance criterion
must say so. The table above is `STATION_NAME="W0 Baseline"` (11 characters), the value on the
measured deployment. Measured sweep with §6.6a applied:

| name | 390 | 360 | 320 |
|---|---|---|---|
| `Home` (4) | 79 / 1 line | 78.6 / 1 | 95.5 / 1 |
| `W0 Baseline` (11) | 79 / 1 | 78.6 / 1 | 119.1 / 2 |
| `Tempest Station` (15, the default at `Header.tsx:30`) | 79 / 1 | **102.3 / 2** | 119.1 / 2 |
| `My Weather Station` (18) | **102.8 / 2** | 102.3 / 2 | 142.8 / 3 |

**Page overflow is 0 at every width for every name**, so the overflow fix is robust; the *height*
is not. §11's `/api/station` fixture must therefore pin the station name, and §12 states the
threshold as a relative reduction rather than an absolute pixel count.

**Three alternatives were measured and rejected**, all for the same reason — they remove
information, which is the defect this branch exists to fix:

- *Hiding `.last-updated`* reaches 71.1px but drops the freshness readout and the `.stale-badge`
  that rides on it. That badge is the only signal that the data on screen is held over.
- *`white-space: nowrap` + `text-overflow: ellipsis` on the name and location* reaches 99.1px but
  truncated the coordinates to `47.8601°N, 122.2043°W ·`, hiding the elevation — and `display:
  block` on `.station-name` broke the logo out of its flex row onto its own line.
- *Wrapping the header into two full-width rows* is consistent (105.8px at every width) but never
  better than the adopted rule at 390px or 360px.

`.logo-icon svg` is sized in CSS over the SVG's `width`/`height` attributes, the same presentational-
attribute override §6.8 relies on.

**This is what closes the 320px page overflow, and the cause is the header — not the grid.**
Isolating each rule at 320px identifies the offending element directly:

| CSS applied | overflow | widest offender |
|---|---|---|
| none (today) | **17px** | `.header-right` at `right = 337.3` on a 320px viewport |
| grid ladder alone | 17px | `.header-right` — unchanged |
| container cap alone | 17px | `.header-right` — unchanged |
| hero wrap / almanac / records alone | 17px | `.header-right` — unchanged |
| **type scale alone** | **1px** | `.header-right` at 321 |
| full Phase 1 incl. §6.6a | **0px** | none |

The 17 → 1px step comes from §6.8's type scale shrinking the header's rem-sized padding and text,
and the last 1px from this rule. An earlier draft attributed it to §6.2's explicit-track change,
which §4.1 now shows is a no-op — that attribution was wrong.

### 6.7 Records card, and the unfiled almanac defect

**Records — lower the `.records-grid` floor from `minmax(150px, 1fr)` to `minmax(120px, 1fr)`:**

| viewport | records card height before → after |
|---|---|
| 561 | 918.5 → **394.5px** |
| 504 | 496.8 → 392px |
| 390 | 918.5 → **480.8px** |
| 360 | 918.5 → 479.2px |
| 320 | 918.5 → 886.9px *(still one column)* |

`minmax(min(140px, 100%), 1fr)` was also measured and is worse — it holds one column at 360px.
In Phase 2 this floor becomes a `StatRow` property. **`.records-grid` holds six `.rstat` boxes,
which are bordered cards-in-cards** (`App.css:1523`); Phase 2 must decide whether `StatRow` gains
a bordered variant or `.rstat` stays a distinct component composing `Stat`. Phase 1 changes the
one value and nothing else.

**Almanac — closing two defects in no filed issue.** The almanac clips **7 text leaves at 320px
and 7 at 360px** (`This Year`, `H ↑`, `83°F`, `Aug 18`, `L ↓`, `56°F`, `Today`) and **none at
390px**. Sitting beside the clipping is a worse defect the clipping metric cannot see: at narrow
widths the sunrise/sunset text renders **on top of** the moon.

```css
.almanac-astro   { flex-wrap: wrap; }
.almanac-sun     { max-width: none; }
.almanac-records { grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); }

@media (max-width: 640px) {
  .almanac-moon { position: static; left: auto; top: auto; transform: none; }
}
@media (max-width: 480px) {
  .almanac-astro { flex-direction: column; align-items: center;
                   justify-content: center; gap: 1rem; }
}
```

`App.css:949-955` caps `.almanac-sun` at `calc(50% - 100px)` — a 50px cap on a block holding a
40px SVG plus text in a 300px-wide card, and **that cap is what clips `Sunrise` and its time**.
Dropping it is therefore required, not optional. `App.css:1029-1033` pins `.almanac-records` to
`repeat(4, 1fr)` with no floor, hence the third rule. **§6.2's ladder changes nothing here**: at
390px the dashboard is one column both today (≤560) and under the ladder (≤640).

**Correction — the two rules stand; the *reason* given for them was false, and they are unsafe
alone.** This section previously justified dropping `.almanac-sun`'s cap by claiming the wrap on
`.almanac-astro` "is what now keeps the sun sections clear of the centre moon". **It cannot be.**
`.almanac-moon` is `position: absolute; left: 50%` (`App.css:985-994`), so it is **not a flex
item** and `flex-wrap` never displaces it — it stays pinned over the card's horizontal centre
whatever the sun blocks do. The cap was the only thing holding them out from under it; the
element's own source comment says as much (`/* keep sun sections away from center moon */`).

So removing the cap is still **required** — it is what fixes the clipping — but on its own it
trades a clipping defect for an overlap one: measured against the live stack, sunrise/sunset text
lands on top of the moon by **84.7 / 64.7 / 50.8 / 32.4px at 320 / 360 / 390 / 430**. Overlap is
not clipping, which is exactly why the earlier "0 clipped leaves at 390px" measurement was
consistent with the defect and did not catch it. **The two media blocks are what make the cap
removal safe**, by putting the moon back in flow so it participates in the layout that displaces
it.

**Returning the moon to normal flow ≤640px is only half the fix.** `justify-content:
space-between` on `.almanac-astro` then drops the moon at whichever end of the wrapped line it
lands on — measured **69.1 / 89.1 / 103.6 / 122.9px** off the card's centre. The centred column
≤480px resolves that. 480px rather than 640px because at 504px all three blocks still fit across
and that layout already reads correctly. **Cost:** card height 482 → 804px at 390px and 1167px at
320px, accepted; the desktop card is untouched at 501px. Both stages were injected into the live
stack and chosen from rendered screenshots.

`AlmanacCard.tsx:101` renders `<GlassCard span={3}>` with no `className`, so the card has no
per-card hook; these rules target its descendants.

### 6.8 Type and icon scale

**Decision: one fluid root type scale in `rem`; icons sized in `rem` so they ride it.**

```css
:root { font-size: clamp(0.9375rem, 0.875rem + 0.28vw, 1.125rem); }
.card-icon { width: 1.25rem; height: 1.25rem; }
```

**`rem`, not `px`.** In a root `font-size`, `rem` resolves against the property's *initial* value —
the user's default — so a reader who has raised their browser font size keeps the increase
(WCAG 1.4.4). At a 16px default this is arithmetically identical to `clamp(15px, 14px + 0.28vw,
18px)`. An earlier draft specified the px form, which would have silently overridden the
preference.

| viewport | icon | title |
|---|---|---|
| 2560 / 1920 / 1512 | 20 → 22.5px | 12.8 → 14.4px |
| 1280 | 20 → 22px | 12.8 → 14.07px |
| 1000 | 20 → 21px | 12.8 → 13.44px |
| 640 | 20 → 19.7px | 12.8 → 12.63px |
| 390 | 20 → 18.9px | 12.8 → 12.07px |
| 320 | 20 → 18.8px | 12.8 → 12px |

The upper clamp binds at **1429px**, so 1512, 1920 and 2560 are identical by design — §12's
threshold is stated as non-decreasing for that reason.

**No component change is required.** All **ten** card icons (`PressureCard.tsx:32`,
`WindCard.tsx:36`, `StationHealth.tsx:36`, `HumidityCard.tsx:26`, `SolarUVCard.tsx:40`,
`LightningCard.tsx:15`, `AlmanacCard.tsx:104`, `ForecastStrip.tsx:26`, `RainCard.tsx:46`,
`RadarCard.tsx:274`) are `<svg className="card-icon" width="20" height="20" viewBox="...">`.
`width`/`height` on SVG are *presentational attributes* — specificity 0, start of the author
origin — so a CSS rule overrides them, and they remain as a no-CSS fallback.

**Three icons deliberately do not ride the scale:** `TemperatureHero.tsx:40`'s
`<WeatherIcon size={72} />` and `Header.tsx:26,62`'s 28px and 22px SVGs. They are sized in TSX,
not CSS. #177's title names "icons and titles", so this is a scoped partial fix, stated as such:
the ten *card* icons scale; the hero glyph and the two header icons do not. Bringing them in means
touching components and is deferred to Phase 2, where `Readout` owns the hero glyph.

*Container queries* were rejected here for a different reason than in §6.2: under the ladder cards
are 1, 2 or 3 tracks wide, so a card-relative icon would render the hero's header icon visibly
larger than the WIND card's beside it. Header icons are labels and should match each other. *A
discrete per-breakpoint scale* was rejected as jumpy and as putting typography into media queries
that otherwise carry only grid concerns.

### 6.9 Header separator (#188)

`Header.tsx:34-37` renders:

```tsx
{formatCoord(station.latitude, station.longitude)}
{/* comment */}
&middot; {station.elevation ?? '—'}m
```

JSX trims line-leading and line-trailing whitespace and drops lines containing only an expression
container. The comment is such a line, so the newline-plus-indent between the coordinate and the
`&middot;` vanishes. Measured output: `47.8601°N, 122.2043°W· 150m`, character-for-character, at
all nineteen widths.

**Fix:** an explicit `{' '}` before `&middot;`. Not fixable in CSS.

### 6.10 maplibre worker (#184)

maplibre-gl v6 derives its worker URL from `import.meta.url`, resolving to
`/assets/maplibre-gl-worker.mjs`. Vite emits no such chunk, so the worker's script 404s and the
map's pipeline never completes.

**In `web/src/components/RadarCard.tsx`**, beside the existing `maplibre-gl` import at `:5` and
above `registerPmtilesProtocol` — it must execute before `new maplibregl.Map(...)` at `:151`, and
`RadarCard` is the only module that constructs a map:

```ts
import maplibreWorkerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url';
maplibregl.setWorkerUrl(maplibreWorkerUrl);
```

**`?worker&url`, not bare `?url`.** `maplibre-gl-worker.mjs` imports `./maplibre-gl-shared.mjs` as
a relative sibling; a plain asset copy loses it. `?worker` routes through Vite's
`bundleWorkerEntry`, which resolves that import. `setWorkerUrl` is a documented export
(`maplibre-gl.d.ts:16731`).

**Evidence, graded by reproducibility.**

*Deterministic and re-runnable:* `npx vite build` on an untouched copy produced
`dist/assets/index-DYNKecXu.js` and **no worker file**, while the bundle referenced one. With the
import, the build emitted `dist/assets/maplibre-gl-worker-5nf0xxoE.js` and the bundle pointed at
it. Serving that build, `/assets/maplibre-gl-worker-*.js` returns 200 and **the worker 404
disappears from the request log**.

*Observed once, not currently reproducible:* with `/api/radar/ATX` returning 200 and 2,911,077
bytes (a `FeatureCollection` of 8 filled `MultiPolygon`s), the fixed build rendered reflectivity
echoes where the unfixed build painted a blank rectangle. **This cannot be re-run on demand.**
During final verification the same endpoint returned 502 on every attempt, because #185 is
data-dependent and the scan had grown past 10 MiB. The end-to-end render is gated on #185, not on
this fix.

*Corrected claim.* An earlier draft cited "zero failed requests" after the fix. That was an
artifact: the verification server fell back to `index.html` for any missing path, returning
200 + HTML for `/basemap/osm.pmtiles` where production 404s
(`internal/httpserver/server.go:186-192`). The harness now mirrors the real rule and is parity-
checked (§11). With it corrected, the post-fix log shows `404 /basemap/osm.pmtiles` (by design,
§5.3) and `502 /api/radar/ATX` (#185) — and **no worker 404**, which is the fix.

**Cost.** The worker adds **470.82 kB uncompressed** to `dist/assets`, and thence to the Go binary
via `//go:embed all:dist`. The main bundle is unchanged (1,187.32 → 1,187.40 kB), so this is pure
addition, ~+40% of the JS payload. Cause: Vite's default `worker.format` is `"iife"`, so
`maplibre-gl-shared.mjs` is inlined into the worker while the main bundle keeps its own copy.

**The obvious mitigation does not work, and was measured rather than deferred.**
`build.worker.format` is not a real option path — Vite silently ignores it and emits a
byte-identical bundle (same `5nf0xxoE` hash). The correct top-level `worker: { format: 'es' }`
produces **471.08 kB**, 0.26 kB *larger*. The duplication is not recoverable this way. **Not a
blocker** — the alternative is a radar card that cannot render at all — but the plan should record
the gzip figure, which was never captured.

---

## 7. Complete CSS

Phase 1 in full, so the plan implements this and not a paraphrase. Line references are to
`web/src/App.css` at `fb52e3f`.

```css
/* #177 */
:root { font-size: clamp(0.9375rem, 0.875rem + 0.28vw, 1.125rem); }
.card-icon { width: 1.25rem; height: 1.25rem; }

/* #179 — replaces max-width:1200px at :252 */
.dashboard { max-width: 1600px; }

/* #181 #182 — replaces :257-261 and :1486-1500 */
.dashboard-grid { grid-template-columns: repeat(3, minmax(0, 1fr));
                  grid-auto-columns: minmax(0, 1fr); }
@media (max-width: 1000px) {
  .dashboard-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .card-span-2, .card-span-3 { grid-column: span 2; }
}
@media (max-width: 640px) {
  .dashboard-grid { grid-template-columns: minmax(0, 1fr); }
  .card-span-2, .card-span-3 { grid-column: span 1; }
}

/* #178 — amends :310-316, :285 */
.hero-content { flex-wrap: wrap; row-gap: 1.25rem; }
.hero-temp-block { min-width: 0; }
.hero-details { flex: 1 1 200px; min-width: 0;
                grid-template-columns: repeat(auto-fit, minmax(88px, 1fr)); }

/* #183 — amends :1522 */
.records-grid { grid-template-columns: repeat(auto-fit, minmax(120px, 1fr)); }

/* unfiled almanac clipping — amends :940-947, :949-955, :1029-1033.
   The cap removal is REQUIRED (it is what clips Sunrise) but is only safe
   because the media blocks below put .almanac-moon back in flow — see §6.7. */
.almanac-astro { flex-wrap: wrap; }
.almanac-sun { max-width: none; }
.almanac-records { grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); }

/* hero centres as a whole in the 1-column band (§6.6) — merges with the block below */
@media (max-width: 640px) {
  .hero-content { justify-content: center; }
    .hero-temp-block { align-items: center; text-align: center; }
  .hero-details { text-align: center; }

  /* unfiled header defect (§6.6a) — amends :123-136, :144-151, :153-157, :171-181 */
  .app-header    { padding: 0.6rem 1rem; gap: 0.5rem; }
  .header-right  { gap: 0.5rem; flex-shrink: 0; }
  .station-name  { font-size: 1.05rem; gap: 0.4rem; }
  .logo-icon svg { width: 20px; height: 20px; }
  .status-badge  { padding: 0.25rem 0.5rem; font-size: 0.7rem; }
  .last-updated  { font-size: 0.7rem; }

  /* unfiled almanac overlap (§6.7) — reverts :985-994's out-of-flow moon so the
     sun blocks cannot render on top of it. `.almanac-moon`'s own rule is left
     untouched; only the query overrides it. */
  .almanac-moon { position: static; left: auto; top: auto; transform: none; }
}

/* unfiled almanac off-centre moon (§6.7) — a wrapped `space-between` row drops the
   in-flow moon at one end; the column centres it. 480, not 640: at 504px all three
   blocks still fit across. */
@media (max-width: 480px) {
  .almanac-astro { flex-direction: column; align-items: center;
                   justify-content: center; gap: 1rem; }
}

/* #180 — and DELETE :663-672, :742-750, :1109-1117.
   NOTE :663-672 also removes `.rain-card { position: relative }`. That is safe
   only because `.glass-card` sets `position: relative` at :67 on the same
   element (GlassCard.tsx:15) — and `.rain-animation` depends on it. */
.glass-card { display: flex; flex-direction: column; }
.glass-card > :first-child { flex: 0 0 auto; }
.glass-card > :first-child + * { margin-top: auto; }
.glass-card > :first-child ~ :nth-last-child(1 of :not(.rain-animation)) { margin-bottom: auto; }
.radar-card > :first-child + * { margin: 0; flex: 1 1 auto; }
.radar-map-container { height: 100%; min-height: 280px; }
```

**Edit-range notes, so the ranges are followed as intended rather than literally:**

- *"replaces `:1486-1500`"* means the **grid and span rules inside** the `@media (max-width: 560px)`
  block. `:1492`'s `@media` opener and `:1502-1515`'s settings rules **stay** — the block keeps its
  560px query and loses only the grid lines. Deleting the literal range would leave an unmatched
  brace.
- *"#178 amends `:310-316`, `:285`"* — it also amends `.hero-content` at `:271-275`, which is the
  first rule the block touches.
- `.hero-icon { order: -1 }` was in an earlier draft and is **removed**: `.hero-icon` is already
  the first DOM child (`TemperatureHero.tsx:41`), and flex wrapping does not reorder, so the
  declaration was dead.

**The settings-panel breakpoint stays at 560px.** `App.css:1492-1515`'s `@media (max-width: 560px)`
also carries `.settings-overlay`, `.settings-panel` and `.theme-grid`. Only the grid and span rules
move to 640px; the settings rules keep their own 560px block, which becomes a separate media query.
This is deliberate — the settings panel is an overlay whose sizing is unrelated to the column count
— and it means the file will legitimately contain both a 560px and a 640px query.

---

## 8. Known residues — measured, not fixed here

1. ~~The page overflows horizontally at 320px.~~ **Closed.** 17px today → 1px after §6.2's
   explicit-track fix → **0px** after §6.6a's header rule. Verified at 320, 360 and 390.
2. **The hero holds ~116px of content in a ~1000px card at desktop.** Real, unfixable by layout,
   filed as **#191**. §6.4's residual ~118px of lopsidedness is mostly this.
3. **Three icons do not scale** — the hero glyph and two header icons (§6.8). Phase 2.
4. **The radar card remains broken in a shipped deployment** even after §6.10, because #185 and
   #186 are open. This branch makes it verifiable, not working.
5. **The header still takes 119.1px at 320px**, against 79px at 390px (§6.6a). Below the iPhone SE
   floor and below `index.css:47`'s `min-width: 320px`. Nothing is truncated or hidden there; it
   simply wraps. Not chased further.

---

## 9. Risks

| risk | why plausible | mitigation |
|---|---|---|
| Phase 2 changes visuals unintentionally | 8 families collapse into 1; they differ today in font size and gap | Phase 1's committed numbers are Phase 2's acceptance criterion; one card per commit with a probe diff; new unit tests per §6.5 |
| The fluid root scale shifts every `rem` in the file | `.dashboard` padding, card padding, gaps and most type are `rem` | **Everything rem-sized contracts below ~1000px** — up to 6% at 320px — which is why §6.3's table is measured, not computed. All 19 widths verified for clipping and overflow after the scale is applied |
| `:first-child` selectors misfire on a card whose first element is not a header | The rule is structural, not semantic | Verified against all 11 mounted cards; `.records-header` and the header-less hero are the two non-obvious cases and both behave correctly |
| The 470.82 kB worker bloats the binary | It ships via `//go:embed` | Measured and recorded (§6.10); `build.worker.format: 'es'` to be evaluated in the plan |
| #91 collides with Phase 2 | Same file, same CSS family | §3.4 — sequence #91 after this branch; agree before either starts |
| The harness needs a live backend | Every threshold in §12 is measured against real data | §11 — API fixtures make it hermetic |

---

## 10. Decisions that were open, now closed

1. **The 640px breakpoint is validated** — 641px still clips nothing at two columns (§6.2).
2. **The almanac reaches zero clipped leaves at 320px and 360px** (it already had none at 390px),
   but only with §6.7's rules; §6.2 alone does nothing for it. The moon overlap §6.7 also closes
   is invisible to the clipping metric, which is why §12 gains a separate centring threshold.
3. **Playwright becomes a `devDependency`, and the layout probe runs against committed API
   fixtures** — see §11. This is the only way §12's thresholds are re-runnable, and it is what
   gives Phase 2 the regression gate §6.5 needs.
4. **`Readout` gains a two-step size scale** (§6.5), replacing six ad-hoc values. This is what
   makes the pressure and UV numbers legible at desktop widths, and it is why `Readout` is worth
   extracting rather than merely re-aligning.
5. **`Badge` is a fifth primitive** (§4.3, §6.5). Two of its five implementations are identical
   apart from a colour.

6. **The header is fixed in Phase 1** (§6.6a) by shrinking its parts, not by truncating or hiding
   anything. 123.9 → 79px at 390px, and it closes the last of the 320px page overflow.
7. **#91 lands after this branch** (§3.4) and is then implemented in terms of `Stat` / `Readout` /
   `Badge`, rather than against the `.lightning-*` classes it would otherwise remove by hand. W1's
   design currently schedules it in parallel and needs amending.

No open questions remain.

---

## 11. Verification harness

**It must be committed in-tree.** The harness used for this design lives in a session scratchpad,
which a plan executed in a fresh worktree cannot reach. Proposed home: `web/test/layout/`.

| file | responsibility |
|---|---|
| `probe.mjs` | the DOM probe — grid geometry, per-card boxes, **leaf-level** overflow, the **asymmetry** metric, icon and type sizes, `documentElement` overflow |
| `measure.mjs` | drives the 19 widths, writes `measurements.json` |
| `serve.mjs` | serves a built `dist/` with `/api/*` from fixtures |
| `fixtures/*.json` | recorded responses for `/api/observations/current`, `/history`, `/summary`, `/station`, `/almanac`, `/capabilities`, `/radar/{site}` |

**`serve.mjs` must mirror `internal/httpserver/server.go:164-195`**, in particular *a missing path
with a file extension 404s; a missing path without one serves `index.html`*. The earlier harness
used a naive SPA fallback, which returned 200 + HTML for `/basemap/osm.pmtiles` and made a
dev/prod-parity defect (12-factor X) invisible in exactly the dimension under test. The corrected
harness is parity-checked against the real server:

| path | harness | real server |
|---|---|---|
| `/` | 200 | 200 |
| `/basemap/osm.pmtiles` | 404 | 404 |
| `/some/spa/route` | 200 | 200 |

**Fixtures, not a live backend.** Recording the API responses once makes the probe hermetic and
deterministic, which is what lets it join `task ci` — a check that needs a running k8s deployment
could not, because CLAUDE.md requires every `task ci` check to be runnable locally with one
command. Radar is the one endpoint whose live response is large and variable (#185); its fixture
should be a small recorded `FeatureCollection`.

**Task wiring.** A new `node:layout` target, added to `node:ci` so `task ci` covers it.
`.taskfiles/node.yml` is template-generated from `github-actions-align`; the plan must either
extend the template or document the local divergence rather than silently editing generated
output.

---

## 12. Per-issue: what changes, and how it is verified

Every row is verified by re-running the probe and diffing numbers, never by looking at a
screenshot. Thresholds are Phase 1 values with a small margin.

| # | Change | Threshold |
|---|---|---|
| 177 | §6.8 | `.card-icon` box and `.card-title` size **non-decreasing** with viewport across all 19 widths, with **≥5 distinct values**. (Not "strictly increasing" — the clamp ceiling binds at 1429px, so 1512/1920/2560 are equal by design.) |
| 178 | §6.2 + §6.6 | **0 clipped leaves at every one of the 19 widths, 320 to 2560**; `.hero-content` `scrollWidth == clientWidth` at all 19 |
| 179 | §6.3 | gutter ≤60px at 1512 and ≤60px at 1280; strictly less than today at 1280/1512/1920/2560 |
| 180 | §6.4 | lopsided empty space ≤125px at 1512 (from 216.5); row height spread ≤1px at every width |
| 181 | §6.2 | every track equal to within 1px at all 19 widths |
| 182 | §6.6 | row spread ≤1px; hero content height ≥180px at ≤504px. **Desktop half is #191 and is not closed here** |
| 183 | §6.7 | records card ≤500px at 390px (from 918.5) |
| — | §6.7 almanac | 0 clipped leaves attributed to the almanac at 320px, 360px and 390px |
| — | §6.7 almanac | **`.almanac-moon` centred within ≤4px of `.almanac-astro`'s centre** at 320/360/390/430, and **0px of overlap** between it and either `.almanac-sun` block. Clipping cannot see this defect; this is the threshold that can |
| 184 | §6.10 | `npx vite build` emits a worker asset into `dist/assets` and the bundle references it; serving that build, `/assets/maplibre-gl-worker-*.js` returns 200 and **no worker 404 appears in the request log**. The end-to-end render is *not* an acceptance criterion — it is gated on #185 |
| 188 | §6.9 | `.station-location` `textContent` matches `/°[WE] ·/` |
| — | §6.6a header | **Relative, because the height depends on the station name (§6.6a):** `.app-header` at least **35% shorter** at 390px and at 360px than the same fixture measures without the rule. **Nothing truncated** at 320/360/390 (`scrollWidth == clientWidth` for `.station-name` and `.station-location`) — this half is absolute and name-independent. `/api/station`'s fixture pins `name` |
| — | §6.2 + §6.6a | `documentElement.scrollWidth == innerWidth` at **every** one of the 19 widths, 320 included (from 17px of overflow at 320) |
| — | §6.4 deletions | `App.css` contains no `.rain-card .rain-grid`, `.lightning-card .lightning-content` or `.station-health-card .health-grid` rule |
| Phase 2 | §6.5 | the label/value families of §4.3 reduce to one; every threshold above still passes; `Stat`/`StatRow`/`ScaleBar`/`Readout`/`Badge` have unit tests |
| Phase 2 | §6.5 `Readout` | All six `--readout-primary` consumers report the **same** computed `font-size` at every width — including `.lightning-count` and `.lightning-distance`, which share a row; ≥40px at 1512 and **≥26px at 320** (a floor, because the scale reduces the UV and lightning numbers on phones); all centred within 2px of their card's content-box centre |
| Phase 2 | §6.5 `Stat` | `PRECIPITATION`, `STATION HEALTH`, `LIGHTNING` and `HUMIDITY` stat blocks all report `align-items: center`; no card in the grid reports a stat family with a different alignment |
| Phase 2 | §6.5 `Badge` | `App.css` contains one pill implementation; `.rain-active-badge` and `.lightning-alert-badge` no longer exist as separate rules |

**Regression floor.** `task ci` must stay green including the node leg (`node:lint`, `node:test`,
`node:typecheck`, and the new `node:layout`). It measured green at `fb52e3f` across 19 stages.
Capture exit codes as `cmd > /tmp/x.log 2>&1; echo "EXIT=$?"` in a single invocation —
`${PIPESTATUS[0]}` does not populate in this harness and reads as success.

**`IMAGE=… task smoke` is mandatory for #184** — the only gate that catches a `//go:embed`
breakage, because an empty `web/dist` still compiles.

---

## 13. Test debt this branch inherits

The `web/` suite contains **ten** `querySelector` assertions across **three** files —
`Header.test.tsx:61,86,106,129` (`.station-location`), `SettingsPanel.test.tsx:51,150,168,186`
(`.theme-swatch` and friends) and `RainCard.test.tsx:38,50` (`.raindrop`). An earlier draft said
"one in total", which was wrong.

The conclusion survives the correction: **none of the ten touches the eight label/value families
Phase 2 collapses**, nor `ScaleBar`, nor `Readout`. Coverage of the structures actually being
rewritten is zero. §11's harness plus §6.5's primitive unit tests are therefore not optional
extras — without them the branch's largest change ships with no automated gate.

One of the ten is directly relevant: `RainCard.test.tsx:38,50` asserts on `.raindrop`, which only
exists while raining — the same conditional branch that §6.4's `:nth-last-child` selector exists
to handle. That test is the natural place to assert the rain card centres in **both** states.

---

## 14. Gate 1 corrections

| # | Finding | Resolution |
|---|---|---|
| B1 | §6.4 prescribed auto margins but quoted `flex: 1` numbers | Re-measured the adopted mechanism at 19 widths; 117.7px, not 113.3px. Both mechanisms now recorded with the reason for choosing the higher-scoring one |
| B1b | `.card-header`-anchored selectors skip `RecordsCard` (`.records-header`) | Changed to `:first-child` |
| B1c | "the hero would be the one hollow card" was false — it is already centred (`:268`) | Justification corrected; hero rule removed as unnecessary |
| B2 | Three almanac rules were measured but absent from the design | Promoted into §6.7 with their cause; the §8 "residue" entry deleted |
| B3 | §6.2 omitted the explicit-track `minmax(0, 1fr)` change | Added, with its distinct mechanism (§4.1) — and it is what takes the 320px overflow from 17px to 1px |
| B4 | "#177 both strictly increase" is falsified by the clamp ceiling | Restated as non-decreasing with ≥5 distinct values |
| B5 | §2 claimed eleven widths and listed fifteen; harness described inaccurately and unreachable | 19 widths listed and measured; §11 rewritten, harness moved in-tree with fixtures |
| B6 | §12 demanded 0 clipped leaves while §6.6 showed 7 | Resolved by B2; now 0 at all 19 widths |
| S1 | §6.3's table was projected, not measured, and 5–6px off | Re-measured, with the reason for the discrepancy |
| S3 | "zero failed requests" was a harness artifact | `serve.mjs` corrected to mirror `server.go:164-195`, parity-checked; §6.10's evidence re-graded by reproducibility |
| S4 | +470.82 kB worker never mentioned | Recorded in §6.10 with its cause and a follow-up measurement |
| S5 | Parallel-safety asserted on a false premise; #91 collision unlisted | §3.4 rewritten; #91 sequencing raised as a decision |
| S6 | `px` root font-size overrides the user's font preference | Changed to `rem`; §9's incoherent mitigation row rewritten to state the real risk (rem-sized things *contract* below ~1000px) |
| S7 | The consolidation has no test safety net | §13 added; primitive unit tests made an acceptance criterion |
| S9 | Not reviewable as one diff | §6.1 — two phases with a measurement gate |
| S10 | `.lightning-count` assigned to two primitives | Assigned to `Readout` only |
| S11 | Ambiguous where the records floor lives | §6.7 — one value in Phase 1; `.rstat`'s bordered-card shape called out as a Phase 2 decision |
| S12 | Moving the grid breakpoint leaves the settings panel at 560 | §7 — deliberate, and stated |
| M1/M2 | Systematic bad cross-references to non-existent §7.x | All references renumbered against the current structure |
| M3 | "eight" card icons | Ten, enumerated; the three non-scaling icons called out as a scoped partial fix |
| M4 | Only `AlmanacCard` named as a span carrier | `ForecastStrip`, `RadarCard` and the hero added |
| M5 | Alignment evidence compared an item against a row | Corrected to like-for-like |
| M6 | "the only reuse in the file" overstated | Narrowed to the label/value families |
| M7 | §4.2 and §8 item 1 contradicted on page overflow | Metric defined explicitly in §2; `body { overflow-x: hidden }` and `min-width: 320px` named |
| M8 | §5.2 corrected a claim #181 does not make | Rewritten to correct what #181 *does* get wrong — its proposed fix is insufficient |
| M9 | New `.glass-card` rule left three per-card duplicates in place | Deletions made mandatory in §6.4 and §7, and made a §12 acceptance criterion — and the duplication reframed as §4.3's strongest evidence |

**Not adopted:** the reviewer's suggestion to restore the `flex: 1` mechanism. It scores 4.4px
better and produces the stranded pressure gauge this design exists to remove. The threshold moved
instead.

---

## 15. Gate 1 re-review corrections

A second scoped review verified §14's table and attacked the material added after it. Three
blocking and seven significant findings, all confirmed first-hand before acting.

| # | Finding | Resolution |
|---|---|---|
| **B-1** | `.glass-card > :first-child ~ :last-child` bottom-pins the rain card **while it is raining** — `.rain-animation` (`RainCard.tsx:68`) is a conditional, absolutely-positioned third child that absorbs no free space. §7 deletes the rule that masks it, so the branch would have shipped the regression as an acceptance criterion | Selector changed to `:nth-last-child(1 of :not(.rain-animation))`. Reproduced in an isolated fixture (margin-top 270px / margin-bottom 0px) and both the adopted fix and a `.rain-card`-scoped fallback measured to restore 135/135. Two relocation alternatives tested and rejected — they break the other way (§6.4) |
| **B-2** | §4.1's "third contributor" is false: `minmax(0, 1fr)` is a **no-op** because `overflow: hidden` on `.glass-card` makes every grid child a scroll container, disabling the automatic minimum (CSS Grid L1 §6.6). So §14's B3 row attributed a 16px overflow reduction to a change that cannot produce it | §4.1 rewritten with the three-variant control measurement; §6.2's "all three parts are required" withdrawn — the **span clamps** are the fix, the other two are declared seams. **The real cause was then isolated: `.header-right` reaching 337.3px on a 320px viewport.** Per-rule isolation table added to §6.6a; the 17→1px step is §6.8's type scale, the last 1px is the header rule |
| **B-3** | §12's header threshold (≤85px at 390 and 360) is a property of the **station name**, not the CSS. The default `Tempest Station` gives 102.3px at 360 where the measured `W0 Baseline` gives 78.6px | Name sweep added to §6.6a (4/11/15/18 characters); §12 restated as a **relative** ≥35% reduction, with the truncation half kept absolute because it *is* name-independent; §11's `/api/station` fixture must pin `name` |
| **S-1** | The two alert badges differ in **three** declarations, not one — background, text colour, and a different animation keyframe at a different duration | §4.3 corrected with the diff; `Badge` now needs `tone` **plus** an explicit animation decision, and the DRY tier reclassified from unify-on-sight to Tier B |
| **S-2** | The headline-number census missed `.wind-speed-value` (2.2rem) and `.lightning-distance` (2rem) — eight sizes, not six. `.lightning-distance` shares a row with `.lightning-count` | §4.3 corrected to eight; §6.5 scopes `--readout-primary` to all six consumers and resolves `.rstat-value` to `Stat` with a reason |
| **S-3** | §6.6's `flex: 1 1 200px` silently reflows the hero stat grid 2×2 → 1×4 at ≥900px, and "do not shrink" is untrue of the CSS | Reflow table added to §6.6 and the decision line reworded to what the CSS actually does |
| **S-4** | `--readout-primary` makes the UV and lightning numbers ~12% **smaller** on phones, unstated and unconstrained | Trade table added to §6.5; §12 gains a ≥26px floor at 320 |
| **S-5** | §6.5 and §6.8 disagreed on whether `Readout` owns the hero glyph | §6.5: `Readout` owns numbers, not glyphs |
| **S-6** | "one DOM-structure assertion in total" is false — ten, across three files | §13 corrected; the conclusion survives because none of the ten covers the rewritten structures, and `RainCard.test.tsx`'s `.raindrop` assertions are named as the natural home for B-1's regression test |
| **S-7** | §6.10's fix had no file path | Named: `RadarCard.tsx`, beside the `:5` import, above the `:151` map construction |
| **M** | `build.worker.format: 'es'` names a non-existent option; the correct `worker.format: 'es'` is 0.26 kB *larger* | Measured and recorded in §6.10 rather than deferred |
| **M** | `.hero-icon { order: -1 }` was dead; §7's edit ranges would produce broken CSS if followed literally; `.rain-card { position: relative }` is a seventh deleted declaration; `TemperatureHero.tsx:37` is `:38`; three dangling cross-references survived §14 | All corrected; §7 gains explicit edit-range notes |

**Live-only claims the re-review could not verify, and which the plan must re-measure rather than
inherit:** §6.4's asymmetry table, all clipped-leaf counts, §6.7's records and almanac heights, and
§6.6's 302.3px hero height at 320px (the reviewer measured 211.5px against a reduced fixture — a
content-dependent difference, not a CSS one). These are exactly what §11's in-tree harness plus
fixtures exist to make reproducible.

---

## 16. Post-Gate-2 amendment — §6.7's almanac mechanism

Gate 2 reviewed the *plan*, and in doing so falsified a mechanism this design asserted. The
correction was made here before the design was committed, so that the committed spec and the
plan it spawned agree. Recorded rather than silently applied.

| # | Finding | Resolution |
|---|---|---|
| **A-1** | §6.7 justified `.almanac-sun { max-width: none }` + `flex-wrap` on `.almanac-astro` by claiming the wrap keeps the sun blocks clear of the centre moon. `.almanac-moon` is `position: absolute; left: 50%` (`App.css:985-994`), so it is **not a flex item** and wrapping cannot displace it. Measured: **84.7 / 64.7 / 50.8 / 32.4px** of sun-text-over-moon overlap at 320 / 360 / 390 / 430 | **The two rules stand — the justification was withdrawn, not the CSS.** Dropping the cap is what fixes the clipping and is required; it is merely unsafe *alone*. Two scoped media blocks make it safe — moon returns to flow ≤640px, astro row becomes a centred column ≤480px — both injected into the live stack and chosen from rendered screenshots by the owner. §6.7 and §7 rewritten; the cost (card 482 → 804px at 390px) stated rather than buried. **An earlier pass of this amendment over-corrected by deleting both rules outright, which would have left the clipping unfixed; that over-correction is itself reverted here.** |
| **A-2** | §6.7 stated the almanac clips 7 leaves **at 390px**. Re-measured: it clips 7 at **320px** and 7 at **360px**, and **none at 390px**. The defect is real, at different widths than recorded | Widths corrected in §6.7, §10 and §12. This is an instance of §15's "live-only claims must be re-measured rather than inherited" |
| **A-3** | Returning the moon to flow alone leaves it **69.1 / 89.1 / 103.6 / 122.9px** off the card's centre, because `justify-content: space-between` drops it at whichever end of the wrapped line it lands on. No existing threshold would have caught either this or A-1, because **overlap and off-centring are not clipping** | §12 gains a dedicated threshold: `.almanac-moon` centred within ≤4px and 0px of overlap with either `.almanac-sun` block, at 320/360/390/430. This takes the Task 12 gate from 23 thresholds to **24** |

**Sequencing decision taken at the same time, and not implemented here.** §3.4 raised #91's
collision with this branch as an open decision. **Settled: #91 lands after this branch**, and is
then implemented in terms of `Stat`/`Readout` rather than against the classes it would otherwise
remove by hand. `docs/designs/2026-08-18-road-to-80-percent-design.md:137,239` still schedules W3
parallel with W1 and needs amending — that file belongs to another workstream and is deliberately
**not** edited by this branch.
