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
  return { bannedFound: found, hasGlassRelative };
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
  /* Selector note: Task 13 renames .status-badge to .badge. When it does, it
     updates BOTH this revert block and Task 7's rule in App.css -- they are two
     halves of one comparison and must name the same element. */
  .status-badge  { padding: 0.3rem 0.75rem; font-size: 0.75rem; }
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

await main();
