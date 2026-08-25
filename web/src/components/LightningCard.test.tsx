import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { LightningCard } from './LightningCard';
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

describe('LightningCard converted readouts (design §6.5)', () => {
  it('renders the dry state as two Readouts sharing one row, distance falling back to the em-dash', () => {
    const { container } = render(<LightningCard current={baseCurrent} unit="km" />);

    const row = container.querySelector('.lightning-content > .stat-row')!;
    const readouts = row.querySelectorAll('.readout');
    expect(readouts).toHaveLength(2);

    const [count, distance] = Array.from(readouts);
    expect(count.querySelector('.readout-value')!.textContent).toBe('0');
    expect(count.querySelector('.readout-value')).toHaveClass('lightning-count-value');
    expect(count.querySelector('.readout-qualifier')!.textContent).toBe('strikes today');

    expect(distance.querySelector('.readout-value')!.textContent).toBe('—');
    expect(distance.querySelector('.readout-value')).toHaveClass('lightning-distance-none');
    expect(distance.querySelector('.readout-qualifier')!.textContent).toBe('avg distance');

    // No rings, no Badge, in the dry state.
    expect(container.querySelector('.lightning-distance-rings')).toBeNull();
    expect(container.querySelector('.alert-badge-slot')).toBeNull();
  });

  it('exercises the striking state: count and a real distance value, no "none" modifier, plus rings and the Badge', () => {
    const striking: CurrentObservation = {
      ...baseCurrent,
      lightningStrikeCount: 7,
      lightningStrikeAvgDistance: 12.3,
    };
    const { container } = render(<LightningCard current={striking} unit="km" />);

    const row = container.querySelector('.lightning-content > .stat-row')!;
    const readouts = row.querySelectorAll('.readout');
    expect(readouts).toHaveLength(2);

    const [count, distance] = Array.from(readouts);
    expect(count.querySelector('.readout-value')!.textContent).toBe('7');

    // This is the branch nothing else on the branch exercises: hasStrikes true,
    // a positive average distance, and valueClassName undefined (no
    // "lightning-distance-none" modifier).
    const distanceValue = distance.querySelector('.readout-value')!;
    expect(distanceValue.textContent).toBe('12.3 km');
    expect(distanceValue).not.toHaveClass('lightning-distance-none');

    expect(container.querySelector('.lightning-distance-rings')).not.toBeNull();
    expect(container.querySelector('.alert-badge-slot')).not.toBeNull();

    // The card's direct children are unchanged by this task: header, content,
    // and the rings block now lands last -- which is why the interior
    // last-child rule targets .lightning-content when dry and
    // .lightning-distance-rings when striking (pre-existing, unmeasured
    // asymmetry -- not this task's to fix).
    const card = container.querySelector('.glass-card')!;
    expect(card.lastElementChild).toHaveClass('lightning-distance-rings');
  });
});

// The station reports km; the reader picks the unit. Added with the distance
// preference so the strike readout, the sensor-range line and the ring labels
// all move together -- a card showing "8.1 km" beside a "40 km" ring while the
// rest of the dashboard is in miles was the defect this prevents.
describe('LightningCard distance unit', () => {
  const striking = {
    ...baseCurrent,
    lightningStrikeCount: 14,
    lightningStrikeAvgDistance: 8.05,
  };

  it('renders the strike distance in miles when miles are selected', () => {
    const { container } = render(<LightningCard current={striking} unit="mi" />);
    expect(container.textContent).toContain('5.0 mi');
    expect(container.textContent).not.toContain('8.1 km');
  });

  it('renders it in km when km are selected', () => {
    const { container } = render(<LightningCard current={striking} unit="km" />);
    expect(container.textContent).toContain('8.1 km');
  });

  it('converts the ring labels too, so the scale matches the readout', () => {
    const { container } = render(<LightningCard current={striking} unit="mi" />);
    const rings = Array.from(container.querySelectorAll('.ring')).map((r) => r.textContent);
    expect(rings).toEqual(['6 mi', '12 mi', '25 mi']);
  });

  it('converts the sensor-range line when no strikes are detected', () => {
    const { container } = render(<LightningCard current={baseCurrent} unit="mi" />);
    expect(container.textContent).toContain('Range: up to 25 mi');
  });

  // The indicator is a fraction of the sensor's fixed 40 km reach, so it must
  // stay in km regardless of the display unit or it would drift off the rings.
  it('places the indicator from the km value, not the converted one', () => {
    const { container } = render(<LightningCard current={striking} unit="mi" />);
    const el = container.querySelector<HTMLElement>('.lightning-indicator')!;
    expect(el.style.top).toBe(`${(8.05 / 40) * 90}%`);
  });
});
