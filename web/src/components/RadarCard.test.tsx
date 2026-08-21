import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { RadarCard } from './RadarCard';
import type { LocatedStation } from '../types/weather';

// --- MapLibre GL mock -------------------------------------------------
// jsdom has no WebGL, so the real maplibre-gl Map would throw on
// construction. We replace the whole module with lightweight fakes that
// record calls so behavior (protocol registration, layer styling,
// attribution, pan calls) can be asserted without a real GL context.
// vi.mock factories are hoisted above all other module code, so anything
// they reference must be created via vi.hoisted rather than a plain
// top-level const/class.
const { mapInstances, attributionControlInstances, setDataMock, addProtocolMock, MockMap, MockAttributionControl } =
  vi.hoisted(() => {
    const mapInstances: Array<{
      options: Record<string, unknown>;
      handlers: Record<string, Array<(...args: unknown[]) => void>>;
      addSource: ReturnType<typeof vi.fn>;
      addLayer: ReturnType<typeof vi.fn>;
      getSource: ReturnType<typeof vi.fn>;
      easeTo: ReturnType<typeof vi.fn>;
      addControl: ReturnType<typeof vi.fn>;
      remove: ReturnType<typeof vi.fn>;
      fire: (event: string) => void;
    }> = [];

    const setDataMock = vi.fn();
    const addProtocolMock = vi.fn();
    const attributionControlInstances: Array<Record<string, unknown>> = [];

    class MockMap {
      options: Record<string, unknown>;
      handlers: Record<string, Array<(...args: unknown[]) => void>> = {};
      addSource = vi.fn();
      addLayer = vi.fn();
      getSource = vi.fn(() => ({ setData: setDataMock }));
      easeTo = vi.fn();
      addControl = vi.fn();
      remove = vi.fn();

      constructor(options: Record<string, unknown>) {
        this.options = options;
        mapInstances.push(this);
      }

      on(event: string, cb: (...args: unknown[]) => void) {
        this.handlers[event] = [...(this.handlers[event] ?? []), cb];
      }

      // Test helper: fires all handlers registered for `event`.
      fire(event: string) {
        (this.handlers[event] ?? []).forEach((cb) => cb());
      }
    }

    class MockAttributionControl {
      options: Record<string, unknown>;
      constructor(options: Record<string, unknown> = {}) {
        this.options = options;
        attributionControlInstances.push(options);
      }
    }

    return { mapInstances, attributionControlInstances, setDataMock, addProtocolMock, MockMap, MockAttributionControl };
  });

// Flat, not wrapped in `default`: RadarCard now uses a namespace import, so
// `maplibregl.Map` resolves against the module's top-level exports. Leaving the
// `default: {...}` shape here would typecheck fine and then fail at run time
// with "maplibregl.Map is not a constructor".
vi.mock('maplibre-gl', () => ({
  Map: MockMap,
  AttributionControl: MockAttributionControl,
  addProtocol: addProtocolMock,
  setWorkerUrl: vi.fn(),
}));

vi.mock('pmtiles', () => ({
  Protocol: class MockProtocol {
    tile = vi.fn();
  },
}));

const STATION: LocatedStation = {
  name: 'Test Station',
  latitude: 35.4,
  longitude: -97.6,
  elevation: 365,
};

const RADAR_GEOJSON = {
  type: 'FeatureCollection',
  features: [
    {
      type: 'Feature',
      properties: { dbz_min: 20, dbz_max: 25 },
      geometry: { type: 'Polygon', coordinates: [[[0, 0], [1, 0], [1, 1], [0, 0]]] },
    },
  ],
  metadata: { site: 'TLX', product: 'N0B', scan_time: '2026-07-20T00:00:00Z', bbox: [-98, 35, -97, 36] },
};

function setMatchMedia(matches: boolean) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

