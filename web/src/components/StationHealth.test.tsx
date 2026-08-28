import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StationHealth } from './StationHealth';
import type { StationStatus } from '../types/weather';

const base: StationStatus = {
  isOnline: true,
  lastReport: Math.floor(Date.now() / 1000) - 30,
  batteryLevel: 2.71,
  signalDbm: null,
  firmwareVersion: null,
};

describe('StationHealth — absent data must not render as a reading', () => {
  // The defect this guards: rendering an absent reading as 0 / '' produced
  // "SIGNAL 0/4" beside four invisible bars and a blank FIRMWARE. "0/4" states
  // NO SIGNAL on a healthy station -- worse than stating nothing. No geometric
  // threshold can see it: cardText compares textContent, so "0/4" reads as
  // content, and the bars were var(--bg-secondary) against the card, so they
  // were invisible rather than absent.
  //
  // #196 gave both fields a real source (device_status) and changed signal
  // from 0-4 bars to raw dBm, but the guard is unchanged in substance: the
  // server still sends null when there is no row, when it is stale, or when
  // its query failed, and 0 dBm is a VALID reading that must never collapse
  // into that.
  it('shows an em-dash and no signal bars when signal is not reported', () => {
    const { container } = render(<StationHealth status={base} />);
    const stats = Array.from(container.querySelectorAll('.stat'));
    const signal = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Signal')!;
    expect(signal.querySelector('.stat-value')!.textContent).toBe('—');
    expect(container.querySelector('.signal-bars')).toBeNull();
    expect(container.textContent).not.toContain('0/4');
  });

  it('shows an em-dash when firmware is not reported', () => {
    const { container } = render(<StationHealth status={base} />);
    const stats = Array.from(container.querySelectorAll('.stat'));
    const fw = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Firmware')!;
    expect(fw.querySelector('.stat-value')!.textContent).toBe('—');
  });

  // The other half of the contract: real values must actually render. If this
  // breaks, the em-dash path has swallowed real data.
  //
  // This assertion was rewritten by #196, with explicit authorisation. It
  // previously required signal bars and the text "3/4", encoding the
  // assumption that plumbing the data through would need no UI change. That
  // assumption was overturned deliberately: WeatherFlow publishes no
  // dBm-to-bars mapping, so bars would have meant inventing thresholds and
  // presenting a guess as a measurement. The INTENT -- real values render, and
  // are distinguishable from the em-dash -- is preserved exactly; only the
  // units changed.
  it('renders real values when they ARE reported', () => {
    const { container } = render(
      <StationHealth status={{ ...base, signalDbm: -61, firmwareVersion: '176' }} />
    );
    const stats = Array.from(container.querySelectorAll('.stat'));
    const signal = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Signal')!;
    expect(signal.querySelector('.stat-value')!.textContent).toBe('-61 dBm');
    expect(container.textContent).not.toContain('—');
    const fw = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Firmware')!;
    expect(fw.querySelector('.stat-value')!.textContent).toBe('176');
  });

  // 0 dBm is a VALID reading, so it must render as one rather than collapsing
  // into the unknown path. This is the case the old 0-4 scale could not
  // express at all, since 0 there meant "no signal".
  it('renders a reported 0 dBm as a reading, not as absent', () => {
    const { container } = render(<StationHealth status={{ ...base, signalDbm: 0 }} />);
    const stats = Array.from(container.querySelectorAll('.stat'));
    const signal = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Signal')!;
    expect(signal.querySelector('.stat-value')!.textContent).toBe('0 dBm');
  });

  it('keeps the battery reading, which does have a source', () => {
    const { container } = render(<StationHealth status={base} />);
    expect(container.textContent).toContain('2.71V');
    expect(container.querySelector('.battery-fill')).not.toBeNull();
  });
});
