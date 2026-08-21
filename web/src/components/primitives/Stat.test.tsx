import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Stat } from './Stat';
import { StatRow } from './StatRow';

describe('Stat', () => {
  it('renders a label above a value', () => {
    const { container } = render(<Stat label="Gust" value="12 mph" />);
    const el = container.querySelector('.stat')!;
    expect(el.querySelector('.stat-label')!.textContent).toBe('Gust');
    expect(el.querySelector('.stat-value')!.textContent).toBe('12 mph');
    expect(el.querySelector('.stat-sublabel')).toBeNull();
  });

  it('renders the optional sublabel only when given', () => {
    const { container } = render(
      <Stat label="Solar Radiation" value="814 W/m²" sublabel="Very High" />
    );
    expect(container.querySelector('.stat-sublabel')!.textContent).toBe('Very High');
  });

  it('puts a per-card modifier on the value, not the wrapper', () => {
    const { container } = render(<Stat label="UV" value="6.3" valueClassName="uv-high" />);
    expect(container.querySelector('.stat-value')).toHaveClass('uv-high');
    expect(container.querySelector('.stat')).not.toHaveClass('uv-high');
  });
});

describe('StatRow', () => {
  it('wraps its children in one distribution rule', () => {
    const { container } = render(
      <StatRow>
        <Stat label="Lull" value="1" />
        <Stat label="Gust" value="3" />
      </StatRow>
    );
    const row = container.querySelector('.stat-row')!;
    expect(row.querySelectorAll('.stat')).toHaveLength(2);
  });

  it('exposes the wrap floor as a custom property rather than a media query', () => {
    const { container } = render(<StatRow minColumn={130}><Stat label="a" value="b" /></StatRow>);
    const row = container.querySelector('.stat-row') as HTMLElement;
    expect(row.style.getPropertyValue('--stat-row-min')).toBe('130px');
  });

  it('adds the hairline only when asked', () => {
    const { container: withDiv } = render(<StatRow divider><Stat label="a" value="b" /></StatRow>);
    const { container: without } = render(<StatRow><Stat label="a" value="b" /></StatRow>);
    expect(withDiv.querySelector('.stat-row')).toHaveClass('stat-row-divided');
    expect(without.querySelector('.stat-row')).not.toHaveClass('stat-row-divided');
  });
});
