import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useWeatherData, POLL_INTERVAL_MS, CAPABILITIES_TIMEOUT_MS } from './useWeatherData';
import * as api from '../api/tempestApi';
import { PrecipitationType, PressureTrend } from '../types/weather';
import type {
  CurrentObservation,
  StationMeta,
  StationStatus,
  StationAlmanac,
  RecordsSummary,
  RecordsWindowDays,
  Capabilities,
} from '../types/weather';

vi.mock('../api/tempestApi', () => ({
  fetchCurrentObservation: vi.fn(),
  fetchStationMeta: vi.fn(),
  fetchForecast: vi.fn(),
  fetchStationStatus: vi.fn(),
  fetchStationAlmanac: vi.fn(),
  fetchRecordsSummary: vi.fn(),
  fetchCapabilities: vi.fn(),
}));

const baseObs: CurrentObservation = {
  timestamp: 1700000000,
  windLull: 1.1,
  windAvg: 2.2,
  windGust: 3.3,
  windDirection: 180,
  windSampleInterval: 3,
  stationPressure: 1013.2,
  airTemperature: 15.5,
  relativeHumidity: 60,
  illuminance: 5000,
  uvIndex: 2,
  solarRadiation: 100,
  rainAccumulated: 0,
  precipitationType: PrecipitationType.None,
  lightningStrikeAvgDistance: 0,
  lightningStrikeCount: 0,
  battery: 2.6,
  reportInterval: 1,
  localDayRainAccumulation: 0,
  feelsLike: 15.5,
  dewPoint: 8,
  wetBulbTemperature: 11,
  heatIndex: 15.5,
  windChill: 15.5,
  pressureTrend: PressureTrend.Steady,
};

const baseStation: StationMeta = {
  name: 'Test Station',
  latitude: 0,
  longitude: 0,
  elevation: 0,
};

const baseStatus: StationStatus = {
  isOnline: true,
  lastReport: 1700000000,
  batteryLevel: 2.6,
  signalStrength: 0,
  firmwareVersion: '',
};

const baseAlmanac: StationAlmanac = {
  today: { high: 10, highDate: 'Today', low: 5, lowDate: 'Today' },
  week: { high: 10, highDate: 'Today', low: 5, lowDate: 'Today' },
  month: { high: 10, highDate: 'Today', low: 5, lowDate: 'Today' },
  year: { high: 10, highDate: 'Today', low: 5, lowDate: 'Today' },
  sunrise: '6:00 AM',
  sunset: '6:00 PM',
  daylightMinutes: 720,
  moonPhase: 0.5,
  moonPhaseName: 'Full Moon',
  moonIllumination: 1,
};

const baseSummary: RecordsSummary = {
  window: { days: 7, from: 1699999000, to: 1700000000 },
  count: 100,
  coveredFrom: 1699999000,
  coveredTo: 1700000000,
  temperature: { max: 20, min: 5 },
  humidity: { max: 90, min: 30 },
  pressure: { max: 1020, min: 1000 },
  windMax: 10,
  gustMax: 15,
  rainTotal: 5,
  lightningTotal: 2,
};

const mockedApi = vi.mocked(api);

beforeEach(() => {
  vi.resetAllMocks();
  // Default to everything enabled. Without this, resetAllMocks leaves
  // fetchCapabilities resolving undefined, the hook fails closed, and every
  // existing test silently stops exercising the forecast and almanac
  // fetches while still passing.
  mockedApi.fetchCapabilities.mockResolvedValue({ forecast: true, radar: true, almanac: true });
});

describe('useWeatherData', () => {
  it('retains prior data and flips isStale when a refetch of the core observation fails', async () => {
    mockedApi.fetchCurrentObservation
      .mockResolvedValueOnce(baseObs)
      .mockRejectedValueOnce(new Error('network down'));
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    await waitFor(() => expect(result.current.current).toEqual(baseObs));
    expect(result.current.isStale).toBe(false);

    result.current.refresh();

    await waitFor(() => expect(result.current.isStale).toBe(true));
    expect(result.current.current).toEqual(baseObs);
    expect(mockedApi.fetchCurrentObservation).toHaveBeenCalledTimes(2);
  });

  it('keeps current populated when only the WeatherFlow-backed fetches fail (allSettled degradation)', async () => {
    mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
    mockedApi.fetchStationMeta.mockRejectedValue(new Error('weatherflow down'));
    mockedApi.fetchForecast.mockRejectedValue(new Error('weatherflow down'));
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockRejectedValue(new Error('weatherflow down'));
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    await waitFor(() => expect(result.current.current).toEqual(baseObs));
    expect(result.current.isStale).toBe(false);
    expect(result.current.error).toBeNull();
    expect(result.current.station).toBeNull();
  });

  it('retains the prior station status on a subsequent status-fetch failure instead of overwriting it with an offline default (M5)', async () => {
    mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus
      .mockResolvedValueOnce(baseStatus)
      .mockRejectedValueOnce(new Error('status endpoint down'));
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    await waitFor(() => expect(result.current.status).toEqual(baseStatus));

    result.current.refresh();

    await waitFor(() => expect(result.current.current).toEqual(baseObs));
    // The status fetch on this second run rejected -- the prior good status
    // must be retained (not overwritten with the offline default), and the
    // observation slice must remain populated.
    expect(result.current.status).toEqual(baseStatus);
    expect(mockedApi.fetchStationStatus).toHaveBeenCalledTimes(2);
  });
});

