import { useEffect, useRef, useState } from 'react';
import maplibregl from 'maplibre-gl';
import 'maplibre-gl/dist/maplibre-gl.css';
import { Protocol } from 'pmtiles';
import type { StationMeta } from '../types/weather';
import { GlassCard } from './GlassCard';

/**
 * NEXRAD reflectivity radar card (design §9/§14 P1.8, decision B2).
 *
 * The basemap is OpenStreetMap served as a same-origin Protomaps `.pmtiles`
 * file via the `pmtiles://` protocol -- no external tile server, no API
 * key, matching the self-only CSP (Task 1.3). Per user decision "Option C"
 * that basemap file is NOT committed or build-fetched: it's an
 * operator-supplied asset dropped at `web/public/basemap/osm.pmtiles` at
 * deploy time (see `web/public/basemap/PROVENANCE.md`). When it's absent,
 * MapLibre's tile requests 404 and the map simply renders without data --
 * that's an intentional, graceful degrade, not a bug.
 *
 * Radar reflectivity itself comes from this server's own `/api/radar/{site}`
 * (Contract A GeoJSON, opt-in ENABLE_RADAR) and is drawn as a fill layer
 * colored by the `dbz_min` band property.
 */

const BASEMAP_URL = 'pmtiles:///basemap/osm.pmtiles';
const BASEMAP_SOURCE_ID = 'basemap';
const OSM_ATTRIBUTION = '© OpenStreetMap contributors';

const RADAR_SOURCE_ID = 'radar-reflectivity';
const RADAR_LAYER_ID = 'radar-reflectivity-fill';
// NEXRAD Level 3 base reflectivity scans roughly every 5 minutes; polling
// faster would just refetch an unchanged sidecar-cached response.
const RADAR_REFRESH_MS = 5 * 60 * 1000;
const DEFAULT_ZOOM = 7;

type RadarStatus = 'loading' | 'ok' | 'unavailable' | 'not-configured';

export interface RadarCardProps {
  station: StationMeta;
  // WSR-88D site code for /api/radar/{site} (e.g. "TLX"). There is currently
  // no client-side nearest-site lookup (flagged as a follow-up -- see the
  // task report); callers that don't have one yet should omit this prop and
  // the card degrades to the "not configured" state rather than guessing.
  site?: string;
}

// maplibregl.addProtocol is a global registry keyed by scheme name -- it
// only needs to happen once per page load, not once per RadarCard mount.
let pmtilesProtocolRegistered = false;
function ensurePmtilesProtocolRegistered(): void {
  if (pmtilesProtocolRegistered) return;
  const protocol = new Protocol();
  maplibregl.addProtocol('pmtiles', protocol.tile);
  pmtilesProtocolRegistered = true;
}

function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

// A minimal, best-effort default cartography over the Protomaps basemap
// schema (earth/water/roads/buildings) -- enough to read the map under the
// radar overlay without pulling in a whole theming library (protomaps-
// themes-base) for a single, non-interactive dashboard card.
function buildBasemapStyle() {
  return {
    version: 8 as const,
    sources: {
      [BASEMAP_SOURCE_ID]: {
        type: 'vector' as const,
        url: BASEMAP_URL,
      },
    },
    layers: [
      {
        id: 'background',
        type: 'background' as const,
        paint: { 'background-color': '#e8e6df' },
      },
      {
        id: 'earth',
        type: 'fill' as const,
        source: BASEMAP_SOURCE_ID,
        'source-layer': 'earth',
        paint: { 'fill-color': '#f2f0e8' },
      },
      {
        id: 'water',
        type: 'fill' as const,
        source: BASEMAP_SOURCE_ID,
        'source-layer': 'water',
        paint: { 'fill-color': '#a8cce0' },
      },
      {
        id: 'roads',
        type: 'line' as const,
        source: BASEMAP_SOURCE_ID,
        'source-layer': 'roads',
        paint: { 'line-color': '#c9c4b8', 'line-width': 1 },
      },
      {
        id: 'buildings',
        type: 'fill' as const,
        source: BASEMAP_SOURCE_ID,
        'source-layer': 'buildings',
        minzoom: 14,
        paint: { 'fill-color': '#d8d4c8' },
      },
    ],
  };
}

interface RadarErrorEnvelope {
  error: string;
}

function isErrorEnvelope(body: unknown): body is RadarErrorEnvelope {
  return typeof body === 'object' && body !== null && 'error' in body;
}

// Contract A's response shape (design §9/§14 P1.8): a GeoJSON
// FeatureCollection whose Feature.properties carry the `dbz_min`/`dbz_max`
// band bounds. Modeled minimally here (not the full @types/geojson shape --
// that ambient global isn't wired into this project's restricted tsconfig
// `types` list, and RadarCard only ever reads/passes this data through, it
// doesn't construct or validate geometry) so maplibre-gl's own GeoJSON.*
// type references don't need to be resolved by this file.
interface RadarFeatureCollection {
  type: 'FeatureCollection';
  features: unknown[];
}

