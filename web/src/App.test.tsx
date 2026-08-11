import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import App from './App';
import type { WeatherData } from './hooks/useWeatherData';
import type { CurrentObservation, ForecastDay, RecordsSummary } from './types/weather';
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
