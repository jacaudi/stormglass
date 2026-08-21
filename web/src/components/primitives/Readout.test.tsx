import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Readout } from './Readout';

describe('Readout', () => {
  it('renders the value with the primary size by default', () => {
    const { container } = render(<Readout value="999.8 mb" />);
    const el = container.querySelector('.readout')!;
    expect(el).toHaveClass('readout-primary');
    expect(el.querySelector('.readout-value')!.textContent).toBe('999.8 mb');
  });

  it('reserves the hero size for an explicit request', () => {
    const { container } = render(<Readout value="74°" size="hero" />);
    expect(container.querySelector('.readout')).toHaveClass('readout-hero');
  });

  it('renders the qualifier only when given, and inline when asked', () => {
    const { container: none } = render(<Readout value="7" />);
    expect(none.querySelector('.readout-qualifier')).toBeNull();

    const { container: stacked } = render(<Readout value="7" qualifier="strikes today" />);
    expect(stacked.querySelector('.readout-qualifier')!.textContent).toBe('strikes today');
    expect(stacked.querySelector('.readout')).not.toHaveClass('readout-inline');

    const { container: inline } = render(<Readout value="7" qualifier="↓ Falling" inline />);
    expect(inline.querySelector('.readout')).toHaveClass('readout-inline');
  });

  it('puts a colour modifier on the value, not the wrapper', () => {
    const { container } = render(<Readout value="6.3" valueClassName="uv-high" />);
    expect(container.querySelector('.readout-value')).toHaveClass('uv-high');
    expect(container.querySelector('.readout')).not.toHaveClass('uv-high');
  });
});
