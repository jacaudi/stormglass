import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StationHealth } from './StationHealth';
import type { StationStatus } from '../types/weather';

const base: StationStatus = {
  isOnline: true,
  lastReport: Math.floor(Date.now() / 1000) - 30,
  batteryLevel: 2.71,
  signalStrength: null,
  firmwareVersion: null,
};

describe('StationHealth — absent data must not render as a reading', () => {
  // The defect this guards: signalStrength and firmwareVersion have no source
  // in Contract C, and rendering them as 0 / '' produced "SIGNAL 0/4" beside
  // four invisible bars and a blank FIRMWARE. "0/4" states NO SIGNAL on a
  // healthy station -- worse than stating nothing. No geometric threshold can
  // see this: cardText compares textContent, so "0/4" reads as content, and
  // the bars are var(--bg-secondary) against the card, so they are invisible
  // rather than absent.
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

  // The other half of the contract: once the follow-up plumbs device_status's
  // rssi and firmware_revision through, the card must render them without any
  // further change. If this breaks, the em-dash path has swallowed real data.
  it('renders real values when they ARE reported', () => {
    const { container } = render(
      <StationHealth status={{ ...base, signalStrength: 3, firmwareVersion: '176' }} />
    );
    expect(container.querySelector('.signal-bars')).not.toBeNull();
    expect(container.querySelectorAll('.signal-bar')).toHaveLength(4);
    expect(container.querySelectorAll('.signal-bar.active')).toHaveLength(3);
    expect(container.textContent).toContain('3/4');
    const stats = Array.from(container.querySelectorAll('.stat'));
    const fw = stats.find((s) => s.querySelector('.stat-label')?.textContent === 'Firmware')!;
    expect(fw.querySelector('.stat-value')!.textContent).toBe('176');
  });

  it('keeps the battery reading, which does have a source', () => {
    const { container } = render(<StationHealth status={base} />);
    expect(container.textContent).toContain('2.71V');
    expect(container.querySelector('.battery-fill')).not.toBeNull();
  });
});
