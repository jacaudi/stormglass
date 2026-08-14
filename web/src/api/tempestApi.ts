/**
 * Tempest data client -- Contract C (design §11).
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
async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { signal });
  if (!res.ok) {
    throw new Error(`${url} responded with ${res.status}`);
  }
  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// Station metadata -- best-effort WeatherFlow proxy (GET /api/station).
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
// Forecast -- best-effort WeatherFlow proxy (GET /api/forecast).
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
// this task. signalStrength and firmwareVersion have no source in Contract
// C and fall back to a safe default (0 / empty string). A failure here
// REJECTS like every other fetch* in this file (M5): the underlying
// observation fetch is the one slice useWeatherData's allSettled can retain
// the prior value for on failure, and swallowing the error into a fake
// "offline" default would instead overwrite a known-good status with one.
// ---------------------------------------------------------------------------
export async function fetchStationStatus(
  _deviceId?: number,
  signal?: AbortSignal
): Promise<StationStatus> {
  const obs = await fetchCurrentObservation(undefined, signal);
  const ageSeconds = Date.now() / 1000 - obs.timestamp;
  return {
    isOnline: ageSeconds <= STATION_ONLINE_THRESHOLD_SECONDS,
    lastReport: obs.timestamp,
    batteryLevel: obs.battery,
    signalStrength: 0,
    firmwareVersion: '',
  };
}

// ---------------------------------------------------------------------------
// Station almanac (historical highs/lows) -- best-effort WeatherFlow proxy
// (GET /api/almanac).
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
