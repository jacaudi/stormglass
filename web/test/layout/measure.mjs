// Layout probe driver. Serves a built dist/ with recorded API fixtures, drives
// 19 viewport widths through a headless Chromium, and evaluates the thresholds
// in docs/designs/2026-08-19-dashboard-responsive-layout-design.md §12.
//
// Hermetic on purpose: a check that needed a live k8s deployment could not join
// `task ci`, and CLAUDE.md requires every `task ci` check to be runnable locally
// with one command.
import { chromium } from 'playwright';
import { readFileSync, writeFileSync, existsSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startServer } from './serve.mjs';
import { probe } from './probe.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = join(HERE, '..', '..');
const DIST = join(WEB, 'dist');
const APP_CSS = join(WEB, 'src', 'App.css');

// The pairs 1001/1000 and 641/640 prove the breakpoints fire where intended;
// 660 and 641 validate the 640px choice (design §6.2).
export const WIDTHS = [
  2560, 1920, 1512, 1280, 1100, 1001, 1000, 900, 756, 660,
  641, 640, 620, 561, 504, 430, 390, 360, 320,
];

// Fixed so the header's "Updated hh:mm" and Station Health's "Ns ago" render
// identical text on every run and every machine. 30s after the fixture's
// timestamp, which also keeps status.isOnline true.
const FIXED_EPOCH_MS = 1787166000 * 1000 + 30_000;
const VIEWPORT_HEIGHT = 900;

// Every task that lands a design section appends its thresholds here. Each check
// is { id, section, describe(m) -> string, pass(m) -> boolean }, where `m` is the
// full measurement object written to measurements.json.
export const CHECKS = [];

function fail(msg) {
  console.error(`::error::${msg}`);
  process.exitCode = 1;
}

// --- structural checks: things provable without a browser -------------------

function checkDistBuilt() {
  if (!existsSync(join(DIST, 'index.html'))) {
    throw new Error(
      'web/dist/index.html is missing. Run `npm run build` (or `task node:build`) first.'
    );
  }
}

