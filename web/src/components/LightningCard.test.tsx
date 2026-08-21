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
    const { container } = render(<LightningCard current={baseCurrent} />);

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
    const { container } = render(<LightningCard current={striking} />);

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
