import { describe, it, expect } from 'vitest';
import { render, within } from '@testing-library/react';
import { WindCard } from './WindCard';
import { formatWind } from '../hooks/useUnits';
import { PrecipitationType, PressureTrend, type CurrentObservation } from '../types/weather';

// Characterization test written BEFORE the Task 19 conversion of WindCard from
// hand-built markup to the Stat/StatRow/Readout primitives -- WindCard has no
// prior test file. Assertions are anchored to CONTENT (the compass centre
// text's concatenated string, and the label/value pairing found by DOM
// sibling order) rather than to the wrapper class names the conversion
// deletes (.wind-speed-value, .wind-direction-text, .wind-stats, .wind-stat),
// so the same assertions hold unmodified both before and after the
// conversion. `.compass-center-text` itself is kept by the brief, so scoping
// to it is stable across the swap. The pre-existing `.stat-label`/`.stat-value`
// classes on Lull/Gust are ALSO stable across the swap -- WindCard already
// used those names before Stat existed, per Stat's own doc comment ("wind +
// solar, the only pre-existing reuse").
const baseCurrent: CurrentObservation = {
  timestamp: 1700000000,
  windLull: 1.2,
  windAvg: 3.4,
  windGust: 5.6,
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

describe('WindCard converted readouts (design §6.5, Task 19)', () => {
  it('renders the card title, the compass readout (value + qualifier) and the lull / gust stats', () => {
    const { container } = render(<WindCard current={baseCurrent} unit="mph" />);

    expect(container.querySelector('.card-title')!.textContent).toBe('Wind');

    // windDirection 180 -> degToCompass(180) = "S". Concatenated with no
    // separator both before (two sibling spans) and after (Readout's value +
    // qualifier spans) the conversion.
    const centerText = container.querySelector('.compass-center-text')!;
    expect(centerText.textContent).toBe(`${formatWind(baseCurrent.windAvg, 'mph')}S (180°)`);

    // Lull / Gust: found by sibling order (label span immediately followed by
    // its value span), which holds both in the pre-conversion .wind-stat
    // wrapper and in the Stat primitive's .stat wrapper -- both already use
    // .stat-label/.stat-value, so a dropped Lull/Gust or a swapped unit is
    // still caught even though the class names don't change across the swap.
    const lullLabel = within(container).getByText('Lull');
    expect(lullLabel.nextElementSibling?.textContent).toBe(formatWind(baseCurrent.windLull, 'mph'));

    const gustLabel = within(container).getByText('Gust');
    expect(gustLabel.nextElementSibling?.textContent).toBe(formatWind(baseCurrent.windGust, 'mph'));
  });

  it('renders a second direction/unit combination without dropping a field', () => {
    // windDirection 45 -> "NE"; unit "kts" exercises a different formatWind
    // branch than the first case's "mph".
    const current = { ...baseCurrent, windDirection: 45, windLull: 0.5, windAvg: 6.7, windGust: 12.3 };
    const { container } = render(<WindCard current={current} unit="kts" />);

    const centerText = container.querySelector('.compass-center-text')!;
    expect(centerText.textContent).toBe(`${formatWind(current.windAvg, 'kts')}NE (45°)`);

    const lullLabel = within(container).getByText('Lull');
    expect(lullLabel.nextElementSibling?.textContent).toBe(formatWind(current.windLull, 'kts'));

    const gustLabel = within(container).getByText('Gust');
    expect(gustLabel.nextElementSibling?.textContent).toBe(formatWind(current.windGust, 'kts'));
  });

  it('renders the PRIMITIVE classes the conversion introduces on the compass readout', () => {
    // Asserts against .readout-value / .readout-qualifier, which the
    // pre-conversion markup has neither of -- red before the conversion,
    // green after.
    const { container } = render(<WindCard current={baseCurrent} unit="mph" />);
    const centerText = container.querySelector<HTMLElement>('.compass-center-text')!;

    expect(
      within(centerText).getByText(formatWind(baseCurrent.windAvg, 'mph')),
    ).toHaveClass('readout-value');
    expect(within(centerText).getByText('S (180°)')).toHaveClass('readout-qualifier');
  });
});
