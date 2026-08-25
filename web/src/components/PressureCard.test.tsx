import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { PressureCard } from './PressureCard';
import { PrecipitationType, PressureTrend, type CurrentObservation } from '../types/weather';

const baseCurrent: CurrentObservation = {
  timestamp: 1700000000,
  windLull: 1,
  windAvg: 2,
  windGust: 3,
  windDirection: 180,
  windSampleInterval: 3,
  stationPressure: 1013,
  airTemperature: 20,
  relativeHumidity: 55,
  illuminance: 1000,
  uvIndex: 2,
  solarRadiation: 100,
  rainAccumulated: 0,
  precipitationType: PrecipitationType.None,
  lightningStrikeAvgDistance: 0,
  lightningStrikeCount: 0,
  battery: 2.6,
  reportInterval: 1,
  localDayRainAccumulation: 0,
  feelsLike: 20,
  dewPoint: 12,
  wetBulbTemperature: 15,
  heatIndex: 20,
  windChill: 20,
  pressureTrend: PressureTrend.Steady,
};

// The card gained a window range row because the taller humidity ring stretched
// its grid row: the card had a header, a readout and a gauge in 345px of space
// and 76px of it was empty. The range is real data the dashboard already
// fetches (GET /api/observations/summary), not a spacer.
describe('PressureCard window range', () => {
  it('renders the low and high for the selected window, labelled with its length', () => {
    const { container } = render(
      <PressureCard
        current={baseCurrent}
        unit="mb"
        range={{ min: 999.74, max: 1000.93 }}
        windowDays={7}
      />
    );
    const text = container.textContent ?? '';
    expect(text).toContain('7-Day Low');
    expect(text).toContain('999.7 mb');
    expect(text).toContain('7-Day High');
    expect(text).toContain('1000.9 mb');
  });

  it('labels the row with whatever window is selected, not a hardcoded 7', () => {
    const { container } = render(
      <PressureCard
        current={baseCurrent}
        unit="mb"
        range={{ min: 990, max: 1030 }}
        windowDays={365}
      />
    );
    expect(container.textContent).toContain('365-Day Low');
    expect(container.textContent).toContain('365-Day High');
  });

  it('converts the range into the selected unit, like the main readout', () => {
    const { container } = render(
      <PressureCard
        current={baseCurrent}
        unit="inHg"
        range={{ min: 999.74, max: 1000.93 }}
        windowDays={30}
      />
    );
    expect(container.textContent).toContain('29.52 inHg');
    expect(container.textContent).toContain('29.56 inHg');
  });

  // Same rule as StationHealth's signal: absent data renders as an em-dash
  // rather than as a number, because 0 mb is not a reading anyone should read.
  it('renders em-dashes when no summary has arrived', () => {
    const { container } = render(
      <PressureCard current={baseCurrent} unit="mb" range={null} windowDays={7} />
    );
    const values = Array.from(container.querySelectorAll('.stat-value')).map((e) => e.textContent);
    expect(values).toEqual(['—', '—']);
  });

  it('renders an em-dash for a bound the window has no reading for', () => {
    const { container } = render(
      <PressureCard current={baseCurrent} unit="mb" range={{ min: null, max: 1000.93 }} windowDays={7} />
    );
    const values = Array.from(container.querySelectorAll('.stat-value')).map((e) => e.textContent);
    expect(values).toEqual(['—', '1000.9 mb']);
  });

  it('still renders the live reading and its trend', () => {
    const { container } = render(
      <PressureCard current={baseCurrent} unit="mb" range={null} windowDays={7} />
    );
    expect(container.textContent).toContain('1013.0 mb');
    expect(container.textContent).toContain('Steady');
  });
});