beforeEach(() => {
  mapInstances.length = 0;
  attributionControlInstances.length = 0;
  setDataMock.mockClear();
  // Deliberately NOT cleared: RadarCard registers the pmtiles:// protocol
  // once per page load (a module-level singleton guard, matching
  // maplibregl.addProtocol's own global-registry semantics), so only the
  // first RadarCard mount across this whole test file actually calls it.
  // Asserting on cumulative call history is the correct check for a
  // once-ever side effect.
  setMatchMedia(false);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('RadarCard basemap + protocol registration', () => {
  it('renders a map container', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    render(<RadarCard station={STATION} site="TLX" />);
    expect(screen.getByTestId('radar-map-container')).toBeInTheDocument();
  });

  it('registers the pmtiles:// protocol and points the basemap source at the same-origin file', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    render(<RadarCard station={STATION} site="TLX" />);

    expect(addProtocolMock).toHaveBeenCalledWith('pmtiles', expect.any(Function));

    const map = mapInstances[mapInstances.length - 1];
    const style = map.options.style as { sources: Record<string, { url?: string }> };
    const basemapSource = Object.values(style.sources).find((s) => s.url?.startsWith('pmtiles://'));
    expect(basemapSource?.url).toBe('pmtiles:///basemap/osm.pmtiles');
  });

  it('renders a visible OSM AttributionControl', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    render(<RadarCard station={STATION} site="TLX" />);

    const attribution = attributionControlInstances.find((opts) =>
      String(opts.customAttribution ?? '').includes('OpenStreetMap')
    );
    expect(attribution?.customAttribution).toBe('© OpenStreetMap contributors');
  });
});

describe('RadarCard radar data layer', () => {
  it('adds a fill layer styled by dbz_min once radar GeoJSON loads', async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve({
        ok: true,
        json: () => Promise.resolve(RADAR_GEOJSON),
      })
    );
    vi.stubGlobal('fetch', fetchMock);

    render(<RadarCard station={STATION} site="TLX" />);
    const map = mapInstances[mapInstances.length - 1];
    map.fire('load');

    await waitFor(() => expect(map.addLayer).toHaveBeenCalled());

    // Pins the SITE VALUE, not merely its truthiness: `site="TLX"` must
    // reach the fetch URL as the WSR-88D site code. Every other assertion in
    // this file only checks that SOME site is/isn't configured, so a caller
    // that passed the wrong prop through (e.g. station.name instead of the
    // radar site code) would pass every one of them as long as the value
    // was truthy.
    expect(fetchMock).toHaveBeenCalledWith('/api/radar/TLX');

    const layerCall = map.addLayer.mock.calls.find(
      (call) => call[0]?.type === 'fill' && call[0]?.source === 'radar-reflectivity'
    );
    expect(layerCall).toBeDefined();
    const paint = layerCall![0].paint;
    // Data-driven expression keyed on the dbz_min property (Contract A band bound).
    expect(JSON.stringify(paint['fill-color'])).toContain('dbz_min');
  });

  it('renders a graceful "radar unavailable" state on an {error} envelope response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve({
          ok: false,
          json: () => Promise.resolve({ error: 'no_recent_scan' }),
        })
      )
    );

    render(<RadarCard station={STATION} site="TLX" />);
    const map = mapInstances[mapInstances.length - 1];
    map.fire('load');

    expect(await screen.findByText(/radar unavailable/i)).toBeInTheDocument();
  });

  it('renders a graceful "radar unavailable" state when the fetch itself rejects', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('network down'))));

    render(<RadarCard station={STATION} site="TLX" />);
    const map = mapInstances[mapInstances.length - 1];
    map.fire('load');

    expect(await screen.findByText(/radar unavailable/i)).toBeInTheDocument();
  });
});

describe('RadarCard reduced motion', () => {
  it('does not auto-pan/animate the map when prefers-reduced-motion is set', () => {
    setMatchMedia(true);
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));

    render(<RadarCard station={STATION} site="TLX" />);
    const map = mapInstances[mapInstances.length - 1];
    map.fire('load');

    expect(map.easeTo).not.toHaveBeenCalled();
  });

  it('pans/animates the map on load when prefers-reduced-motion is not set', () => {
    setMatchMedia(false);
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));

    render(<RadarCard station={STATION} site="TLX" />);
    const map = mapInstances[mapInstances.length - 1];
    map.fire('load');

    expect(map.easeTo).toHaveBeenCalled();
  });
});

describe('RadarCard missing site', () => {
  it('renders a graceful "not configured" state and does not attempt to create a map when site is absent', () => {
    render(<RadarCard station={STATION} />);
    expect(screen.getByText(/radar not configured/i)).toBeInTheDocument();
    expect(mapInstances.length).toBe(0);
  });
});

describe('RadarCard site wiring', () => {
  it('leaves the card in the not-configured state without a site', () => {
    render(<RadarCard station={STATION} />);
    expect(screen.getByText('Radar not configured for this station.')).toBeInTheDocument();
  });

  it('leaves the not-configured state once a site is supplied', () => {
    render(<RadarCard station={STATION} site="TLX" />);
    expect(screen.queryByText('Radar not configured for this station.')).not.toBeInTheDocument();
  });
});
