// The in-page DOM probe. Three details are load-bearing (design §2):
//  1. clipping is measured on LEAF elements, not child boxes -- a wrapper that
//     grows to fill its card reads as "no slack" while its text sits at the top;
//  2. empty space is ASYMMETRY, not bottom slack -- bottom slack punishes the
//     deliberately-centred hero;
//  3. horizontal overflow is documentElement.scrollWidth - innerWidth --
//     index.css:49 sets body { overflow-x: hidden }, so measuring on body
//     reports zero everywhere and hides the defect.

export async function probe(page) {
  return page.evaluate(() => {
    const EPS = 0.5;

    const textLeaves = (root) =>
      Array.from(root.querySelectorAll('*')).filter(
        (el) =>
          el.children.length === 0 &&
          !(el instanceof SVGElement) &&
          (el.textContent ?? '').trim().length > 0 &&
          el.getClientRects().length > 0
      );

    const padBox = (card) => {
      const r = card.getBoundingClientRect();
      const cs = getComputedStyle(card);
      const bl = parseFloat(cs.borderLeftWidth);
      const br = parseFloat(cs.borderRightWidth);
      const bt = parseFloat(cs.borderTopWidth);
      const bb = parseFloat(cs.borderBottomWidth);
      return {
        left: r.left + bl,
        right: r.right - br,
        top: r.top + bt,
        bottom: r.bottom - bb,
      };
    };

    const cards = Array.from(document.querySelectorAll('.glass-card'));

    const clipped = [];
    let asymmetry = 0;
    const cardData = cards.map((card) => {
      const p = padBox(card);
      const leaves = textLeaves(card);
      let inkTop = Infinity;
      let inkBottom = -Infinity;
      for (const leaf of leaves) {
        const r = leaf.getBoundingClientRect();
        inkTop = Math.min(inkTop, r.top);
        inkBottom = Math.max(inkBottom, r.bottom);
        if (
          r.right > p.right + EPS ||
          r.left < p.left - EPS ||
          r.bottom > p.bottom + EPS ||
          r.top < p.top - EPS
        ) {
          clipped.push({
            cardIndex: cards.indexOf(card),
            card: card.className,
            cls: leaf.className,
            text: (leaf.textContent ?? '').trim().slice(0, 24),
          });
        }
      }
      const r = card.getBoundingClientRect();
      const asym =
        leaves.length === 0
          ? 0
          : Math.max(0, (p.bottom - inkBottom) - (inkTop - p.top));
      asymmetry += asym;
      return {
        className: card.className,
        top: Math.round(r.top),
        height: r.height,
        width: r.width,
        asym,
      };
    });

    // Row height spread: group by rounded top, take the worst max-min.
    const rows = new Map();
    for (const c of cardData) {
      if (!rows.has(c.top)) rows.set(c.top, []);
      rows.get(c.top).push(c.height);
    }
    let rowSpread = 0;
    for (const heights of rows.values()) {
      rowSpread = Math.max(rowSpread, Math.max(...heights) - Math.min(...heights));
    }

    const grid = document.querySelector('.dashboard-grid');
    const tracks = getComputedStyle(grid)
      .gridTemplateColumns.split(' ')
      .map(parseFloat)
      .filter((n) => !Number.isNaN(n));

    const q = (sel) => document.querySelector(sel);
    const box = (sel) => {
      const el = q(sel);
      return el ? el.getBoundingClientRect() : null;
    };
    const fontPx = (sel) => {
      const el = q(sel);
      return el ? parseFloat(getComputedStyle(el).fontSize) : null;
    };

    const heroContent = q('.hero-content');
    const rainGrid = q('.rain-grid');
    const rainCS = rainGrid ? getComputedStyle(rainGrid) : null;
    const header = q('.app-header');
    const name = q('.station-name');
    const loc = q('.station-location');

    const cardOf = (el) => el.closest('.glass-card');
    // Identify the almanac by INDEX, not by className: AlmanacCard and
    // ForecastStrip both render <GlassCard span={3}> and AlmanacCard passes no
    // className, so both produce the identical string "glass-card card-span-3 ".
    // Latent today only because the fixture has forecast: false.
    const almanacCard = q('.almanac-astro') ? cardOf(q('.almanac-astro')) : null;
    const almanacIndex = almanacCard ? cards.indexOf(almanacCard) : -1;
    // almanacFound names the fact directly rather than making every reader of
    // almanacClipped re-derive it from a sentinel: almanacClipped is 0 both
    // when the almanac genuinely has zero clipped leaves AND when
    // .almanac-astro can't be found at all (renamed/deleted), and those two
    // cases must not be conflated by a threshold that only checks `=== 0`.
    const almanacFound = almanacIndex !== -1;
    const almanacClipped = almanacIndex === -1
      ? 0
      : clipped.filter((c) => c.cardIndex === almanacIndex).length;

    const readoutSelectors = [
      '.pressure-value',
      '.humidity-value',
      '.uv-number',
      '.lightning-count',
      '.lightning-distance',
      '.wind-speed-value',
    ];
    const readouts = {};
    for (const sel of readoutSelectors) readouts[sel] = fontPx(sel);

    const centredWithin = (sel) => {
      const el = q(sel);
      if (!el) return null;
      const card = cardOf(el);
      if (!card) return null;
      const p = padBox(card);
      const r = el.getBoundingClientRect();
      return Math.abs((r.left + r.right) / 2 - (p.left + p.right) / 2);
    };

    const alignOf = (sel) => {
      const el = q(sel);
      return el ? getComputedStyle(el).alignItems : null;
    };

    return {
      viewport: { width: window.innerWidth, height: window.innerHeight },
      docOverflow: document.documentElement.scrollWidth - window.innerWidth,
      widestOffender: (() => {
        let worst = null;
        for (const el of document.querySelectorAll('*')) {
          const r = el.getBoundingClientRect();
          if (r.width === 0 && r.height === 0) continue;
          if (!worst || r.right > worst.right) {
            // SVGElement.className is an SVGAnimatedString whose toString() is
            // "[object SVGAnimatedString]" -- useless in the diagnostic Task 7
            // tells the executor to read. Fall back to the class attribute.
            const cls =
              typeof el.className === 'string'
                ? el.className
                : el.getAttribute('class') ?? `<${el.tagName.toLowerCase()}>`;
            worst = { right: r.right, cls };
          }
        }
        return worst;
      })(),
      tracks,
      trackSpread: tracks.length ? Math.max(...tracks) - Math.min(...tracks) : 0,
      gutter: grid ? window.innerWidth - grid.getBoundingClientRect().width : null,
      cards: cardData,
      cardCount: cards.length,
      clipped,
      clippedCount: clipped.length,
      almanacFound,
      almanacClipped,
      asymmetry,
      rowSpread,
      hero: heroContent
        ? {
            height: heroContent.getBoundingClientRect().height,
            scrollWidth: heroContent.scrollWidth,
            clientWidth: heroContent.clientWidth,
            detailsWidth: box('.hero-details')?.width ?? null,
          }
        : null,
      rainGrid: rainCS
        ? {
            marginTop: parseFloat(rainCS.marginTop),
            marginBottom: parseFloat(rainCS.marginBottom),
            hasAnimation: !!q('.rain-animation'),
          }
        : null,
      header: header
        ? {
            height: header.getBoundingClientRect().height,
            nameOverflow: name ? name.scrollWidth - name.clientWidth : null,
            locOverflow: loc ? loc.scrollWidth - loc.clientWidth : null,
            locText: loc ? (loc.textContent ?? '') : null,
            nameText: name ? (name.textContent ?? '').trim() : null,
          }
        : null,
      icons: Array.from(document.querySelectorAll('.card-icon')).map((el) => {
        const r = el.getBoundingClientRect();
        return { w: r.width, h: r.height };
      }),
      titleFontPx: fontPx('.card-title'),
      rootFontPx: parseFloat(getComputedStyle(document.documentElement).fontSize),
      records: { height: box('.records-card')?.height ?? null },
      readouts,
      // Converted readouts, read by structure rather than by the eight legacy
      // class names -- which vanish one card at a time through Phase 2.
      readoutValues: {
        primary: Array.from(document.querySelectorAll('.readout-primary .readout-value')).map(
          (el) => parseFloat(getComputedStyle(el).fontSize)
        ),
        hero: Array.from(document.querySelectorAll('.readout-hero .readout-value')).map(
          (el) => parseFloat(getComputedStyle(el).fontSize)
        ),
      },
      readoutCentre: Object.fromEntries(
        readoutSelectors.map((sel) => [sel, centredWithin(sel)])
      ),
      align: {
        rain: alignOf('.rain-stat-block'),
        health: alignOf('.health-item'),
        lightning: alignOf('.lightning-stat'),
        humidity: alignOf('.humidity-stat'),
      },
      // RadarCard's DOM differs by resolved status; recorded so a nondeterministic
      // WebGL/fetch race fails loudly rather than drifting the row heights.
      radarStatusMessage: !!q('.radar-status-message'),
      // .almanac-moon is position:absolute at left:50%. Task 8 removes
      // .almanac-sun's max-width cap, whose own comment says it exists to "keep
      // sun sections away from center moon" -- and an overlap is NOT clipping,
      // so nothing else here would see it.
      almanacSunMoonOverlap: (() => {
        const moon = q('.almanac-moon');
        if (!moon) return null;
        const mr = moon.getBoundingClientRect();
        return Array.from(document.querySelectorAll('.almanac-sun')).reduce((worst, sun) => {
          const sr = sun.getBoundingClientRect();
          const dx = Math.min(sr.right, mr.right) - Math.max(sr.left, mr.left);
          const dy = Math.min(sr.bottom, mr.bottom) - Math.max(sr.top, mr.top);
          return dx > 0 && dy > 0 ? Math.max(worst, dx) : worst;
        }, 0);
      })(),
      // Once the moon is back in flow, `justify-content: space-between` drops it
      // at whichever end of the wrapped line it lands on. Not overlapping and
      // not clipped -- just visibly off-centre, which only this measures.
      almanacMoonOffset: (() => {
        const moon = q('.almanac-moon');
        if (!moon) return null;
        const card = cardOf(moon);
        if (!card) return null;
        const p = padBox(card);
        const mr = moon.getBoundingClientRect();
        return Math.abs((mr.left + mr.right) / 2 - (p.left + p.right) / 2);
      })(),
      // --readout-primary reaches ~41px at 1512 inside a 140px ring. The card-level
      // clipping metric cannot see an overflow that stays inside the card.
      // Measured on the ring text's CHILDREN, not on .humidity-ring-text itself:
      // that element is `position: absolute; inset: 0` inside the container, so
      // its own rect is always exactly the container's box and can never report
      // an overflow. The number at risk is the readout INSIDE it.
      humidityRingOverflow: (() => {
        const ring = q('.humidity-ring-container');
        const text = q('.humidity-ring-text');
        if (!ring || !text) return null;
        const rr = ring.getBoundingClientRect();
        return Array.from(text.querySelectorAll('*')).reduce((worst, el) => {
          if (el.getClientRects().length === 0) return worst;
          const r = el.getBoundingClientRect();
          return Math.max(worst, r.right - rr.right, rr.left - r.left, r.bottom - rr.bottom, rr.top - r.top);
        }, 0);
      })(),
      // Every card's visible text, normalised. Phase 2 rewrites ten cards by hand;
      // no geometric threshold can see a dropped label or a swapped value, and #178
      // is literally "WIND CHILL and UV INDEX are absent". Frozen at the Task 12
      // gate and compared on every Phase 2 task.
      // Excludes anything MapLibre injects: RadarCard.tsx:174-177 adds an
      // AttributionControl rendering "© OpenStreetMap contributors" INSIDE the
      // card, so whether the map initialises would otherwise flip the radar
      // card's text between runs and report a drift no task caused.
      cardText: cards.map((card) =>
        textLeaves(card)
          .filter((el) => !el.closest('.radar-map-container'))
          .map((el) => (el.textContent ?? '').trim())
          .join('|')
      ),
    };
  });
}
