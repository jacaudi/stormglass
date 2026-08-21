import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import App from './App';
import type { WeatherData } from './hooks/useWeatherData';
import type { CurrentObservation, ForecastDay, RecordsSummary, StationAlmanac, StationMeta } from './types/weather';
import { PrecipitationType, PressureTrend } from './types/weather';

// --- MapLibre GL mock -------------------------------------------------
// jsdom has no WebGL, so the real maplibre-gl Map would throw on
// construction. We replace the whole module with lightweight fakes that
// record calls so behavior (protocol registration, layer styling,
// attribution, pan calls) can be asserted without a real GL context.
// vi.mock factories are hoisted above all other module code, so anything
// they reference must be created via vi.hoisted rather than a plain
// top-level const/class.
//
// Copied from RadarCard.test.tsx: these two tests are the first App tests
// that actually mount RadarCard with a `site` (every prior radar test
// asserts the card is absent), and the real maplibre-gl module throws in
// jsdom on unmount even though RadarCard's construction try/catch survives.
// Only MockMap/MockAttributionControl/addProtocolMock are consumed below
// (these App-level tests assert text, not map internals) -- the other
// returned fields exist so this block can be a straight copy of the
// RadarCard.test.tsx factory rather than a forked one.
const { addProtocolMock, MockMap, MockAttributionControl } =
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

const mockCurrent: CurrentObservation = {
  timestamp: 1_700_000_000,
  windLull: 0.5,
  windAvg: 1.2,
  windGust: 3.4,
  windDirection: 180,
  windSampleInterval: 3,
  stationPressure: 1013.2,
  airTemperature: 21.5,
  relativeHumidity: 55,
  illuminance: 10000,
  uvIndex: 3,
  solarRadiation: 400,
  rainAccumulated: 0,
  precipitationType: PrecipitationType.None,
  lightningStrikeAvgDistance: 0,
  lightningStrikeCount: 0,
  battery: 2.8,
  reportInterval: 1,
  localDayRainAccumulation: 0,
  feelsLike: 21.5,
  dewPoint: 12.1,
  wetBulbTemperature: 15.0,
  heatIndex: 21.5,
  windChill: 21.5,
  pressureTrend: PressureTrend.Steady,
};

const mockForecast: ForecastDay[] = [
  {
    dayNum: 21,
    monthNum: 7,
    conditions: 'Clear',
    icon: 'clear-day',
    airTempHigh: 25,
    airTempLow: 15,
    precipProbability: 0,
    precipType: 'none',
    sunrise: Math.floor(Date.now() / 1000),
    sunset: Math.floor(Date.now() / 1000) + 36000,
  },
];

const mockSummary: RecordsSummary = {
  window: { days: 7, from: 1_699_000_000, to: 1_700_000_000 },
  count: 42,
  coveredFrom: 1_699_000_000,
  coveredTo: 1_700_000_000,
  temperature: { max: 28.4, min: 10.1 },
  humidity: { max: 88, min: 30 },
  pressure: { max: 1020.5, min: 1005.2 },
  windMax: 8.3,
  gustMax: 14.7,
  rainTotal: 12.5,
  lightningTotal: 3,
};

const mockAlmanac: StationAlmanac = {
  today: { high: 25, highDate: 'Today', low: 15, lowDate: 'Today' },
  week: { high: 28, highDate: 'Mon', low: 12, lowDate: 'Tue' },
  month: { high: 30, highDate: 'Jul 1', low: 10, lowDate: 'Jul 15' },
  year: { high: 32, highDate: 'Aug 1', low: 5, lowDate: 'Jan 1' },
  sunrise: '5:47 AM',
  sunset: '8:17 PM',
  daylightMinutes: 14 * 60 + 30,
  moonPhase: 0.5,
  moonPhaseName: 'Full Moon',
  moonIllumination: 1,
};

const mockStationWithCoords: StationMeta = {
  name: 'Test Station',
  latitude: 40.7128,
  longitude: -74.006,
  elevation: 10,
};

const mockWeatherData: WeatherData = {
  station: null,
  current: mockCurrent,
  forecast: mockForecast,
  status: null,
  almanac: null,
  summary: mockSummary,
  isLoading: false,
  error: null,
  lastUpdated: new Date(),
  isStale: false,
  capabilities: { forecast: true, radar: false, almanac: false },
  refresh: vi.fn(),
};

vi.mock('./hooks/useWeatherData', () => ({
  useWeatherData: vi.fn(() => mockWeatherData),
}));