function checkAppCssDeletions() {
  const css = readFileSync(APP_CSS, 'utf8');
  const banned = [
    /\.rain-card\s+\.rain-grid\b/,
    /\.lightning-card\s+\.lightning-content\b/,
    /\.station-health-card\s+\.health-grid\b/,
  ];
  const found = banned.filter((re) => re.test(css)).map(String);
  // .glass-card must keep `position: relative` (App.css:67) -- deleting
  // `.rain-card { position: relative }` is only safe because of it, and
  // .rain-animation is absolutely positioned against it.
  const hasGlassRelative = /\.glass-card\s*\{[^}]*position:\s*relative/.test(css);
  // Task 13: the five hand-built badge rules must be gone AND the shared
  // .badge primitive must exist. Checking only the negative (legacyBadges
  // empty) would pass even if the Badge CSS block was never added -- absent
  // data reading as success -- so hasBadgeBlock is a required positive
  // companion, mirroring hasGlassRelative above.
  //
  // Deviation from the plan's verbatim snippet (disclosed per the branch's
  // standing fail-loud-on-absent-data rule): the plan's regex was bare
  // `/\.badge\s*\{/`, which also matches Task 7's renamed responsive override
  // `.badge { padding: ...; font-size: ...; }` inside the 640px media query.
  // Deleting the actual primitive block while leaving that override rule in
  // place left the naive regex passing -- proven by deleting the primitive
  // block during verification and watching the check NOT go red. Anchored
  // instead on `white-space: nowrap`, a declaration unique to the primitive's
  // own rule body (the media-query override only ever touches padding and
  // font-size), confirmed unique elsewhere in the file.
  const legacyBadges = [
    /\.status-badge\s*\{/,
    /\.stale-badge\s*\{/,
    /\.rain-active-badge\s*\{/,
    /\.lightning-alert-badge\s*\{/,
    /\.records-window\s*\{/,
  ]
    .filter((re) => re.test(css))
    .map(String);
  const hasBadgeBlock = /\.badge\s*\{[^}]*white-space:\s*nowrap/.test(css);
  return { bannedFound: found, hasGlassRelative, legacyBadges, hasBadgeBlock };
}

function checkWorkerAsset() {
  const assets = join(DIST, 'assets');
  if (!existsSync(assets)) return { workerFile: null, referenced: false };
  const files = readdirSync(assets);
  const workerFile = files.find((f) => /^maplibre-gl-worker-.*\.js$/.test(f)) ?? null;
  if (!workerFile) return { workerFile: null, referenced: false };
  const referenced = files
    .filter((f) => f.endsWith('.js') && f !== workerFile)
    .some((f) => readFileSync(join(assets, f), 'utf8').includes(workerFile));
  return { workerFile, referenced };
}

async function checkServerParity(port) {
  // Mirrors internal/httpserver/server.go:164-200. A missing path WITH a file
  // extension 404s; a missing path WITHOUT one serves index.html; /api/* never
  // falls back. An earlier harness used a naive SPA fallback and returned
  // 200 + HTML for /basemap/osm.pmtiles, hiding a dev/prod-parity defect
  // (12-factor X) in exactly the dimension under test.
  const cases = [
    ['/', 200],
    ['/basemap/osm.pmtiles', 404],
    ['/some/spa/route', 200],
    ['/api/nope', 404],
  ];
  const results = [];
  for (const [path, want] of cases) {
    const res = await fetch(`http://127.0.0.1:${port}${path}`);
    results.push({ path, want, got: res.status });
  }
  return results;
}

// --- the sweep --------------------------------------------------------------

async function sweep(browser, port, { revertHeaderRule = false } = {}) {
  const context = await browser.newContext({
    locale: 'en-US',
    timezoneId: 'UTC',
    viewport: { width: WIDTHS[0], height: VIEWPORT_HEIGHT },
    deviceScaleFactor: 1,
  });
  await context.clock.setFixedTime(new Date(FIXED_EPOCH_MS));
  const page = await context.newPage();
  const out = {};
  for (const width of WIDTHS) {
    await page.setViewportSize({ width, height: VIEWPORT_HEIGHT });
    // NEVER `networkidle` -- the dashboard polls observations continuously and
    // retries radar, so it never settles (design §2).
    // 3s, not 8: if MapLibre never initialises the request is never made, and
    // this timeout is paid on all 38 sweep navigations. 3s is comfortably above
    // the fixture server's response time and bounds the waste at ~2 minutes.
    const radarSettled = page
      .waitForResponse((r) => /\/api\/radar\//.test(r.url()), { timeout: 3_000 })
      .catch(() => null);
    await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('.dashboard-grid', { timeout: 15_000 });
    // RadarCard renders a .radar-status-message in the 'unavailable' state and
    // nothing in 'loading'/'ok', so an unsettled radar changes the card's child
    // count and its height. Wait for its fetch before the settle delay, and
    // record the resulting state so `radar-state-consistent` can fail loudly if
    // it ever differs across the sweep instead of drifting silently.
    await radarSettled;
    if (revertHeaderRule) {
      await page.addStyleTag({ content: HEADER_RULE_REVERT });
    }
    await page.waitForTimeout(600);
    out[width] = await probe(page);
  }
  await context.close();
  return out;
}

// The pre-change state the design's ">= 35% shorter" figure was derived from:
// BOTH the root type scale (§6.8) and §6.6a's six declarations reverted. The
// design measured 123.9 -> 79px at 390 (63.8%) and 123.3 -> 78.6 at 360
// (63.7%) on CSS that had neither, so reverting only §6.6a would compare the
// after-state against a header that had already contracted ~6% from the rem
// scale -- moving the ratio up toward the 0.65 threshold for no real reason.
// `font-size: 1rem` in a root rule resolves against the property's INITIAL
// value, i.e. the user's default, which is exactly the pre-Task-3 state.
// `gap: normal` is likewise the initial value -- .app-header declared no gap.
const HEADER_RULE_REVERT = `
:root { font-size: 1rem; }
@media (max-width: 640px) {
  .app-header    { padding: 1rem 2rem; gap: normal; }
  .header-right  { gap: 1rem; flex-shrink: initial; }
  .station-name  { font-size: 1.25rem; gap: 0.6rem; }
  .logo-icon svg { width: 28px; height: 28px; }
  /* Selector note: Task 13 renamed .status-badge to .badge. This revert block
     and Task 7's rule in App.css are two halves of one comparison and must
     name the same element. */
  .badge         { padding: 0.3rem 0.75rem; font-size: 0.75rem; }
  .last-updated  { font-size: 0.8rem; }
}`;

async function rainStateMeasurement(browser) {
  const { port, close } = await startServer({ rain: true });
  try {
    const context = await browser.newContext({
      locale: 'en-US',
      timezoneId: 'UTC',
      viewport: { width: 1512, height: VIEWPORT_HEIGHT },
      deviceScaleFactor: 1,
    });
    await context.clock.setFixedTime(new Date(FIXED_EPOCH_MS));
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('.rain-card', { timeout: 15_000 });
    await page.waitForTimeout(400);
    const m = await probe(page);
    await context.close();
    return m;
  } finally {
    await close();
  }
}

// --- main -------------------------------------------------------------------

async function main() {
  const argv = process.argv.slice(2);
  const baselineIdx = argv.indexOf('--baseline');
  const baselinePath = baselineIdx === -1 ? null : argv[baselineIdx + 1];
  const outIdx = argv.indexOf('--out');
  const outPath = outIdx === -1 ? join(HERE, 'measurements.json') : argv[outIdx + 1];

  checkDistBuilt();

  const { port, log, close } = await startServer({});
  let m;
  try {
    const parity = await checkServerParity(port);
    const browser = await chromium.launch();
    try {
      const widths = await sweep(browser, port);
      const headerRevert = await sweep(browser, port, { revertHeaderRule: true });
      const rain = await rainStateMeasurement(browser);
      m = {
        widths,
        headerRevert: { 390: headerRevert[390], 360: headerRevert[360], 320: headerRevert[320] },
        rain,
        parity,
        worker: checkWorkerAsset(),
        css: checkAppCssDeletions(),
        requestLog: log.slice(),
      };
    } finally {
      await browser.close();
    }
  } finally {
    await close();
  }

  const target = baselinePath ?? outPath;
  writeFileSync(target, JSON.stringify(m, null, 2));
  console.log(`wrote ${target}`);

  if (baselinePath) {
    console.log('--baseline: thresholds not evaluated');
    return;
  }

  let failed = 0;
  for (const check of CHECKS) {
    let ok = false;
    let detail;
    try {
      ok = check.pass(m);
      detail = check.describe(m);
    } catch (err) {
      detail = `threw: ${err.message}`;
    }
    console.log(`${ok ? 'PASS' : 'FAIL'}  ${check.id}  (${check.section})  ${detail}`);
    if (!ok) failed++;
  }
  if (CHECKS.length === 0) {
    console.log('no thresholds registered yet');
  }
  if (failed > 0) fail(`${failed} of ${CHECKS.length} layout thresholds failed`);
}

// --- Task 2 / harness self-consistency --------------------------------------
// RadarCard's DOM differs by resolved status ('unavailable' adds a
// .radar-status-message and ~50px). If that resolves differently across the
// sweep, row heights and asymmetry move for reasons that have nothing to do with
// the CSS -- in a check that gates `task ci`. Fail loudly rather than drift.
CHECKS.push({
  id: 'radar-state-consistent',
  section: 'harness',
  describe: (m) => {
    const states = WIDTHS.map((w) => m.widths[w].radarStatusMessage);
    return `statusMessage present at ${states.filter(Boolean).length}/19 widths`;
  },
  pass: (m) => new Set(WIDTHS.map((w) => m.widths[w].radarStatusMessage)).size === 1,
});

// .almanac-sun's max-width cap exists (per its own comment) to keep the sun
// blocks clear of the absolutely-positioned centre moon. Task 8 removes it and
// relies on .almanac-astro wrapping instead. An overlap is NOT clipping, so
// nothing else here would see it.
CHECKS.push({
  id: 'almanac-sun-clears-the-moon',
  section: '§6.7',
  describe: (m) => {
    // null means .almanac-moon is gone -- Tasks 8/23 rewrite the almanac and
    // could delete it outright, not just mispositioning it. A deleted moon
    // must fail loudly here, not read as "0px overlap".
    const bad = WIDTHS.filter((w) => {
      const v = m.widths[w].almanacSunMoonOverlap;
      return v === null || v > 1;
    });
    return bad.length
      ? bad
          .map((w) => {
            const v = m.widths[w].almanacSunMoonOverlap;
            return v === null ? `${w}:SELECTOR MISSING (.almanac-moon)` : `${w}:${v.toFixed(1)}px`;
          })
          .join(' ')
      : 'no overlap at any width';
  },
  pass: (m) =>
    WIDTHS.every((w) => {
      const v = m.widths[w].almanacSunMoonOverlap;
      return v !== null && v <= 1;
    }),
});

// The moon must also be CENTRED, not merely clear of the sun blocks. With the
// moon back in flow and the row still wrapping, it sits 69/89/104/123px off the
// card's centre at 320/360/390/430 -- no overlap, no clipping, and visibly
// wrong. 4px of tolerance covers the existing space-between rounding at 504/640.
CHECKS.push({
  id: 'almanac-moon-centred',
  section: '§6.7',
  describe: (m) => {
    // null means .almanac-moon (or its enclosing card) is gone -- see the
    // sibling check above for why a deleted moon must fail loudly.
    const bad = WIDTHS.filter((w) => {
      const v = m.widths[w].almanacMoonOffset;
      return v === null || v > 4;
    });
    return bad.length
      ? bad
          .map((w) => {
            const v = m.widths[w].almanacMoonOffset;
            return v === null ? `${w}:SELECTOR MISSING (.almanac-moon)` : `${w}:${v.toFixed(1)}px`;
          })
          .join(' ')
      : 'centred at all 19 widths';
  },
  pass: (m) =>
    WIDTHS.every((w) => {
      const v = m.widths[w].almanacMoonOffset;
      return v !== null && v <= 4;
    }),
});

// --readout-primary reaches ~41px at 1512 inside a 140px ring. Design §6.5
// measured only pressure and UV; the card-level clipping metric cannot see an
// overflow that stays inside the card.
CHECKS.push({
  id: 'humidity-ring-text-fits',
  section: '§6.5 Readout',
  describe: (m) => {
    // null means .humidity-ring-container or .humidity-ring-text is gone --
    // Task 18 rewrites HumidityCard and could delete the ring outright, not
    // just mispositioning its text. A deleted ring must fail loudly here,
    // not read as "0px overflow".
    const bad = WIDTHS.filter((w) => {
      const v = m.widths[w].humidityRingOverflow;
      return v === null || v > 1;
    });
    return bad.length
      ? bad
          .map((w) => {
            const v = m.widths[w].humidityRingOverflow;
            return v === null
              ? `${w}:SELECTOR MISSING (.humidity-ring-container/.humidity-ring-text)`
              : `${w}:${v.toFixed(1)}px`;
          })
          .join(' ')
      : 'fits at all 19 widths';
  },
  pass: (m) =>
    WIDTHS.every((w) => {
      const v = m.widths[w].humidityRingOverflow;
      return v !== null && v <= 1;
    }),
});

// --- Task 3 / design §6.8 / #177 --------------------------------------------
// NON-DECREASING, not strictly increasing: the clamp ceiling binds at 1429px,
// so 1512/1920/2560 are equal by design (design §14 B4).
const ascending = (m, pick) =>
  [...WIDTHS].sort((a, b) => a - b).map((w) => pick(m.widths[w]));

// Expected .card-icon count per width, read from baseline.json rather than
// hardcoded -- a hardcoded literal would be wrong the moment a card is gated
// off by configuration, and baseline.json already recorded the real count for
// every width when Task 2 captured it. A width missing from baseline entirely
// reads as `undefined`, which a live count of any real length will not equal,
// so an unrecognised width still fails loud instead of silently passing.
const BASELINE = JSON.parse(readFileSync(join(HERE, 'baseline.json'), 'utf8'));
const expectedIconCount = (w) => BASELINE.widths[String(w)]?.icons?.length;

CHECKS.push({
  id: 'icon-scale-non-decreasing',
  section: '§6.8 / #177',
  // Fix round 1 (#177 review): sampling icons[0] alone let a per-card CSS
  // override on any of the other cards pass silently, even though probe.mjs
  // already records every .card-icon. Widened to also assert (a) all icons
  // agree in size at each width and (b) the icon count matches baseline --
  // both are per-width anomalies, so describe lists only the widths that
  // disagree rather than dumping all 19 widths x every icon.
  describe: (m) => {
    const s = ascending(m, (x) => x.icons[0].w);
    const countIssues = WIDTHS.filter(
      (w) => m.widths[w].icons.length !== expectedIconCount(w)
    ).map((w) => `${w}:expected ${expectedIconCount(w)} got ${m.widths[w].icons.length}`);
    const agreementIssues = WIDTHS.filter((w) => {
      const icons = m.widths[w].icons;
      if (icons.length === 0) return false;
      const first = icons[0].w;
      return icons.some((ic) => Math.abs(ic.w - first) > 0.01);
    }).map((w) => `${w}:${JSON.stringify(m.widths[w].icons.map((ic) => +ic.w.toFixed(2)))}`);
    let detail = `sizes ${JSON.stringify(s)}`;
    if (countIssues.length) detail += ` | COUNT MISMATCH ${countIssues.join(' ')}`;
    if (agreementIssues.length) detail += ` | DISAGREEMENT ${agreementIssues.join(' ')}`;
    return detail;
  },
  pass: (m) => {
    const s = ascending(m, (x) => x.icons[0].w);
    const monotone = s.every((v, i) => i === 0 || v >= s[i - 1] - 0.01);
    const distinct = new Set(s.map((v) => v.toFixed(2))).size;
    const countOk = WIDTHS.every((w) => m.widths[w].icons.length === expectedIconCount(w));
    const agreementOk = WIDTHS.every((w) => {
      const icons = m.widths[w].icons;
      if (icons.length === 0) return false;
      const first = icons[0].w;
      return icons.every((ic) => Math.abs(ic.w - first) <= 0.01);
    });
    return monotone && distinct >= 5 && countOk && agreementOk;
  },
});

CHECKS.push({
  id: 'title-scale-non-decreasing',
  section: '§6.8 / #177',
  describe: (m) => `sizes ${JSON.stringify(ascending(m, (x) => x.titleFontPx))}`,
  pass: (m) => {
    const s = ascending(m, (x) => x.titleFontPx);
    const monotone = s.every((v, i) => i === 0 || v >= s[i - 1] - 0.01);
    const distinct = new Set(s.map((v) => v.toFixed(2))).size;
    return monotone && distinct >= 5;
  },
});

// --- Task 4 / design §6.3 / #179 --------------------------------------------
// BASELINE is declared above (Task 3, §6.8/#177) -- reused here rather than
// redeclared, since `const BASELINE` would otherwise be a duplicate
// declaration in the same module scope.
const GUTTER_WIDTHS = [2560, 1920, 1512, 1280];

CHECKS.push({
  id: 'gutter-cap',
  section: '§6.3 / #179',
  describe: (m) =>
    GUTTER_WIDTHS.map((w) => {
      const g = m.widths[w].gutter;
      return `${w}: ${BASELINE.widths[w].gutter.toFixed(0)} -> ${
        g === null ? 'MISSING' : g.toFixed(0)
      }`;
    }).join('  '),
  pass: (m) =>
    GUTTER_WIDTHS.every((w) => {
      const g = m.widths[w].gutter;
      return g !== null && g < BASELINE.widths[w].gutter;
    }) &&
    m.widths[1512].gutter !== null &&
    m.widths[1512].gutter <= 60 &&
    m.widths[1280].gutter !== null &&
    m.widths[1280].gutter <= 60,
});

// --- Task 5 / design §6.2 / #181 #182 ---------------------------------------
CHECKS.push({
  id: 'tracks-equal',
  section: '§6.2 / #181',
  describe: (m) => {
    const worst = WIDTHS.reduce(
      (a, w) => (m.widths[w].trackSpread > a.v ? { w, v: m.widths[w].trackSpread } : a),
      { w: null, v: -1 }
    );
    return `worst spread ${worst.v.toFixed(3)}px at ${worst.w}`;
  },
  pass: (m) => WIDTHS.every((w) => m.widths[w].trackSpread <= 1),
});

CHECKS.push({
  id: 'ladder-fires-where-intended',
  section: '§6.2',
  describe: (m) =>
    [1001, 1000, 641, 640].map((w) => `${w}:${m.widths[w].tracks.length}`).join(' '),
  pass: (m) =>
    m.widths['1001'].tracks.length === 3 &&
    m.widths['1000'].tracks.length === 2 &&
    m.widths['641'].tracks.length === 2 &&
    m.widths['640'].tracks.length === 1,
});

// --- Task 6 / design §6.6 / #178 #182 ---------------------------------------
CHECKS.push({
  id: 'hero-no-overflow',
  section: '§6.6 / #178',
  describe: (m) => {
    const bad = WIDTHS.filter((w) => m.widths[w].hero.scrollWidth > m.widths[w].hero.clientWidth);
    return bad.length ? `overflows at ${bad.join(',')}` : 'clean at all 19';
  },
  pass: (m) => WIDTHS.every((w) => m.widths[w].hero.scrollWidth <= m.widths[w].hero.clientWidth),
});

CHECKS.push({
  id: 'hero-grows-when-narrow',
  section: '§6.6 / #182',
  describe: (m) =>
    [504, 430, 390, 360, 320].map((w) => `${w}:${m.widths[w].hero.height.toFixed(0)}`).join(' '),
  pass: (m) => [504, 430, 390, 360, 320].every((w) => m.widths[w].hero.height >= 180),
});

// --- Task 7 / design §6.6a --------------------------------------------------
// RELATIVE, because the height depends on the station name. m.headerRevert is
// the same fixture measured with §6.6a's declarations reverted to their
// pre-change values, so this compares like with like.
// The design derived ">= 35% shorter" from 123.9 -> 79px at 390 (63.8%) and
// 123.3 -> 78.6 at 360 (63.7%). HEADER_RULE_REVERT reverts BOTH the root type
// scale and §6.6a, so this compares against the same "before" that figure came
// from -- roughly 1.2pp of margin against 0.65. If the fixture-derived ratio
// lands between 0.65 and 0.68, that is the fixture's station name and font
// metrics, not a CSS defect: record both numbers, say so, and raise the
// constant to 0.68 with that measurement quoted. Do NOT change Task 7's CSS,
// which is exactly what design §7 specifies.
CHECKS.push({
  id: 'header-shorter-at-phone-widths',
  section: '§6.6a',
  describe: (m) =>
    [390, 360].map((w) =>
      `${w}: ${m.headerRevert[w].header.height.toFixed(1)} -> ${m.widths[w].header.height.toFixed(1)}`
    ).join('  '),
  pass: (m) =>
    [390, 360].every(
      (w) => m.widths[w].header.height <= 0.65 * m.headerRevert[w].header.height
    ),
});

// ABSOLUTE and name-independent: nothing may be truncated at any phone width.
// nameOverflow/locOverflow are `null` (probe.mjs) when their selector doesn't
// match -- e.g. a later task renames the class, or a fixture without a
// location string. `null <= 0` is `true` in JavaScript, so both halves
// exclude null BEFORE comparing rather than folding it into the numeric
// check; a missing selector must FAIL loud, never read as "0px overflow".
CHECKS.push({
  id: 'header-nothing-truncated',
  section: '§6.6a',
  describe: (m) =>
    [390, 360, 320]
      .map((w) => {
        const { nameOverflow, locOverflow } = m.widths[w].header;
        const missing = [];
        if (nameOverflow == null) missing.push('.station-name');
        if (locOverflow == null) missing.push('.station-location');
        return missing.length
          ? `${w}:SELECTOR MISSING (${missing.join(', ')})`
          : `${w}: name ${nameOverflow} loc ${locOverflow}`;
      })
      .join('  '),
  pass: (m) =>
    [390, 360, 320].every((w) => {
      const { nameOverflow, locOverflow } = m.widths[w].header;
      return (
        nameOverflow != null && nameOverflow <= 0 && locOverflow != null && locOverflow <= 0
      );
    }),
});

CHECKS.push({
  id: 'no-page-overflow',
  section: '§6.2 + §6.6a',
  describe: (m) => {
    const bad = WIDTHS.filter((w) => m.widths[w].docOverflow !== 0);
    return bad.length
      ? bad.map((w) => `${w}:${m.widths[w].docOverflow} (${m.widths[w].widestOffender.cls})`).join(' ')
      : '0 at all 19 widths';
  },
  pass: (m) => WIDTHS.every((w) => m.widths[w].docOverflow === 0),
});

// --- Task 8 / design §6.7 / #183 + the unfiled almanac defect ---------------
// Design measured 918.5 -> 480.8 = 52.3% against this 55% threshold, and
// 216.5 -> 117.7 = 54.4% against the 60% one below -- 2.7pp and 5.6pp of
// margin, on quantities design §15 lists as live-only and requiring
// re-measurement. If a fixture-derived ratio lands just over (say 56% / 61%),
// the CSS is not wrong: record baseline and after, confirm the direction and
// magnitude match the design's, and raise that one constant by the measured
// difference plus 2pp, quoting both numbers in the task report. A ratio that is
// nowhere near -- above 75% -- means the CSS did not take, and is a defect.
//
// `null <= N` is `true` in JavaScript, so a missing `.records-card` selector
// (records.height === null) must be excluded BEFORE the ratio comparison
// rather than folded into it -- otherwise a deleted selector reads as "0px
// tall", which trivially passes a "shorter than baseline" check.
CHECKS.push({
  id: 'records-card-not-a-tower',
  section: '§6.7 / #183',
  describe: (m) => {
    const h = m.widths['390'].records.height;
    if (h === null) return '390: SELECTOR MISSING (.records-card)';
    return `390: ${BASELINE.widths['390'].records.height.toFixed(1)} -> ${h.toFixed(1)}`;
  },
  pass: (m) => {
    const h = m.widths['390'].records.height;
    return h !== null && h <= 0.55 * BASELINE.widths['390'].records.height;
  },
});

// almanacClipped is a COUNT (probe.mjs), not a nullable measurement -- but
// probe.mjs's own almanacIndex === -1 branch returns the sentinel 0 when the
// almanac card can't be found at all (e.g. .almanac-astro renamed or deleted),
// which is indistinguishable from "genuinely zero clipped leaves" on this
// field alone. Fix round 1: an earlier version of this guard used
// almanacMoonOffset !== null, which is gated on the CHILD .almanac-moon
// (probe.mjs) rather than the PARENT .almanac-astro that almanacClipped's
// sentinel actually keys on -- a rename of .almanac-astro alone, leaving the
// subtree (including .almanac-moon) intact, would have left that guard
// passing while the real clipping state was unknown. almanacFound is the
// direct fact (almanacIndex !== -1, probe.mjs), keyed on the same element as
// the sentinel it guards.
CHECKS.push({
  id: 'almanac-not-clipped',
  section: '§6.7',
  describe: (m) =>
    [390, 320]
      .map((w) =>
        m.widths[w].almanacFound
          ? `${w}: ${m.widths[w].almanacClipped}`
          : `${w}: ALMANAC CARD NOT FOUND (.almanac-astro)`
      )
      .join('  '),
  pass: (m) =>
    [390, 320].every((w) => m.widths[w].almanacFound && m.widths[w].almanacClipped === 0),
});

// The global criterion. Registered here because this is the last of the four
// contributors (type scale, ladder, hero wrap, almanac rules); every later task
// re-runs it, so it also guards them.
CHECKS.push({
  id: 'zero-clipped-leaves',
  section: '§6.2 + §6.6 + §6.7 / #178',
  describe: (m) => {
    const bad = WIDTHS.filter((w) => m.widths[w].clippedCount > 0);
    if (!bad.length) return '0 at all 19 widths';
    const w = bad[0];
    return `${bad.map((x) => `${x}:${m.widths[x].clippedCount}`).join(' ')} | first at ${w}: ` +
      JSON.stringify(m.widths[w].clipped.slice(0, 5));
  },
  pass: (m) => WIDTHS.every((w) => m.widths[w].clippedCount === 0),
});

// --- Task 9 / design §6.4 / #180 --------------------------------------------
// Both checks below are corrected from the plan's literal snippet: `null <= N`
// is `true` in JavaScript, so an absent `asymmetry`/`rowSpread` (a renamed or
// deleted probe field) would otherwise silently PASS instead of failing loud.
CHECKS.push({
  id: 'cards-fill-their-height',
  section: '§6.4 / #180',
  describe: (m) => {
    const before = BASELINE.widths['1512']?.asymmetry;
    const after = m.widths['1512']?.asymmetry;
    if (before == null || after == null) {
      return `MISSING asymmetry (baseline ${before} -> current ${after})`;
    }
    return `1512: ${before.toFixed(1)} -> ${after.toFixed(1)}`;
  },
  pass: (m) => {
    const before = BASELINE.widths['1512']?.asymmetry;
    const after = m.widths['1512']?.asymmetry;
    return before != null && after != null && after <= 0.6 * before;
  },
});

CHECKS.push({
  id: 'rows-stay-uniform',
  section: '§6.4 / #180 #182',
  describe: (m) => {
    const missing = WIDTHS.filter((w) => m.widths[w]?.rowSpread == null);
    if (missing.length) return `MISSING rowSpread at ${missing.join(', ')}`;
    const worst = WIDTHS.reduce(
      (a, w) => (m.widths[w].rowSpread > a.v ? { w, v: m.widths[w].rowSpread } : a),
      { w: null, v: -1 }
    );
    return `worst spread ${worst.v.toFixed(2)}px at ${worst.w}`;
  },
  pass: (m) =>
    WIDTHS.every((w) => m.widths[w]?.rowSpread != null && m.widths[w].rowSpread <= 1),
});

// The three hand-rolled copies of the adopted idiom must be gone -- leaving them
// is exactly the duplication design §4.3 is about -- and .glass-card must keep
// position: relative, because deleting `.rain-card { position: relative }` is
// only safe on the strength of it and .rain-animation depends on it.
CHECKS.push({
  id: 'per-card-interior-rules-deleted',
  section: '§6.4 / §4.3',
  describe: (m) =>
    `banned ${JSON.stringify(m.css.bannedFound)} glassRelative ${m.css.hasGlassRelative}`,
  pass: (m) => m.css.bannedFound.length === 0 && m.css.hasGlassRelative === true,
});

// B-1's regression gate: the rain card must centre in BOTH states. m.rain is a
// second server instance serving observations-current.rain.json, so
// .rain-animation is present as a conditional third child.
CHECKS.push({
  id: 'rain-card-centres-while-raining',
  section: '§6.4 / §15 B-1',
  describe: (m) => {
    const dry = m.widths['1512'].rainGrid;
    const wet = m.rain.rainGrid;
    return `dry ${dry.marginTop.toFixed(1)}/${dry.marginBottom.toFixed(1)} ` +
      `wet ${wet.marginTop.toFixed(1)}/${wet.marginBottom.toFixed(1)} ` +
      `(animation present: ${wet.hasAnimation})`;
  },
  pass: (m) => {
    const dry = m.widths['1512'].rainGrid;
    const wet = m.rain.rainGrid;
    return (
      wet.hasAnimation === true &&
      Math.abs(dry.marginTop - dry.marginBottom) <= 1 &&
      Math.abs(wet.marginTop - wet.marginBottom) <= 1
    );
  },
});

// --- Task 10 / design §6.9 / #188 -------------------------------------------
CHECKS.push({
  id: 'header-separator-spaced',
  section: '§6.9 / #188',
  describe: (m) => JSON.stringify(m.widths['1512'].header.locText),
  pass: (m) => /°[WE] ·/.test(m.widths['1512'].header.locText ?? ''),
});

// --- Task 11 / design §6.10 / #184 ------------------------------------------
CHECKS.push({
  id: 'maplibre-worker-emitted',
  section: '§6.10 / #184',
  describe: (m) => `file ${m.worker.workerFile ?? 'MISSING'} referenced ${m.worker.referenced}`,
  pass: (m) => m.worker.workerFile !== null && m.worker.referenced === true,
});

// Weaker than design §12's "/assets/maplibre-gl-worker-*.js returns 200",
// deliberately and on the record. loadRadar() runs only inside map.on('load')
// (RadarCard.tsx:234-243), so if MapLibre never initialises in headless
// Chromium the worker is never requested and a 200-required check would fail
// for a reason unrelated to this fix. Asserted instead: no worker request 404s,
// and any that WAS made returned 200. `maplibre-worker-emitted` above is the
// deterministic half and is the real gate.
CHECKS.push({
  id: 'no-worker-404',
  section: '§6.10 / #184',
  describe: (m) => {
    const hits = m.requestLog.filter((r) => /maplibre-gl-worker/.test(r.path));
    // /basemap/osm.pmtiles 404s BY DESIGN (PROVENANCE.md) and is not counted.
    return hits.length
      ? JSON.stringify(hits)
      : 'worker never requested (MapLibre did not initialise) -- see maplibre-worker-emitted';
  },
  pass: (m) =>
    m.requestLog
      .filter((r) => /maplibre-gl-worker/.test(r.path))
      .every((r) => r.status === 200),
});

// --- Task 13 / design §6.5 / Badge ------------------------------------------
// Deviation from the plan's verbatim snippet (disclosed per the branch's
// standing fail-loud-on-absent-data rule): `legacyBadges.length === 0` alone
// would pass if the five legacy rules were deleted and the .badge block was
// never added -- absent data reading as success. hasBadgeBlock is a required
// positive companion (=== true, not truthy, so a renamed/absent field can't
// coerce to a pass), and describe names which half failed.
CHECKS.push({
  id: 'one-badge-implementation',
  section: '§6.5 Badge',
  describe: (m) => {
    const parts = [];
    if (m.css.legacyBadges.length > 0) {
      parts.push(`legacy badge rules found: ${JSON.stringify(m.css.legacyBadges)}`);
    }
    if (m.css.hasBadgeBlock !== true) {
      parts.push('no .badge block in App.css');
    }
    return parts.length ? parts.join(' | ') : 'legacy rules gone, .badge block present';
  },
  pass: (m) => m.css.legacyBadges.length === 0 && m.css.hasBadgeBlock === true,
});

await main();
