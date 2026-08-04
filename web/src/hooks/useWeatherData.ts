import { useState, useEffect, useCallback, useRef } from 'react';
import type {
  CurrentObservation,
  StationMeta,
  ForecastDay,
  StationStatus,
  StationAlmanac,
  RecordsSummary,
  RecordsWindowDays,
} from '../types/weather';
import {
  fetchCurrentObservation,
  fetchStationMeta,
  fetchForecast,
  fetchStationStatus,
  fetchStationAlmanac,
  fetchRecordsSummary,
} from '../api/tempestApi';

// There is no WebSocket backend (Contract C is plain JSON, see design §11),
// so live-ness comes from polling the core observation instead. 30s keeps
// the UI reasonably fresh against the station's own ~1-minute report
// cadence without hammering the read path; not derived from anything
// authoritative, just a reasonable middle ground.
export const POLL_INTERVAL_MS = 30_000;

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
        fetchForecast(stationId, signal),
        fetchStationStatus(stationId, signal),
        fetchStationAlmanac(stationId, signal),
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
  }, [stationId]);

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
    // eslint-disable-next-line react-hooks/set-state-in-effect -- see above
    loadData();
    return () => abortRef.current?.abort();
  }, [loadData]);

  useEffect(() => {
    const id = setInterval(pollCurrent, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [pollCurrent]);

  // Records summary is a separate, slow-moving slice (a 7-365 day
  // aggregate read from the local store, keyed on window not stationId) --
  // it has its own trigger (the window pref changing), not the 30s poll or
  // loadData's stationId-keyed refresh, so it gets its own controller and
  // effect rather than sharing abortRef/loadData.
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
  }, [recordsWindowDays]);

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
    refresh: loadData,
  };
}