describe('App dashboard layout', () => {
  it('renders the Records card before the 7-Day Forecast', () => {
    render(<App />);

    const recordsAnchor = screen.getByText('Records');
    const forecastAnchor = screen.getByText('7-Day Forecast');

    expect(recordsAnchor).toBeInTheDocument();
    expect(forecastAnchor).toBeInTheDocument();

    // DOCUMENT_POSITION_FOLLOWING on recordsAnchor -> forecastAnchor means
    // forecastAnchor comes AFTER recordsAnchor in document order.
    const position = recordsAnchor.compareDocumentPosition(forecastAnchor);
    expect(position & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

describe('App loading screen', () => {
  afterEach(() => {
    mockWeatherData.isLoading = false;
  });

  it('keeps rendering the dashboard when a later load starts and data is already on screen', () => {
    // A capability flip (e.g. a failed capability fetch retried successfully on
    // a poll tick) changes loadData's identity and re-raises isLoading. With
    // `current` already populated, the rendered dashboard must survive it.
    mockWeatherData.isLoading = true;

    render(<App />);

    expect(screen.queryByText('Connecting to station...')).toBeNull();
    expect(screen.getByText('Records')).toBeInTheDocument();
  });

  it('shows the loading screen for an initial load with no data yet', () => {
    mockWeatherData.isLoading = true;
    mockWeatherData.current = null;

    render(<App />);

    expect(screen.getByText('Connecting to station...')).toBeInTheDocument();

    mockWeatherData.current = mockCurrent;
  });
});

describe('App optional card gating', () => {
  afterEach(() => {
    mockWeatherData.capabilities = { forecast: true, radar: false, almanac: false };
    mockWeatherData.almanac = null;
    mockWeatherData.station = null;
  });

  it('mounts no optional card when every capability is false', () => {
    mockWeatherData.capabilities = { forecast: false, radar: false, almanac: false };

    render(<App />);

    expect(screen.queryByText('7-Day Forecast')).toBeNull();
    expect(screen.queryByText('Station Almanac')).toBeNull();
    expect(screen.queryByText('Radar')).toBeNull();
  });

  it('mounts nothing optional when capabilities are unknown', () => {
    mockWeatherData.capabilities = null;

    render(<App />);

    expect(screen.queryByText('7-Day Forecast')).toBeNull();
    expect(screen.queryByText('Station Almanac')).toBeNull();
    expect(screen.queryByText('Radar')).toBeNull();
  });

  it('mounts the forecast strip only when forecast is enabled', () => {
    mockWeatherData.capabilities = { forecast: true, radar: false, almanac: false };

    render(<App />);

    expect(screen.getByText('7-Day Forecast')).toBeInTheDocument();
  });

  it('does not mount the radar card for a station with no usable coordinates', () => {
    mockWeatherData.capabilities = { forecast: false, radar: true, almanac: false };
    mockWeatherData.station = { status: { status_code: 0 } } as unknown as StationMeta;

    render(<App />);

    expect(screen.queryByText('Radar')).toBeNull();
  });

  it('does not mount the almanac card when almanac data exists but the capability is disabled', () => {
    mockWeatherData.capabilities = { forecast: false, radar: false, almanac: false };
    mockWeatherData.almanac = mockAlmanac;

    render(<App />);

    expect(screen.queryByText('Station Almanac')).toBeNull();
  });

  it('mounts the almanac card when almanac data exists and the capability is enabled', () => {
    mockWeatherData.capabilities = { forecast: false, radar: false, almanac: true };
    mockWeatherData.almanac = mockAlmanac;

    render(<App />);

    expect(screen.getByText('Station Almanac')).toBeInTheDocument();
  });

  it('does not mount the radar card for a station with valid coordinates when the radar capability is disabled', () => {
    mockWeatherData.capabilities = { forecast: false, radar: false, almanac: false };
    mockWeatherData.station = mockStationWithCoords;

    render(<App />);

    expect(screen.queryByText('Radar')).toBeNull();
  });

  it('passes the station radarSite through to the radar card', () => {
    // The not-configured message is the observable proxy for "no site
    // reached RadarCard" -- it is exactly the state an absent prop produces.
    mockWeatherData.capabilities = { forecast: false, radar: true, almanac: false };
    mockWeatherData.station = { ...mockStationWithCoords, radarSite: 'TLX' };

    render(<App />);

    expect(screen.queryByText('Radar not configured for this station.')).toBeNull();
  });

  it('leaves the radar card unconfigured when the station carries no radarSite', () => {
    mockWeatherData.capabilities = { forecast: false, radar: true, almanac: false };
    mockWeatherData.station = mockStationWithCoords;

    render(<App />);

    expect(screen.getByText('Radar not configured for this station.')).toBeInTheDocument();
  });
});
