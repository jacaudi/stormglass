import { describe, it, expect } from 'vitest';
import { render, within } from '@testing-library/react';
import { TemperatureHero } from './TemperatureHero';
import { formatTemp } from '../hooks/useUnits';
import { PrecipitationType, PressureTrend, type CurrentObservation } from '../types/weather';

// Characterization test written BEFORE the Task 21 conversion of
// TemperatureHero from hand-built markup to the Readout/Stat/StatRow
// primitives. Assertions are anchored to CONTENT (the condition and
// feels-like text, and the label/value pairing found by DOM sibling order)
// rather than to the wrapper class names the conversion deletes
// (.hero-detail-item, .detail-label, .detail-value), so the same assertions
// hold unmodified both before and after the conversion -- following the
// pattern HumidityCard.test.tsx established for Task 18. This guards against
// a dropped stat during the swap to StatRow/Stat.
const baseCurrent: CurrentObservation = {
  timestamp: 1700000000,
  windLull: 1,
  windAvg: 2,
  windGust: 3,
  windDirection: 180,
  windSampleInterval: 3,
  stationPressure: 1013,
  airTemperature: 72,
  relativeHumidity: 55,
  illuminance: 1000,
  uvIndex: 3.7,
  solarRadiation: 500,
  rainAccumulated: 0,
  precipitationType: PrecipitationType.None,
  lightningStrikeAvgDistance: 0,
  lightningStrikeCount: 0,
  battery: 2.6,
  reportInterval: 1,
  localDayRainAccumulation: 0,
  feelsLike: 70,
  dewPoint: 12,
  wetBulbTemperature: 15,
  heatIndex: 75,
  windChill: 68,
  pressureTrend: PressureTrend.Steady,
};

describe('TemperatureHero converted readouts (design §6.5/§6.6, Task 21)', () => {
  it('renders the headline temperature, condition, feels-like text, and all four detail stats', () => {
    const { container } = render(
      <TemperatureHero current={baseCurrent} unit="F" precipProbability={42.6} />,
    );

    // solarRadiation 500 is >400 and <=800 -> "Partly Cloudy". Content-only
    // assertion: .hero-condition keeps its class name across the conversion
    // (it lives inside the Readout's qualifier afterward), so this holds
    // unmodified both before and after.
    expect(within(container).getByText('Partly Cloudy')).toBeInTheDocument();

    // .hero-feels-like likewise keeps its class name and its text content
    // across the conversion.
    expect(
      within(container).getByText(`Feels like ${formatTemp(baseCurrent.feelsLike, 'F')}`),
    ).toBeInTheDocument();

    // The headline number itself: a plain span (.hero-temp) before the
    // conversion, the Readout's .readout-value span after -- the text is
    // present in the document either way.
    expect(
      within(container).getByText(formatTemp(baseCurrent.airTemperature, 'F')),
    ).toBeInTheDocument();

    // The four detail stats, found by sibling order (label span immediately
    // followed by its value span) -- true both in the pre-conversion
    // .hero-detail-item wrapper and in the Stat primitive's .stat wrapper,
    // unlike the wrapper/class names themselves (.detail-label/-value vs
    // .stat-label/-value), which differ across the swap.
    const heatIndexLabel = within(container).getByText('Heat Index');
    expect(heatIndexLabel.nextElementSibling?.textContent).toBe(
      formatTemp(baseCurrent.heatIndex, 'F'),
    );

    const windChillLabel = within(container).getByText('Wind Chill');
    expect(windChillLabel.nextElementSibling?.textContent).toBe(
      formatTemp(baseCurrent.windChill, 'F'),
    );

    const rainChanceLabel = within(container).getByText('Rain Chance');
    expect(rainChanceLabel.nextElementSibling?.textContent).toBe('43%');

    const uvIndexLabel = within(container).getByText('UV Index');
    expect(uvIndexLabel.nextElementSibling?.textContent).toBe(
      baseCurrent.uvIndex.toFixed(1),
    );
  });

  it('defaults rain chance to 0% when precipProbability is omitted', () => {
    const { container } = render(<TemperatureHero current={baseCurrent} unit="F" />);

    const rainChanceLabel = within(container).getByText('Rain Chance');
    expect(rainChanceLabel.nextElementSibling?.textContent).toBe('0%');
  });
});
