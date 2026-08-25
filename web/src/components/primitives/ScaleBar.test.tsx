import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { ScaleBar } from './ScaleBar';

describe('ScaleBar', () => {
  it('renders a track with a fill sized by percent', () => {
    const { container } = render(<ScaleBar percent={40} />);
    const fill = container.querySelector('.scale-bar-fill') as HTMLElement;
    expect(container.querySelector('.scale-bar-track')).not.toBeNull();
    expect(fill.style.width).toBe('40%');
  });

  it('clamps out-of-range input rather than trusting the caller', () => {
    // PressureCard clamped inline; SolarUVCard clamped only the upper bound.
    const { container: over } = render(<ScaleBar percent={140} />);
    const { container: under } = render(<ScaleBar percent={-20} />);
    expect((over.querySelector('.scale-bar-fill') as HTMLElement).style.width).toBe('100%');
    expect((under.querySelector('.scale-bar-fill') as HTMLElement).style.width).toBe('0%');
  });

  it('omits the indicator unless asked, and positions it with the fill', () => {
    const { container: plain } = render(<ScaleBar percent={50} />);
    expect(plain.querySelector('.scale-bar-indicator')).toBeNull();

    const { container: marked } = render(<ScaleBar percent={50} indicator />);
    const ind = marked.querySelector('.scale-bar-indicator') as HTMLElement;
    expect(ind.style.left).toBe('50%');
  });

  it('renders tick labels when given and nothing when not', () => {
    const { container: none } = render(<ScaleBar percent={10} />);
    expect(none.querySelector('.scale-bar-ticks')).toBeNull();

    const { container } = render(<ScaleBar percent={10} ticks={['Low', 'Normal', 'High']} />);
    expect(container.querySelectorAll('.scale-bar-ticks > span')).toHaveLength(3);
  });
});
