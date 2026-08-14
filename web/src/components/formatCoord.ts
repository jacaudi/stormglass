import type { StationMeta, LocatedStation } from '../types/weather';

/** Formats a lat/lon pair with correct hemisphere suffixes (N/S, E/W) derived from sign. */
export function formatCoord(latitude: number, longitude: number): string {
  const latSuffix = latitude < 0 ? 'S' : 'N';
  const lonSuffix = longitude < 0 ? 'W' : 'E';
  return `${Math.abs(latitude).toFixed(4)}°${latSuffix}, ${Math.abs(longitude).toFixed(4)}°${lonSuffix}`;
}

/**
 * Narrows a station to one that actually carries a location.
 *
 * /api/station is served from this server's own configuration and OMITS
 * latitude/longitude unless both are set, so `station` being truthy does not
 * mean it has coordinates -- StationMeta declares them optional for exactly
 * that reason. Narrowing to LocatedStation gives consumers plain numbers.
 *
 * The `Number.isFinite` checks are retained deliberately: they cost nothing
 * and they are the guard that would catch a malformed value reaching the
 * client, which is the failure this function was written to prevent.
 */
export function hasCoordinates(station: StationMeta | null): station is LocatedStation {
  return (
    station !== null &&
    typeof station.latitude === 'number' &&
    typeof station.longitude === 'number' &&
    Number.isFinite(station.latitude) &&
    Number.isFinite(station.longitude)
  );
}
