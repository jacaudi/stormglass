import { describe, it, expect } from 'vitest';
import { render, within } from '@testing-library/react';
import { HumidityCard } from './HumidityCard';
import { formatTemp } from '../hooks/useUnits';
import { PrecipitationType, PressureTrend, type CurrentObservation } from '../types/weather';

// Characterization test written BEFORE the Task 18 conversion of HumidityCard
// from hand-built markup to the Stat/StatRow/Readout primitives. Assertions
// are anchored to CONTENT (the ring text's concatenated string, and the
// label/value pairing found by DOM sibling order) rather than to the wrapper
// class names the conversion deletes (.humidity-value, .humidity-level,
// .humidity-stats-row, .humidity-stat, .humidity-stat-label,
// .humidity-stat-value), so the same assertions hold unmodified both before
// and after the conversion. `.humidity-ring-text` itself is kept by the
// brief (it is the ring's absolutely-positioned overlay, not a primitive), so
// scoping to it is stable across the swap.
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

describe('HumidityCard converted readouts (design §6.5, Task 18)', () => {
  it('renders the card title, the ring readout (value + qualifier) and the dew point / wet bulb stats', () => {
    const { container } = render(<HumidityCard current={baseCurrent} tempUnit="F" />);

    expect(container.querySelector('.card-title')!.textContent).toBe('Humidity');

    // relativeHumidity 55 -> Math.round(55) = "55%"; 55 is >=50 and <70 ->
    // "Humid". Concatenated with no separator both before (two sibling
    // spans) and after (Readout's value + qualifier spans) the conversion.
    const ringText = container.querySelector('.humidity-ring-text')!;
    expect(ringText.textContent).toBe('55%Humid');

    // Dew Point / Wet Bulb: found by sibling order (label span immediately
    // followed by its value span), which holds both in the pre-conversion
    // .humidity-stat wrapper and in the Stat primitive's .stat wrapper --
    // unlike the wrapper class names themselves, which differ across the
    // swap (.humidity-stat-label/-value vs .stat-label/.stat-value).
    const dewLabel = within(container).getByText('Dew Point');
    expect(dewLabel.nextElementSibling?.textContent).toBe(formatTemp(baseCurrent.dewPoint, 'F'));

    const wetLabel = within(container).getByText('Wet Bulb');
    expect(wetLabel.nextElementSibling?.textContent).toBe(
      formatTemp(baseCurrent.wetBulbTemperature, 'F'),
    );
  });

  it('renders the wide cases: a four-character value, the longest qualifier, and the lowest band', () => {
    // These assert against the PRIMITIVE classes the conversion introduces
    // (.readout-value / .readout-qualifier), so they are red against the
    // pre-conversion markup (which has neither class) and green after.
    // rh=100 covers BOTH the four-character value ("100%") AND the
    // rh>=70/"Very Humid" qualifier in one case; rh=75 is a second, more
    // typical rh>=70 case with a three-character value, so "Very Humid"
    // isn't verified only at the extreme; rh=15 covers the lowest band.
    const cases: Array<[number, string, string]> = [
      [100, '100%', 'Very Humid'],
      [75, '75%', 'Very Humid'],
      [15, '15%', 'Dry'],
    ];

    for (const [relativeHumidity, expectedValue, expectedQualifier] of cases) {
      const { container, unmount } = render(
        <HumidityCard current={{ ...baseCurrent, relativeHumidity }} tempUnit="F" />,
      );
      const ringText = container.querySelector<HTMLElement>('.humidity-ring-text')!;
      expect(within(ringText).getByText(expectedValue)).toHaveClass('readout-value');
      expect(within(ringText).getByText(expectedQualifier)).toHaveClass('readout-qualifier');
      unmount();
    }
  });
});