describe('useWeatherData - isLoading with polling', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    // Same default as the outer hook -- this resetAllMocks runs after it and
    // would otherwise clear it. An undefined document is not just "disabled":
    // it also defeats the `capabilities === null` retry predicate.
    mockedApi.fetchCapabilities.mockResolvedValue({ forecast: true, radar: true, almanac: true });
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('clears isLoading when the poll interval aborts a still-in-flight initial load', async () => {
    // The initial fetchCurrentObservation call never resolves on its own --
    // it only settles (rejects, mirroring real fetch's abort behavior) once
    // its AbortSignal fires, simulating a hung/slow request. The second
    // call (issued by the poll tick) resolves immediately.
    let obsCallCount = 0;
    mockedApi.fetchCurrentObservation.mockImplementation(
      (_stationId?: number, signal?: AbortSignal) => {
        obsCallCount += 1;
        if (obsCallCount === 1) {
          return new Promise<CurrentObservation>((_resolve, reject) => {
            signal?.addEventListener('abort', () => {
              const err = new Error('Aborted');
              err.name = 'AbortError';
              reject(err);
            });
          });
        }
        return Promise.resolve(baseObs);
      }
    );
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    expect(result.current.isLoading).toBe(true);

    // Advance past one poll tick: pollCurrent aborts the still-in-flight
    // initial loadData (its fetchCurrentObservation call rejects with
    // AbortError) and issues its own fetchCurrentObservation call, which
    // resolves.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });

    expect(result.current.current).toEqual(baseObs);
    expect(result.current.isLoading).toBe(false);
  });

  it('does not clear isLoading for a run that a newer refresh() call has already superseded', async () => {
    // First loadData's fetchCurrentObservation hangs until aborted (mirrors
    // real fetch abort behavior); the second (superseding) call also hangs
    // until manually resolved, so we can assert isLoading is still true
    // while it is in flight -- proving the aborted first run did not clear
    // the spinner out from under it.
    let obsCallCount = 0;
    let resolveSecondCall: ((obs: CurrentObservation) => void) | undefined;
    mockedApi.fetchCurrentObservation.mockImplementation(
      (_stationId?: number, signal?: AbortSignal) => {
        obsCallCount += 1;
        if (obsCallCount === 1) {
          return new Promise<CurrentObservation>((_resolve, reject) => {
            signal?.addEventListener('abort', () => {
              const err = new Error('Aborted');
              err.name = 'AbortError';
              reject(err);
            });
          });
        }
        return new Promise<CurrentObservation>((resolve) => {
          resolveSecondCall = resolve;
        });
      }
    );
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    expect(result.current.isLoading).toBe(true);

    // Trigger a second, superseding loadData run (aborts the first, in-flight
    // run) while the first run's fetchCurrentObservation is still pending.
    await act(async () => {
      result.current.refresh();
    });

    // The first run's allSettled has now resolved (its fetch rejected from
    // the abort), but the second run is still in flight -- isLoading must
    // still be true.
    expect(result.current.isLoading).toBe(true);

    // Let the second run's fetch resolve, completing it. Fake timers are
    // active in this describe block, so testing-library's `waitFor` (which
    // polls via setTimeout) would hang -- flush microtasks directly instead,
    // matching the pattern the sibling test above uses.
    await act(async () => {
      resolveSecondCall?.(baseObs);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.isLoading).toBe(false);
    expect(result.current.current).toEqual(baseObs);
  });

  it('keeps the current object reference stable across a poll that returns a new object with the same timestamp (§14 P2.13)', async () => {
    // Two distinct object instances with an identical timestamp -- mirrors a
    // poll tick that refetches the same underlying reading. The guard in
    // pollCurrent's setCurrent should retain the prior reference instead of
    // swapping in the new (but equivalent) object, so memoized current-
    // consuming cards don't re-render for a no-op tick.
    const firstObs: CurrentObservation = { ...baseObs };
    const secondObs: CurrentObservation = { ...baseObs };
    mockedApi.fetchCurrentObservation
      .mockResolvedValueOnce(firstObs)
      .mockResolvedValueOnce(secondObs);
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    // Flush the initial (non-poll) load's microtasks. Fake timers are active
    // in this describe block, so `waitFor` would hang; flush directly instead
    // (matching the sibling test above).
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.current).toBe(firstObs);
    const refAfterInitialLoad = result.current.current;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });

    expect(mockedApi.fetchCurrentObservation).toHaveBeenCalledTimes(2);
    expect(result.current.current).toBe(refAfterInitialLoad);
    expect(result.current.current).not.toBe(secondObs);
  });

  it('opens the settle-gate on the capabilities deadline when the fetch hangs', async () => {
    // fetchCapabilities never settles on its own -- it only rejects once its
    // AbortSignal fires, mirroring real fetch's abort behavior. Nothing but
    // CAPABILITIES_TIMEOUT_MS can release it, so the core dashboard load is
    // held until the deadline elapses, and then proceeds with capabilities
    // still unknown (fail-closed).
    mockedApi.fetchCapabilities.mockImplementation(
      (signal?: AbortSignal) =>
        new Promise<Capabilities>((_resolve, reject) => {
          signal?.addEventListener('abort', () => {
            const err = new Error('Aborted');
            err.name = 'AbortError';
            reject(err);
          });
        })
    );
    mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    // Before the deadline the gate is shut: nothing has loaded yet.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(CAPABILITIES_TIMEOUT_MS - 1);
    });
    expect(mockedApi.fetchCurrentObservation).not.toHaveBeenCalled();
    expect(result.current.isLoading).toBe(true);

    // Crossing the deadline aborts the hung fetch, which settles the gate.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });

    expect(result.current.current).toEqual(baseObs);
    expect(result.current.capabilities).toBeNull();
    expect(result.current.error).toBeNull();
    expect(mockedApi.fetchForecast).not.toHaveBeenCalled();
  });

  it('retries fetchCapabilities on a poll tick after a failure, then stops once held', async () => {
    mockedApi.fetchCapabilities
      .mockRejectedValueOnce(new Error('capabilities down'))
      .mockResolvedValue({ forecast: false, radar: false, almanac: false });
    mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

    const { result } = renderHook(() => useWeatherData());

    // Fake timers are active in this describe block, so testing-library's
    // `waitFor` would hang -- its async wrapper drains through a real
    // setTimeout it only advances when the *jest* global is present, which
    // vitest does not provide -- so flush microtasks directly instead,
    // matching the sibling tests above.
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.capabilities).toBeNull();
    expect(mockedApi.fetchCapabilities).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
    });

    expect(result.current.capabilities).not.toBeNull();
    expect(mockedApi.fetchCapabilities).toHaveBeenCalledTimes(2);
    // Guards the `=== true` comparisons behind forecastEnabled/almanacEnabled:
    // this retry took capabilities from unknown to all-false, which must leave
    // loadData's identity untouched. A truthiness check instead would flip
    // undefined -> false, re-run the effect, and fire a second full load.
    expect(mockedApi.fetchStationMeta).toHaveBeenCalledTimes(1);

    // A held document -- including an all-false one -- is never refetched.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS * 3);
    });
    expect(mockedApi.fetchCapabilities).toHaveBeenCalledTimes(2);
  });
});

