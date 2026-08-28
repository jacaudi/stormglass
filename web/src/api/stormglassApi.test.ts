import { describe, it, expect, vi, afterEach } from 'vitest';
import { fetchCurrentObservation, fetchStationStatus, fetchRecordsSummary, fetchCapabilities } from './stormglassApi';
import stormglassApiSource from './stormglassApi.ts?raw';
import { PrecipitationType, PressureTrend } from '../types/weather';
import type { CurrentObservation, RecordsSummary } from '../types/weather';

const mockObservation: CurrentObservation = {
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
  signalDbm: null,
  firmwareVersion: null,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('fetchCurrentObservation', () => {
  it('GETs the tokenless relative Contract C endpoint and returns a typed CurrentObservation', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockObservation),
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = await fetchCurrentObservation();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/observations/current');
    expect(init?.signal).toBeUndefined();
    expect(result).toEqual(mockObservation);
  });

  it('forwards an AbortSignal to fetch', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockObservation),
    });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();

    await fetchCurrentObservation(undefined, controller.signal);

    expect(fetchMock.mock.calls[0][1]?.signal).toBe(controller.signal);
  });

  it('throws on a non-OK response instead of returning stub/garbage data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503 }));

    await expect(fetchCurrentObservation()).rejects.toThrow();
  });
});

describe('fetchStationStatus', () => {
  it('rejects instead of returning an offline default when the underlying fetch fails', async () => {
    // A transient server error must surface as a rejection so useWeatherData's
    // allSettled retains the prior good status, rather than fetchStationStatus
    // swallowing the error into a fake {isOnline:false, lastReport:0, ...}
    // default that would overwrite good prior state (M5).
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503 }));

    await expect(fetchStationStatus()).rejects.toThrow();
  });

  it('derives an online StationStatus from a successful current-observation fetch', async () => {
    // mockObservation's fixed timestamp is years stale, which would derive
    // isOnline=false regardless of this fix -- use a fresh, "just reported"
    // observation instead so this test actually exercises the online branch.
    const freshObservation = { ...mockObservation, timestamp: Date.now() / 1000 };
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(freshObservation),
      })
    );

    const result = await fetchStationStatus();

    expect(result.isOnline).toBe(true);
    expect(result.lastReport).toBe(freshObservation.timestamp);
    expect(result.batteryLevel).toBe(mockObservation.battery);
  });
});

describe('fetchRecordsSummary', () => {
  const mockSummary: RecordsSummary = {
    window: { days: 7, from: 1699999000, to: 1700000000 },
    count: 42,
    coveredFrom: 1699999100,
    coveredTo: 1700000000,
    temperature: { max: 22.1, min: 8.4 },
    humidity: { max: 95, min: 30 },
    pressure: { max: 1020.5, min: 1005.2 },
    windMax: 12.3,
    gustMax: 18.9,
    rainTotal: 4.2,
    lightningTotal: 0,
  };

  it('GETs the days-windowed Contract C endpoint and returns a typed RecordsSummary', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockSummary),
    });
    vi.stubGlobal('fetch', fetchMock);

    const result = await fetchRecordsSummary(7);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/observations/summary?days=7');
    expect(init?.signal).toBeUndefined();
    expect(result).toEqual(mockSummary);
  });

  it('forwards an AbortSignal to fetch', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve(mockSummary),
    });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();

    await fetchRecordsSummary(7, controller.signal);

    expect(fetchMock.mock.calls[0][1]?.signal).toBe(controller.signal);
  });

  it('throws on a non-OK response instead of returning stub/garbage data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503 }));

    await expect(fetchRecordsSummary(7)).rejects.toThrow();
  });
});

describe('module hygiene', () => {
  it('does not import the deleted stub data module', () => {
    expect(stormglassApiSource).not.toMatch(/stubData/);
  });
});

describe('fetchCapabilities', () => {
  it('returns the capability document on 200', async () => {
    const body = { forecast: true, radar: false, almanac: true };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => body,
    }));

    await expect(fetchCapabilities()).resolves.toEqual(body);
    expect(fetch).toHaveBeenCalledWith('/api/capabilities', { signal: undefined });
  });

  it('rejects on a non-OK status so the caller can fail closed', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => ({}),
    }));

    await expect(fetchCapabilities()).rejects.toThrow('/api/capabilities responded with 500');
  });

  it('propagates a network error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));

    await expect(fetchCapabilities()).rejects.toThrow('offline');
  });
});
