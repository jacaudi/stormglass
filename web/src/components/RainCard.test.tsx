import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { RainCard } from './RainCard';
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
  rainAccumulated: 0.5,
  precipitationType: PrecipitationType.Rain,
  lightningStrikeAvgDistance: 0,
  lightningStrikeCount: 0,
  battery: 2.6,
  reportInterval: 1,
  localDayRainAccumulation: 1.2,
  feelsLike: 20,
  dewPoint: 12,
  wetBulbTemperature: 15,
  heatIndex: 20,
  windChill: 20,
  pressureTrend: PressureTrend.Steady,
  signalDbm: null,
  firmwareVersion: null,
};

describe('RainCard raindrop stability (P2.13)', () => {
  it('keeps raindrop positions stable across a re-render with new (still-raining) props', () => {
    const { container, rerender } = render(<RainCard current={baseCurrent} unit="in" windowTotal={null} windowDays={7} />);

    const before = Array.from(container.querySelectorAll<HTMLElement>('.raindrop')).map(
      (el) => el.style.left
    );
    expect(before).toHaveLength(12);

    rerender(
      <RainCard
        current={{ ...baseCurrent, localDayRainAccumulation: 2.4 }}
        unit="in"
        windowTotal={null}
        windowDays={7}
      />
    );

    const after = Array.from(container.querySelectorAll<HTMLElement>('.raindrop')).map(
      (el) => el.style.left
    );

    expect(after).toEqual(before);
  });
});

const raincard = (c: HTMLElement) => c.querySelector('.glass-card')!.children;

describe('RainCard interior centring precondition (design §6.4)', () => {
  it('renders the rain animation as a conditional LAST child while raining', () => {
    // The interior rule in App.css excludes .rain-animation by class because it
    // is an out-of-flow third child that exists only in this state. If the
    // component ever stops rendering it last, or renders it unconditionally,
    // the exclusion silently stops matching what it was written for.
    const raining = { ...baseCurrent, precipitationType: PrecipitationType.Rain };
    const { container } = render(<RainCard current={raining} unit="in" windowTotal={null} windowDays={7} />);
    const card = container.querySelector('.glass-card')!;
    expect(card.lastElementChild).toHaveClass('rain-animation');

    const dry = { ...baseCurrent, precipitationType: PrecipitationType.None };
    const { container: dryContainer } = render(<RainCard current={dry} unit="in" windowTotal={null} windowDays={7} />);
    const dryCard = dryContainer.querySelector('.glass-card')!;
    expect(dryCard.querySelector('.rain-animation')).toBeNull();

    // What the exclusion actually needs is that the last IN-FLOW child is the
    // one carrying the interior's margin-bottom: auto. That is the window-total
    // row now rather than .rain-grid -- asserting the class here pinned the
    // card's content instead of the property, so assert the property.
    const inFlow = Array.from(raincard(dryContainer)).filter(
      (el) => !el.classList.contains('rain-animation')
    );
    expect(dryCard.lastElementChild).toBe(inFlow[inFlow.length - 1]);
    expect(inFlow[inFlow.length - 1]).toHaveClass('stat-row');
  });

  it('renders the rain grid as a StatRow that keeps its probe hook', () => {
    // .rain-grid is what web/test/layout/probe.mjs reads to assert the card
    // centres in both states, so the class has to survive the conversion.
    const { container } = render(<RainCard current={baseCurrent} unit="in" windowTotal={null} windowDays={7} />);
    const grid = container.querySelector('.rain-grid')!;
    expect(grid).not.toBeNull();
    expect(grid).toHaveClass('stat-row');
    expect(grid.querySelectorAll('.stat')).toHaveLength(2);
  });
});

// Same reason as PressureCard's range row: the card had 28px of empty space
// under its two stats. rainTotal is real data the dashboard already fetches.
describe('RainCard window total', () => {
  const dry = { ...baseCurrent, precipitationType: PrecipitationType.None };

  it('renders the window total, labelled with the window length', () => {
    const { container } = render(
      <RainCard current={dry} unit="in" windowTotal={12.7} windowDays={7} />
    );
    expect(container.textContent).toContain('7-Day Total');
    expect(container.textContent).toContain('0.50 in');
  });

  it('labels the row with whatever window is selected', () => {
    const { container } = render(
      <RainCard current={dry} unit="mm" windowTotal={12.7} windowDays={180} />
    );
    expect(container.textContent).toContain('180-Day Total');
    expect(container.textContent).toContain('12.7 mm');
  });

  it('renders an em-dash when no summary has arrived', () => {
    const { container } = render(
      <RainCard current={dry} unit="in" windowTotal={null} windowDays={7} />
    );
    const values = Array.from(container.querySelectorAll('.stat-value')).map((e) => e.textContent);
    expect(values[values.length - 1]).toBe('—');
  });

  it('keeps a real zero total as a reading, not an em-dash', () => {
    const { container } = render(
      <RainCard current={dry} unit="in" windowTotal={0} windowDays={7} />
    );
    const values = Array.from(container.querySelectorAll('.stat-value')).map((e) => e.textContent);
    expect(values[values.length - 1]).toBe('0.00 in');
  });
});
