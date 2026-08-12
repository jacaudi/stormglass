import { useState, useEffect, useCallback, useRef } from 'react';
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
import {
  fetchCurrentObservation,
  fetchStationMeta,
  fetchForecast,
  fetchStationStatus,
  fetchStationAlmanac,
  fetchRecordsSummary,
  fetchCapabilities,
} from '../api/tempestApi';

// There is no WebSocket backend (Contract C is plain JSON, see design §11),
// so live-ness comes from polling the core observation instead. 30s keeps
// the UI reasonably fresh against the station's own ~1-minute report
// cadence without hammering the read path; not derived from anything
// authoritative, just a reasonable middle ground.
export const POLL_INTERVAL_MS = 30_000;

// Empty value for a disabled forecast slice. Hoisted to a module constant so a
// fresh [] per load doesn't defeat React's Object.is bail-out on the cards that
// consume it. (The almanac's disabled value is `null`, which is already a
// singleton -- it needs no constant.)
const NO_FORECAST: ForecastDay[] = [];

// How long to wait for the capability document before giving up and failing
// closed. The first data load is gated on this fetch SETTLING, so without a
// deadline a request that hangs -- rather than fails -- would hold the
// dashboard on its loading screen indefinitely, including the core observation
// path that has no other dependency on capabilities.
export const CAPABILITIES_TIMEOUT_MS = 5_000;

export interface WeatherData {
  station: StationMeta | null;
  current: CurrentObservation | null;
  forecast: ForecastDay[];
  status: StationStatus | null;
  almanac: StationAlmanac | null;
  summary: RecordsSummary | null;
  isLoading: boolean;
  error: string | null;
  lastUpdated: Date | null;
  // True when the most recent attempt to refresh the core observation
  // failed and the data shown is therefore held over from an earlier,
  // successful fetch (§14 P1.6). False immediately after a successful
  // refresh.
  isStale: boolean;
  // Which optional cards the server has enabled. `null` means unknown --
  // either not yet fetched or the fetch failed -- and is treated exactly like
  // all-false, so a card is never mounted on a guess (issue #145).
  capabilities: Capabilities | null;
  refresh: () => void;
}

// Applies a settled slice's result to its setter if it fulfilled, leaving
// prior state untouched otherwise -- the "retain on failure" rule every
// non-core slice (station/forecast/status/almanac) shares below.
function applySettled<T>(
  result: PromiseSettledResult<T>,
  setValue: (value: T) => void
): void {
  if (result.status === 'fulfilled') {
    setValue(result.value);
  }
}

function describeError(result: PromiseRejectedResult): string {
  return result.reason instanceof Error
    ? result.reason.message
    : 'Failed to load weather data';
}

