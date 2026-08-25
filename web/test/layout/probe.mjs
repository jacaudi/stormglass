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
    // The interior's two auto margins sit on the FIRST body child and on the
    // LAST in-flow child. Those were the same element (.rain-grid) until the
    // card gained its window-total row, so reading both off .rain-grid now
    // reports 21.3/0.0 for a card that is in fact centred. Centring is the gap
    // ABOVE the first body child against the gap BELOW the last in-flow one --
    // record both ends and let the check compare them.
    const rainCard = rainGrid ? rainGrid.closest('.glass-card') : null;
    const rainInFlow = rainCard
      ? Array.from(rainCard.children).filter((el) => !el.classList.contains('rain-animation'))
      : [];
    const rainLast = rainInFlow.length ? rainInFlow[rainInFlow.length - 1] : null;
    const rainLastCS = rainLast ? getComputedStyle(rainLast) : null;
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

    // Single source for the three readoutValues.primary / readoutCentreOffsets
    // / readoutSharesRow fields below -- all three describe the same set of
    // elements from the same selector, so it is queried once.
    const readoutPrimaryValues = Array.from(
      document.querySelectorAll('.readout-primary .readout-value')
    );

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
            interiorTop: parseFloat(rainCS.marginTop),
            // null (never 0) when the last in-flow child can't be found, so a
            // renamed or emptied card fails the check loudly instead of
            // comparing 0 against 0 and reading as centred.
            interiorBottom: rainLastCS ? parseFloat(rainLastCS.marginBottom) : null,
            lastInFlow: rainLast ? rainLast.className : null,
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
      // Converted readouts, read by structure rather than by the eight legacy
      // class names -- which vanish one card at a time through Phase 2.
      readoutValues: {
        primary: readoutPrimaryValues.map((el) => parseFloat(getComputedStyle(el).fontSize)),
        hero: Array.from(document.querySelectorAll('.readout-hero .readout-value')).map(
          (el) => parseFloat(getComputedStyle(el).fontSize)
        ),
      },
      // .readout-value, NOT .readout: .readout is a block-level child of a
      // flex-column card with the default align-items: stretch, so it is
      // full-width and its centre equals the card's centre by construction --
      // measuring it would pass regardless of where the NUMBER sits.
      //
      // Sentinel fix (Task 24, disclosed per the branch's standing
      // fail-loud-on-absent-data rule): the brief's snippet returned 0 when
      // `.closest('.glass-card')` found no card, and 0 <= 2 passes -- the
      // identical shape to the almanacIndex === -1 -> 0 sentinel already found
      // and fixed elsewhere on this branch. A readout outside a .glass-card (or
      // a renamed .glass-card) must report "unmeasurable", not "perfectly
      // centred". Returns null; readouts-centred (measure.mjs) excludes null
      // before comparing, because `null <= 2` is true in JavaScript.
      readoutCentreOffsets: readoutPrimaryValues.map((el) => {
        const card = el.closest('.glass-card');
        if (!card) return null;
        const p = padBox(card);
        const r = el.getBoundingClientRect();
        return Math.abs((r.left + r.right) / 2 - (p.left + p.right) / 2);
      }),
      // True when this readout shares its StatRow with a sibling readout --
      // LightningCard.tsx renders lightning-count and lightning-distance side
      // by side in one <StatRow>. Two half-width columns cannot each
      // independently sit at the FULL CARD's centre (measured: both offset by
      // an equal ~110px at 1512, symmetric around the card centre -- the ROW is
      // centred, by construction of StatRow's centred grid; the two ITEMS
      // cannot be). readouts-centred (measure.mjs) uses this to scope its
      // per-item assertion to solo readouts, the only ones the design's "all
      // centred within 2px of their card's content-box centre" (§12) can
      // possibly hold for.
      readoutSharesRow: readoutPrimaryValues.map((el) => {
        const row = el.closest('.stat-row');
        if (!row) return false;
        return row.querySelectorAll('.readout-primary .readout-value').length > 1;
      }),
      // Companion to readoutSharesRow, so narrowing readouts-centred's per-item
      // assertion does not also remove ALL coverage of a shared row: even
      // though LightningCard's two columns cannot each individually centre on
      // the card, the pair should be MIRRORED around the card's centre (one
      // sits left, the other right, by equal amounts) -- that is what "the row
      // is centred" actually means for a 2-up StatRow. One value per distinct
      // .stat-row that holds 2+ primary readouts: the SIGNED per-item centre
      // offsets (not the absolute values readoutCentreOffsets stores) summed
      // and taken as one magnitude -- a genuinely symmetric pair sums to ~0
      // (a left offset and an equal-magnitude right offset cancel); a row that
      // became lopsided as a WHOLE would not.
      //
      // Rejected: leftmost-edge-to-rightmost-edge of the group's ink extent.
      // Each `.readout-value` is individually centred within ITS OWN column
      // via `.readout`'s `text-align: center` on a full-width block parent, so
      // its rect's CENTRE equals its column's centre regardless of the text's
      // own width -- but its EDGES do not, when the two readouts hold
      // different-length text (e.g. "3" vs "12.4 km"). Measured: the edge-based
      // version read 2.1-3.3px of "imbalance" on a row already proven
      // symmetric by readoutCentreOffsets (110.5 / 110.5, equal to four
      // decimal places) -- an artifact of unequal text width, not a real
      // off-centre row. Signed centre offsets are invariant to that.
      readoutRowGroupOffsets: (() => {
        const groups = new Map();
        for (const el of readoutPrimaryValues) {
          const row = el.closest('.stat-row');
          if (!row) continue;
          if (!groups.has(row)) groups.set(row, []);
          groups.get(row).push(el);
        }
        const offsets = [];
        for (const [row, els] of groups) {
          if (els.length < 2) continue;
          const card = cardOf(row);
          if (!card) {
            offsets.push(null);
            continue;
          }
          const p = padBox(card);
          const cardCentre = (p.left + p.right) / 2;
          const signedSum = els.reduce((sum, el) => {
            const r = el.getBoundingClientRect();
            return sum + ((r.left + r.right) / 2 - cardCentre);
          }, 0);
          offsets.push(Math.abs(signedSum));
        }
        return offsets;
      })(),
      statAlignments: Array.from(document.querySelectorAll('.stat')).map(
        (el) => getComputedStyle(el).alignItems
      ),
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
        const suns = Array.from(document.querySelectorAll('.almanac-sun'));
        // Sibling fix to the `.almanac-moon` null guard above (identical shape
        // to the almanacIndex === -1 -> 0 sentinel this branch already found and
        // fixed elsewhere): `.reduce` over an EMPTY array returns its seed, 0,
        // unconditionally -- a renamed/deleted `.almanac-sun` would read as "no
        // overlap" instead of "unmeasurable". Fail loud instead.
        if (suns.length === 0) return null;
        const mr = moon.getBoundingClientRect();
        return suns.reduce((worst, sun) => {
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
        const descendants = Array.from(text.querySelectorAll('*'));
        // Same shape as almanacSunMoonOverlap's fix above: `.reduce` over an
        // EMPTY array returns its seed, 0, unconditionally -- an
        // element-child-free `.humidity-ring-text` (the Readout markup
        // changing to bare text, or the readout being removed) would read as
        // "fits" instead of "unmeasurable". Fail loud instead.
        if (descendants.length === 0) return null;
        return descendants.reduce((worst, el) => {
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