describe('useWeatherData - records summary', () => {
  const setupCoreMocks = () => {
    mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
    mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
    mockedApi.fetchForecast.mockResolvedValue([]);
    mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
    mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
  };

  it('fetches the summary for the current window, exposes it, and re-fetches when the window pref changes', async () => {
    setupCoreMocks();
    const summary7: RecordsSummary = { ...baseSummary, window: { ...baseSummary.window, days: 7 } };
    const summary30: RecordsSummary = { ...baseSummary, window: { ...baseSummary.window, days: 30 } };
    mockedApi.fetchRecordsSummary
      .mockResolvedValueOnce(summary7)
      .mockResolvedValueOnce(summary30);

    const { result, rerender } = renderHook(
      ({ days }: { days: RecordsWindowDays }) => useWeatherData(undefined, days),
      { initialProps: { days: 7 } }
    );

    await waitFor(() => expect(result.current.summary).toEqual(summary7));
    expect(mockedApi.fetchRecordsSummary).toHaveBeenNthCalledWith(1, 7, expect.any(AbortSignal));

    rerender({ days: 30 });

    await waitFor(() => expect(result.current.summary).toEqual(summary30));
    expect(mockedApi.fetchRecordsSummary).toHaveBeenNthCalledWith(2, 30, expect.any(AbortSignal));
  });

  it('re-fetches the summary when refresh() is called', async () => {
    setupCoreMocks();
    const before: RecordsSummary = { ...baseSummary, rainTotal: 5 };
    const after: RecordsSummary = { ...baseSummary, rainTotal: 9 };
    mockedApi.fetchRecordsSummary.mockResolvedValueOnce(before).mockResolvedValueOnce(after);

    const { result } = renderHook(() => useWeatherData());

    await waitFor(() => expect(result.current.summary).toEqual(before));
    expect(mockedApi.fetchRecordsSummary).toHaveBeenCalledTimes(1);

    result.current.refresh();

    // The manual Refresh button must cover the Records card too. Without this
    // the card silently holds page-load extremes -- a storm's rain total and
    // lightning count never move, and coveredTo freezes -- while every live
    // card around it updates.
    await waitFor(() => expect(result.current.summary).toEqual(after));
    expect(mockedApi.fetchRecordsSummary).toHaveBeenCalledTimes(2);
    expect(mockedApi.fetchRecordsSummary).toHaveBeenNthCalledWith(2, 7, expect.any(AbortSignal));
  });

  it('retains the prior summary when a refetch after a window change fails (stale-retain)', async () => {
    setupCoreMocks();
    mockedApi.fetchRecordsSummary
      .mockResolvedValueOnce(baseSummary)
      .mockRejectedValueOnce(new Error('summary endpoint down'));

    const { result, rerender } = renderHook(
      ({ days }: { days: RecordsWindowDays }) => useWeatherData(undefined, days),
      { initialProps: { days: 7 } }
    );

    await waitFor(() => expect(result.current.summary).toEqual(baseSummary));

    rerender({ days: 30 });

    await waitFor(() => expect(mockedApi.fetchRecordsSummary).toHaveBeenCalledTimes(2));
    expect(result.current.summary).toEqual(baseSummary);
  });
});

