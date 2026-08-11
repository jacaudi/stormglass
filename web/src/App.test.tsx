import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import App from './App';
import type { WeatherData } from './hooks/useWeatherData';
import type { CurrentObservation, ForecastDay, RecordsSummary, StationAlmanac, StationMeta } from './types/weather';
import { PrecipitationType, PressureTrend } from './types/weather';

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
  sunrise: Math.floor(Date.now() / 1000),
  sunset: Math.floor(Date.now() / 1000) + 36000,
  moonPhase: 0.5,
  moonPhaseName: 'Full Moon',
  moonIllumination: 1,
};

const mockStationWithCoords: StationMeta = {
  station_id: 1,
  name: 'Test Station',
  latitude: 40.7128,
  longitude: -74.006,
  elevation: 10,
  timezone: 'America/New_York',
  firmware_revision: '1.0',
  serial_number: 'ST-00000001',
  device_id: 1,
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
});