export function useWeatherData(
  stationId?: number,
  recordsWindowDays: RecordsWindowDays = 7
): WeatherData {
  const [station, setStation] = useState<StationMeta | null>(null);
  const [current, setCurrent] = useState<CurrentObservation | null>(null);
  const [forecast, setForecast] = useState<ForecastDay[]>([]);
  const [status, setStatus] = useState<StationStatus | null>(null);
  const [almanac, setAlmanac] = useState<StationAlmanac | null>(null);
  const [summary, setSummary] = useState<RecordsSummary | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [isStale, setIsStale] = useState(false);
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [capsSettled, setCapsSettled] = useState(false);
  // Bumped by refresh() to re-run the records-summary effect below. The
  // summary is not part of loadData's request set (it is keyed on window, not
  // stationId, and reads the local store rather than the WeatherFlow proxy),
  // so a counter in that effect's dependency array is what lets the manual
  // Refresh button reach it -- while leaving the effect's own per-run
  // AbortController and stale-retain behaviour exactly as they were.
  const [summaryNonce, setSummaryNonce] = useState(0);

  // Stable booleans, NOT `capabilities?.forecast === true` inline in a
  // dependency array -- a computed expression there is an eslint error
  // (react-hooks/use-memo). Comparing with === true also makes the
  // null -> false transition a no-op, so a default deployment never re-runs
  // loadData when capabilities arrive.
  const forecastEnabled = capabilities?.forecast === true;
  const almanacEnabled = capabilities?.almanac === true;

  const loadCapabilities = useCallback(async (signal?: AbortSignal) => {
    // Two reasons to abort, combined into one controller: the caller's signal
    // (unmount or a superseding call) and our own deadline. An abort rejects,
    // which the catch treats as unknown, which fails closed and lets the load
    // proceed -- unlike a hang, which would hold the settle-gate shut forever.
    // Deliberately a plain AbortController + setTimeout rather than
    // AbortSignal.any/AbortSignal.timeout: those are only Baseline "newly
    // available" (2024), below this bundle's browser floor, and build.target
    // transpiles syntax without polyfilling runtime APIs. They are also
    // undrivable by fake timers, which would leave this deadline untested.
    const deadline = new AbortController();
    const timer = setTimeout(() => deadline.abort(), CAPABILITIES_TIMEOUT_MS);
    if (signal?.aborted) deadline.abort();
    else signal?.addEventListener('abort', () => deadline.abort(), { once: true });

    try {
      const caps = await fetchCapabilities(deadline.signal);
      if (signal?.aborted) return;
      setCapabilities(caps);
    } catch {
      // Unknown: leave `capabilities` null, which the gates below treat as
      // all-false. Never surfaced as an error -- an unreachable
      // optional-feature document must not blank the dashboard.
    } finally {
      clearTimeout(timer);
      if (!signal?.aborted) setCapsSettled(true);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    // loadCapabilities only setStates after an await, but the rule's analysis
    // is inter-procedural and not await-aware, so it flags the call itself.
    // Fetching a document the first render needs is exactly the external-system
    // synchronisation an effect is for, and it runs once. Same suppression,
    // same reason, as the loadData effect below. (The directive must be the
    // last comment line: eslint-disable-next-line applies to the line directly
    // after it, so trailing prose lines would silently defuse it.)
    // eslint-disable-next-line react-hooks/set-state-in-effect -- see above
    loadCapabilities(controller.signal);
    return () => controller.abort();
  }, [loadCapabilities]);

  // Tracks whichever request set (full load or a poll tick) is currently in
  // flight, so starting a new one cancels the old -- fixes the race where a
  // slow earlier request's response could land after, and clobber, a faster
  // later one (UI B-MEDIUM).
  const abortRef = useRef<AbortController | null>(null);

  // Tracks which controller was created by the most recent loadData call
  // specifically (unlike abortRef, pollCurrent never writes to this one).
  // Used below to tell "superseded by a poll tick" (which doesn't manage
  // isLoading, so this run must still clear it) apart from "superseded by a
  // newer loadData/refresh call" (which owns isLoading now, so this run must
  // NOT clear it out from under it).
  const loadOwnerRef = useRef<AbortController | null>(null);

  const loadData = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    loadOwnerRef.current = controller;
    const { signal } = controller;

    setIsLoading(true);

    const [stationResult, obsResult, forecastResult, statusResult, almanacResult] =
      await Promise.allSettled([
        fetchStationMeta(stationId, signal),
        fetchCurrentObservation(stationId, signal),
        forecastEnabled ? fetchForecast(stationId, signal) : Promise.resolve(NO_FORECAST),
        fetchStationStatus(stationId, signal),
        almanacEnabled ? fetchStationAlmanac(stationId, signal) : Promise.resolve(null),
      ]);

    // Clear the loading flag only if no newer loadData/refresh call has
    // superseded this one. A poll tick aborting a still-in-flight initial
    // load does NOT touch loadOwnerRef (only loadData does), so that case
    // still clears isLoading as before; a newer loadData call does, so this
    // (now-superseded) run leaves isLoading alone for the newer run to clear.
    if (loadOwnerRef.current === controller) {
      setIsLoading(false);
    }

    // This run was superseded by a newer loadData/poll call (which aborted
    // it) -- its DATA results are stale, so drop them instead of overwriting
    // state the newer call already wrote.
    if (signal.aborted) return;

    applySettled(stationResult, setStation);
    applySettled(forecastResult, setForecast);
    applySettled(statusResult, setStatus);
    applySettled(almanacResult, setAlmanac);

    // isStale/error track only the core observation fetch -- the one
    // endpoint that actually works with no server TOKEN configured. The
    // WeatherFlow-backed slices (station/forecast/almanac) are documented
    // best-effort (design §11) and degrade silently: applySettled above
    // already left their prior value in place on failure.
    if (obsResult.status === 'fulfilled') {
      setCurrent(obsResult.value);
      setIsStale(false);
      setError(null);
      setLastUpdated(new Date());
    } else {
      setIsStale(true);
      setError(describeError(obsResult));
    }
  }, [stationId, forecastEnabled, almanacEnabled]);

  // Lightweight poll: refetches only the core observation, not the
  // WeatherFlow-backed slices -- there is no reason to hammer a best-effort
  // proxy on a timer. Shares abortRef with loadData so only one request set
  // is ever in flight.
  const pollCurrent = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const { signal } = controller;

    try {
      const obs = await fetchCurrentObservation(stationId, signal);
      if (signal.aborted) return;
      // Reference-stability guard (§14 P2.13): an unchanged observation
      // (same timestamp -- unique per reading, UNIQUE(serial,timestamp))
      // keeps the same object reference so React.memo on the current-
      // consuming cards can skip re-rendering on ticks with no new data.
      setCurrent((prev) => (prev && prev.timestamp === obs.timestamp ? prev : obs));
      setIsStale(false);
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      if (signal.aborted) return; // superseded/unmounted, not a real failure
      setIsStale(true);
      setError(err instanceof Error ? err.message : 'Failed to load weather data');
    }
  }, [stationId]);

  // loadData's synchronous prologue is setIsLoading(true) -- the canonical
  // data-fetching shape React's own "You Might Not Need an Effect" docs use.
  // The rule exists to stop cascading renders; this fires once per stationId
  // change, not per render. Both ways out are worse than the suppression:
  // dropping the loading indicator, or duplicating isLoading as render-phase
  // adjusted state purely to satisfy a linter. Revisit if this hook ever moves
  // to a data-fetching library, which is React's actual recommendation here.
  useEffect(() => {
    // Wait for capabilities to SETTLE (resolved or failed), not to succeed.
    // Starting the load first and letting a late capability flip change
    // loadData's identity would abort the in-flight requests and restart
    // them, either delaying first paint or bouncing the rendered dashboard
    // back to the loading screen.
    if (!capsSettled) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- see above
    loadData();
    return () => abortRef.current?.abort();
  }, [capsSettled, loadData]);

  useEffect(() => {
    const id = setInterval(() => {
      pollCurrent();
      // Retry only while unknown. A held document -- including an all-false
      // one, which is an object and not null -- is never refetched. Without
      // this, one transient failure hides all three cards for the lifetime of
      // a page, and this appliance's tab may stay open for weeks.
      if (capabilities === null) loadCapabilities();
    }, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [pollCurrent, capabilities, loadCapabilities]);

  // Records summary is a separate, slow-moving slice (a 7-365 day
  // aggregate read from the local store, keyed on window not stationId) --
  // it has its own triggers, not the 30s poll, so it gets its own controller
  // and effect rather than sharing abortRef/loadData.
  //
  // Two triggers: the window pref changing, and refresh() bumping
  // summaryNonce. It stays off the 30s poll deliberately -- a multi-day
  // aggregate does not move on a 30s cadence, and re-running this scan every
  // tick would put load on the store for nothing.
  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;

    (async () => {
      try {
        const result = await fetchRecordsSummary(recordsWindowDays, signal);
        if (signal.aborted) return;
        setSummary(result);
      } catch {
        // Stale-retain (matches applySettled's philosophy for the other
        // best-effort slices): on abort or a transient failure, keep
        // whatever summary is already in state rather than clobbering it.
      }
    })();

    return () => controller.abort();
  }, [recordsWindowDays, summaryNonce]);

  // Accepted wrinkle: if capabilities were unknown, the user presses Retry, and
  // the capability re-attempt then SUCCEEDS, the enabled-flags flip changes
  // loadData's identity and the effect fires a second load that aborts this one.
  // One wasted round trip on a path the user explicitly asked to retry; removing
  // it needs state this hook does not otherwise want.
  const refresh = useCallback(() => {
    if (capabilities === null) loadCapabilities();
    loadData();
    // Re-runs the records-summary effect. Without this the Records card is the
    // one slice the Refresh button does not reach, so it holds page-load
    // extremes -- and coveredTo -- for the lifetime of the tab (issue #89).
    setSummaryNonce((n) => n + 1);
  }, [capabilities, loadCapabilities, loadData]);

  return {
    station,
    current,
    forecast,
    status,
    almanac,
    summary,
    isLoading,
    error,
    lastUpdated,
    isStale,
    capabilities,
    refresh,
  };
}