it('skips the disabled fetches and still loads the core observation', async () => {
  mockedApi.fetchCapabilities.mockResolvedValue({ forecast: false, radar: false, almanac: false });
  mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
  mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
  mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
  mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

  const { result } = renderHook(() => useWeatherData());

  await waitFor(() => expect(result.current.current).toEqual(baseObs));

  expect(mockedApi.fetchForecast).not.toHaveBeenCalled();
  expect(mockedApi.fetchStationAlmanac).not.toHaveBeenCalled();
  expect(result.current.forecast).toEqual([]);
  expect(result.current.almanac).toBeNull();
  expect(result.current.capabilities).toEqual({ forecast: false, radar: false, almanac: false });
});

it('fetches the optional slices when they are enabled', async () => {
  mockedApi.fetchCapabilities.mockResolvedValue({ forecast: true, radar: false, almanac: true });
  mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
  mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
  mockedApi.fetchForecast.mockResolvedValue([]);
  mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
  mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
  mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

  const { result } = renderHook(() => useWeatherData());

  await waitFor(() => expect(result.current.almanac).toEqual(baseAlmanac));
  expect(mockedApi.fetchForecast).toHaveBeenCalled();
  expect(mockedApi.fetchStationAlmanac).toHaveBeenCalled();
});

it('fails closed and skips the optional fetches when capabilities are unreachable', async () => {
  mockedApi.fetchCapabilities.mockRejectedValue(new Error('capabilities down'));
  mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
  mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
  mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
  mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

  const { result } = renderHook(() => useWeatherData());

  await waitFor(() => expect(result.current.current).toEqual(baseObs));

  expect(result.current.capabilities).toBeNull();
  expect(mockedApi.fetchForecast).not.toHaveBeenCalled();
  expect(result.current.error).toBeNull();
});

it('loads the core observation exactly once when capabilities are enabled', async () => {
  mockedApi.fetchCapabilities.mockResolvedValue({ forecast: true, radar: true, almanac: true });
  mockedApi.fetchCurrentObservation.mockResolvedValue(baseObs);
  mockedApi.fetchStationMeta.mockResolvedValue(baseStation);
  mockedApi.fetchForecast.mockResolvedValue([]);
  mockedApi.fetchStationStatus.mockResolvedValue(baseStatus);
  mockedApi.fetchStationAlmanac.mockResolvedValue(baseAlmanac);
  mockedApi.fetchRecordsSummary.mockResolvedValue(baseSummary);

  const { result } = renderHook(() => useWeatherData());

  await waitFor(() => expect(result.current.current).toEqual(baseObs));
  // Holding the first load until capabilities settle is what prevents the
  // abort-and-restart a late capability flip would otherwise cause.
  expect(mockedApi.fetchStationMeta).toHaveBeenCalledTimes(1);
});
