/**
 * Stormglass data client -- Contract C (design §11).
 *
 * Every fetch below hits this server's own tokenless, same-origin JSON API
 * (never WeatherFlow directly; the browser never holds a token). Two
 * reliability tiers:
 *   - fetchCurrentObservation is the core: GET /api/observations/current
 *     reads this station's own SQLite store and works in UDP mode with no
 *     TOKEN configured.
 *   - fetchStationMeta/fetchStationAlmanac read this server's own
 *     configuration and SQLite store (GET /api/station, /api/almanac). They
 *     are tokenless and return this file's declared types exactly.
 *   - fetchForecast has no provider until issue #81; /api/forecast is not
 *     registered and capabilities.forecast is false, so useWeatherData never
 *     calls it.
 */

import type {
  CurrentObservation,
  StationMeta,
  ForecastDay,
  StationStatus,
  StationAlmanac,
  RecordsSummary,
  RecordsWindowDays,
  Capabilities,
} from '../types/weather';

// Single-sourced so the endpoint path used by a fetch* function and the one
// asserted in tests/read in useWeatherData can never drift apart -- an
// external contract (Contract C's URL shape), Tier A DRY.
const ENDPOINTS = {
  current: '/api/observations/current',
  station: '/api/station',
  forecast: '/api/forecast',
  almanac: '/api/almanac',
  summary: '/api/observations/summary',
  capabilities: '/api/capabilities',
} as const;

// A report older than this is treated as "station offline" by
// fetchStationStatus's derivation below -- several multiples of the
// station's typical ~1-minute report cadence, enough to absorb a couple of
// missed/delayed reports without flapping. No authoritative source; a
// judgment call, same spirit as observations.go's pressureTrendWindow.
const STATION_ONLINE_THRESHOLD_SECONDS = 5 * 60;

// The "fetch, reject non-OK, parse JSON" sequence is identical for every
// endpoint below -- shared knowledge (how a Contract C response is read),
// not just shared shape, so it is written once here.
// Carries the HTTP status alongside the message so callers can classify a
// failure rather than only report it. /api/observations/current answers 404
// when the store is simply empty -- a normal state on a fresh deployment, not
// a fault -- and a bare Error made that indistinguishable from a real outage
// (#218).
export class ApiError extends Error {
  readonly status: number;

  constructor(url: string, status: number) {
    super(`${url} responded with ${status}`);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
  if (!res.ok) {
    throw new ApiError(url, res.status);
  }
  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// Station metadata -- this server's own STATION_* configuration
// (GET /api/station). Tokenless.
// ---------------------------------------------------------------------------
export async function fetchStationMeta(
  _stationId?: number,
  signal?: AbortSignal
): Promise<StationMeta> {
  return getJSON<StationMeta>(ENDPOINTS.station, signal);
}

// ---------------------------------------------------------------------------
// Current observation -- the core, real endpoint (GET /api/observations/current).
// ---------------------------------------------------------------------------
export async function fetchCurrentObservation(
  _stationId?: number,
  signal?: AbortSignal
): Promise<CurrentObservation> {
  return getJSON<CurrentObservation>(ENDPOINTS.current, signal);
}

// ---------------------------------------------------------------------------
// Forecast -- has no provider until issue #81. GET /api/forecast is not
// registered and capabilities.forecast is a constant false, so
// useWeatherData never calls this function; it exists for when a provider
// lands.
// ---------------------------------------------------------------------------
export async function fetchForecast(
  _stationId?: number,
  signal?: AbortSignal
): Promise<ForecastDay[]> {
  return getJSON<ForecastDay[]>(ENDPOINTS.forecast, signal);
}

// ---------------------------------------------------------------------------
// Station status / health -- Contract C has no dedicated status endpoint
// (design §11), so this derives a best-effort StationStatus from the latest
// CurrentObservation rather than inventing a server route out of scope for
// this task. As of #196 signalDbm and firmwareVersion DO have a source: the
// server serves them on the current-observation response, sourced from the
// newest device_status row for this station's serial. They stay nullable --
// the server sends null when there is no row, when the newest is stale, or
// when its query failed -- because 0 dBm is a valid reading and cannot double
// as the unknown sentinel, and a blank FIRMWARE is absent data presented as a
// reading. A failure here
// REJECTS like every other fetch* in this file (M5): the underlying
// observation fetch is the one slice useWeatherData's allSettled can retain
// the prior value for on failure, and swallowing the error into a fake
// "offline" default would instead overwrite a known-good status with one.
// ---------------------------------------------------------------------------
export async function fetchStationStatus(
  _deviceId?: number,
  signal?: AbortSignal
): Promise<StationStatus> {
  return stationStatusFrom(await fetchCurrentObservation(undefined, signal));
}

// stationStatusFrom is the pure derivation, exported so the 30-second poll can
// refresh status from the observation it ALREADY fetched instead of issuing a
// second request -- or worse, not refreshing status at all. Before #196 the
// poll updated `current` and never touched `status`, so anything derived here
// was frozen at page load for the lifetime of the tab. That is the same defect
// #89 fixed for the Records card.
export function stationStatusFrom(obs: CurrentObservation): StationStatus {
  const ageSeconds = Date.now() / 1000 - obs.timestamp;
  return {
    isOnline: ageSeconds <= STATION_ONLINE_THRESHOLD_SECONDS,
    lastReport: obs.timestamp,
    batteryLevel: obs.battery,
    // #196: these now come from device_status via the current-observation
    // response. Still nullable, and still null-honest -- the server sends
    // null when there is no row, when it is stale, or when its query failed,
    // and 0 dBm is a real reading that must not collapse into that.
    signalDbm: obs.signalDbm,
    firmwareVersion: obs.firmwareVersion,
  };
}

// ---------------------------------------------------------------------------
// Station almanac (historical highs/lows) -- computed from the local SQLite
// store plus computed astronomy (GET /api/almanac). Tokenless.
// ---------------------------------------------------------------------------
export async function fetchStationAlmanac(
  _stationId?: number,
  signal?: AbortSignal
): Promise<StationAlmanac> {
  return getJSON<StationAlmanac>(ENDPOINTS.almanac, signal);
}

// ---------------------------------------------------------------------------
// Records summary -- the core, real endpoint
// (GET /api/observations/summary?days=N).
// ---------------------------------------------------------------------------
export async function fetchRecordsSummary(
  days: RecordsWindowDays,
  signal?: AbortSignal
): Promise<RecordsSummary> {
  return getJSON<RecordsSummary>(`${ENDPOINTS.summary}?days=${days}`, signal);
}

// ---------------------------------------------------------------------------
// Capabilities -- which optional cards the server has enabled
// (GET /api/capabilities). Static, no dependencies server-side, so a failure
// here means the server is unreachable rather than a feature being off; the
// caller treats a rejection as "unknown" and fails closed.
// ---------------------------------------------------------------------------
export async function fetchCapabilities(signal?: AbortSignal): Promise<Capabilities> {
  return getJSON<Capabilities>(ENDPOINTS.capabilities, signal);
}
