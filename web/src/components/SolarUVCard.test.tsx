import { describe, it, expect } from 'vitest';
import { render, within } from '@testing-library/react';
import { SolarUVCard } from './SolarUVCard';
import { PrecipitationType, PressureTrend, type CurrentObservation } from '../types/weather';

// Characterization test written BEFORE the Task 17 conversion of SolarUVCard
// from hand-built markup to the Stat/StatRow/Readout/ScaleBar primitives.
// Assertions are deliberately anchored to content (labels, values, units, the
// UV colour class, the tick labels, the geometry of the bar) rather than to
// the wrapper class names the conversion deletes (.uv-index-display,
// .uv-number, .uv-label, .uv-bar-track, .uv-bar-fill, .uv-bar-indicator,
// .uv-bar-labels, .solar-section, .solar-stat), so the same assertions hold
// unmodified both before and after the conversion. The two Stat blocks are
// the one exception where the class names ARE stable across the conversion
// (.stat-label/.stat-value/.stat-sublabel are already the Task 14 primitive
// classes, hand-rendered today), so those are asserted directly.
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
  illuminance: 12345,
  uvIndex: 6.4,
  solarRadiation: 245.7,
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
  signalDbm: null,
  firmwareVersion: null,
};

describe('SolarUVCard converted readouts (design §6.5, Task 17)', () => {
  it('renders the card title, the UV readout with its colour class, the tick labels and the bar geometry', () => {
    const { container } = render(<SolarUVCard current={baseCurrent} />);

    expect(container.querySelector('.card-title')!.textContent).toBe('Solar & UV');

    const uvSection = container.querySelector<HTMLElement>('.uv-section')!;
    expect(uvSection).not.toBeNull();

    // Value: "6.4", carrying the uv-high colour class directly (uvIndex 6.4 is
    // (2, 5] < uv <= 7 -> "High" / "uv-high").
    const uvValue = within(uvSection).getByText('6.4');
    expect(uvValue).toHaveClass('uv-high');

    // Qualifier: "High".
    expect(within(uvSection).getByText('High')).toBeInTheDocument();

    // Tick labels, in order: 0, 3, 6, 8, 11+. These render as bare text nodes
    // that nothing else in the section matches.
    const ticks = within(uvSection).getAllByText(/^(0|3|6|8|11\+)$/);
    expect(ticks.map((t) => t.textContent)).toEqual(['0', '3', '6', '8', '11+']);

    // Bar geometry: a fill and an indicator both positioned at
    // (uvIndex / 11) * 100 == 58.18...%, found by inline style rather than by
    // wrapper class name so the assertion survives the primitive swap.
    const expectedPercent = `${(6.4 / 11) * 100}%`;
    const allEls = Array.from(uvSection.querySelectorAll<HTMLElement>('*'));
    const fill = allEls.find((el) => el.style.width === expectedPercent);
    const indicator = allEls.find((el) => el.style.left === expectedPercent);
    expect(fill, 'expected a fill element positioned by width').toBeTruthy();
    expect(indicator, 'expected an indicator element positioned by left').toBeTruthy();
  });

  it('puts the uv-scale class on the same element as scale-bar, so the .uv-scale .scale-bar-track descendant selector actually matches', () => {
    // ScaleBar puts its `className` prop on the OUTER `.scale-bar` element, and
    // App.css colours the bar via the descendant selector
    // `.uv-scale .scale-bar-track` (and `.uv-scale .scale-bar-fill`). Neither
    // vitest's jsdom render nor any committed layout threshold applies that
    // stylesheet, so nothing else catches `className="uv-scale"` silently
    // going missing from the ScaleBar call -- which would leave the bar
    // rendering grey with zero other test failure. This test exists
    // specifically to catch that: it asserts `.uv-scale` and `.scale-bar` are
    // the SAME element, not merely that `.uv-scale` exists somewhere in the
    // tree, because "somewhere" would not make the descendant selector match.
    const { container } = render(<SolarUVCard current={baseCurrent} />);

    const scaleBar = container.querySelector('.scale-bar')!;
    expect(scaleBar).not.toBeNull();
    expect(scaleBar).toHaveClass('uv-scale');
  });

  it('renders Solar Radiation and Illuminance label/value/sublabel with correct values and units', () => {
    // Deliberately not scoped through a `.stat` wrapper: that class is the
    // Stat primitive's own wrapper, added by the conversion. Today's markup
    // wraps in `.solar-stat` instead, so the assertion holds unmodified
    // across the swap by going straight for the (already-shared, per the
    // brief) `.stat-label`/`.stat-value`/`.stat-sublabel` classes and relying
    // on DOM order: Solar Radiation renders before Illuminance both before
    // and after.
    const { container } = render(<SolarUVCard current={baseCurrent} />);

    const labels = container.querySelectorAll('.stat-label');
    const values = container.querySelectorAll('.stat-value');
    const sublabels = container.querySelectorAll('.stat-sublabel');

    expect(Array.from(labels).map((l) => l.textContent)).toEqual([
      'Solar Radiation',
      'Illuminance',
    ]);
    expect(Array.from(values).map((v) => v.textContent)).toEqual([
      '245.7 W/m²',
      '12,345 lux',
    ]);
    // solarRadiation 245.7 is >=200 and <500 -> "Moderate"; Illuminance has no
    // sublabel at all.
    expect(sublabels).toHaveLength(1);
    expect(sublabels[0].textContent).toBe('Moderate');
  });

  it('exercises the other four UV colour bands and the zero/none solar states', () => {
    const cases: Array<[number, string, string]> = [
      [1.5, 'Low', 'uv-low'],
      [4, 'Moderate', 'uv-moderate'],
      [10, 'Very High', 'uv-very-high'],
      [11.2, 'Extreme', 'uv-extreme'],
    ];

    for (const [uvIndex, label, colorClass] of cases) {
      const { container, unmount } = render(
        <SolarUVCard current={{ ...baseCurrent, uvIndex }} />,
      );
      const uvSection = container.querySelector<HTMLElement>('.uv-section')!;
      const value = within(uvSection).getByText(uvIndex.toFixed(1));
      expect(value).toHaveClass(colorClass);
      expect(within(uvSection).getByText(label)).toBeInTheDocument();
      unmount();
    }

    // solarRadiation === 0 -> "None".
    const { container } = render(
      <SolarUVCard current={{ ...baseCurrent, solarRadiation: 0 }} />,
    );
    expect(container.querySelectorAll('.stat-value')[0].textContent).toBe('0 W/m²');
    expect(container.querySelectorAll('.stat-sublabel')[0].textContent).toBe('None');
  });
});