export function RadarCard({ station, site }: RadarCardProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [status, setStatus] = useState<RadarStatus>(site ? 'loading' : 'not-configured');

  useEffect(() => {
    if (!site || !containerRef.current) return;

    ensurePmtilesProtocolRegistered();

    let map: maplibregl.Map;
    try {
      map = new maplibregl.Map({
        container: containerRef.current,
        style: buildBasemapStyle(),
        center: [station.longitude, station.latitude],
        zoom: DEFAULT_ZOOM,
        attributionControl: false,
      });
    } catch {
      // MapLibre/WebGL init failure (e.g. no GL context available) --
      // degrade to the same unavailable state as any other radar failure
      // rather than crashing the card.
      //
      // Constructing the map is textbook external-system synchronisation: it
      // needs the DOM node, so it can only happen in an effect, and its
      // failure has nowhere to go but state. The rule targets cascading
      // renders; this runs once, on a terminal error path. The sibling
      // setStatus calls below are not flagged because they sit in async
      // callbacks -- the only difference is this one throws synchronously.
      // eslint-disable-next-line react-hooks/set-state-in-effect -- see above
      setStatus('unavailable');
      return;
    }

    map.addControl(
      new maplibregl.AttributionControl({ customAttribution: OSM_ATTRIBUTION }),
      'bottom-right'
    );

    // A missing/404 basemap .pmtiles surfaces as a MapLibre 'error' event,
    // not a thrown exception -- swallow it so it never looks like a crash.
    // The map still renders (empty basemap, radar overlay unaffected) by
    // design; see web/public/basemap/PROVENANCE.md.
    map.on('error', () => {});

    let sourceAdded = false;
    const loadRadar = async () => {
      try {
        const res = await fetch(`/api/radar/${site}`);
        const body: unknown = await res.json();
        if (!res.ok || isErrorEnvelope(body)) {
          setStatus('unavailable');
          return;
        }

        // body isn't the ambient @types/geojson `GeoJSON.GeoJSON` type (see
        // RadarFeatureCollection's comment); double-cast through `unknown`
        // since it's structurally what maplibre-gl's GeoJSON source expects
        // (a FeatureCollection) without naming that unresolvable namespace.
        const geojson = body as unknown as RadarFeatureCollection;

        if (!sourceAdded) {
          map.addSource(RADAR_SOURCE_ID, { type: 'geojson', data: geojson });
          map.addLayer({
            id: RADAR_LAYER_ID,
            type: 'fill',
            source: RADAR_SOURCE_ID,
            paint: {
              // NWS-style reflectivity ramp, data-driven off the Contract A
              // `dbz_min` band bound.
              'fill-color': [
                'interpolate',
                ['linear'],
                ['get', 'dbz_min'],
                5, '#04e9e7',
                20, '#01ff00',
                35, '#ffff00',
                50, '#ff0000',
                65, '#ff00ff',
              ],
              'fill-opacity': 0.6,
            },
          });
          sourceAdded = true;
        } else {
          const source = map.getSource(RADAR_SOURCE_ID) as maplibregl.GeoJSONSource | undefined;
          source?.setData(geojson);
        }
        setStatus('ok');
      } catch {
        setStatus('unavailable');
      }
    };

    map.on('load', () => {
      // Respect prefers-reduced-motion: only pan/animate into the station's
      // position when motion is allowed. The initial `center`/`zoom` above
      // already place the map correctly either way -- this is purely the
      // animated "fly in", which reduced-motion users skip.
      if (!prefersReducedMotion()) {
        map.easeTo({ center: [station.longitude, station.latitude], zoom: DEFAULT_ZOOM, duration: 500 });
      }
      loadRadar();
    });

    const intervalId = window.setInterval(loadRadar, RADAR_REFRESH_MS);

    return () => {
      window.clearInterval(intervalId);
      map.remove();
    };
  }, [site, station.latitude, station.longitude]);

  if (!site) {
    return (
      <GlassCard className="radar-card">
        <RadarCardHeader />
        <p className="radar-status-message">Radar not configured for this station.</p>
      </GlassCard>
    );
  }

  return (
    <GlassCard className="radar-card" span={2}>
      <RadarCardHeader />
      <div ref={containerRef} className="radar-map-container" data-testid="radar-map-container" />
      {status === 'unavailable' && <p className="radar-status-message">Radar unavailable.</p>}
    </GlassCard>
  );
}

function RadarCardHeader() {
  return (
    <div className="card-header">
      <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="2" />
        <path d="M12 2a10 10 0 0 1 10 10" />
        <path d="M12 6a6 6 0 0 1 6 6" />
      </svg>
      <span className="card-title">Radar</span>
    </div>
  );
}
