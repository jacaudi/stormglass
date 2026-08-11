import type { StationMeta } from '../types/weather';

/** Formats a lat/lon pair with correct hemisphere suffixes (N/S, E/W) derived from sign. */
export function formatCoord(latitude: number, longitude: number): string {
  const latSuffix = latitude < 0 ? 'S' : 'N';
  const lonSuffix = longitude < 0 ? 'W' : 'E';
  return `${Math.abs(latitude).toFixed(4)}°${latSuffix}, ${Math.abs(longitude).toFixed(4)}°${lonSuffix}`;
}

/**
 * Narrows a station to one that actually carries a location.
 *
 * StationMeta declares latitude/longitude as `number`, but /api/station is a
 * raw passthrough of WeatherFlow's response and returns HTTP 200 with a
 * status-only envelope when the server holds no token — so a truthy `station`
 * is not a usable one, and formatting it yields NaN. Two consumers share this
 * knowledge: the Header's location line and App's radar mount condition.
 */
export function hasCoordinates(station: StationMeta | null): station is StationMeta {
  return (
    station !== null &&
    typeof station.latitude === 'number' &&
    typeof station.longitude === 'number' &&
    Number.isFinite(station.latitude) &&
    Number.isFinite(station.longitude)
  );
}
