import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Badge } from './Badge';

describe('Badge', () => {
  it('renders one pill class plus a tone modifier', () => {
    const { container } = render(<Badge tone="info">Active</Badge>);
    const el = container.querySelector('.badge')!;
    expect(el).not.toBeNull();
    expect(el).toHaveClass('badge-info');
    expect(el.textContent).toBe('Active');
  });

  it('defaults to the neutral tone with no animation', () => {
    const { container } = render(<Badge>Live</Badge>);
    const el = container.querySelector('.badge')!;
    expect(el).toHaveClass('badge-neutral');
    expect(el.className).not.toMatch(/badge-anim-/);
  });

  it('keeps the two alert animations distinct', () => {
    // The rain and lightning badges differ in background, text colour AND
    // animation keyframe. A tone prop alone cannot express the third, so this
    // asserts the animation is its own axis.
    const { container: a } = render(<Badge tone="info" animation="pulse">Active</Badge>);
    const { container: b } = render(<Badge tone="warning" animation="flash">Detected</Badge>);
    expect(a.querySelector('.badge')).toHaveClass('badge-anim-pulse');
    expect(b.querySelector('.badge')).toHaveClass('badge-anim-flash');
  });

  it('supports the fully-rounded pill shape used by the records window', () => {
    const { container } = render(<Badge pill>Last 7 days</Badge>);
    expect(container.querySelector('.badge')).toHaveClass('badge-pill');
  });

  it('passes role through for the stale announcement', () => {
    const { container } = render(<Badge tone="danger" role="status">Stale</Badge>);
    expect(container.querySelector('.badge')).toHaveAttribute('role', 'status');
  });
});
