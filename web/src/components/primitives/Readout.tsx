import type { ReactNode } from 'react';

/**
 * The card's headline number, its qualifier, AND its size.
 *
 * Eight ad-hoc sizes for one concept collapse to two steps. --readout-hero is
 * flat rather than clamped on purpose: the root type scale already shrinks it
 * with the viewport, and a second clamp on top measured SMALLER than today at
 * mobile (60.4px -> 41.5px).
 *
 * Readout owns numbers, not glyphs -- the hero's WeatherIcon belongs to
 * whatever renders it.
 */
export interface ReadoutProps {
  value: ReactNode;
  qualifier?: ReactNode;
  size?: 'hero' | 'primary';
  inline?: boolean;
  valueClassName?: string;
  className?: string;
}

export function Readout({
  value,
  qualifier,
  size = 'primary',
  inline = false,
  valueClassName,
  className,
}: ReadoutProps) {
  return (
    <div
      className={['readout', `readout-${size}`, inline ? 'readout-inline' : null, className]
        .filter(Boolean)
        .join(' ')}
    >
      <span className={['readout-value', valueClassName].filter(Boolean).join(' ')}>{value}</span>
      {qualifier === undefined ? null : <span className="readout-qualifier">{qualifier}</span>}
    </div>
  );
}
