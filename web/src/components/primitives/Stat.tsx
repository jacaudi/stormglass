import type { ReactNode } from 'react';

/**
 * A label above a value. Replaces eight independent implementations:
 * .detail-*, .stat-* (wind + solar, the only pre-existing reuse),
 * .humidity-stat-*, .rain-stat-*, .lightning-label, .health-*, .rstat-* and
 * .almanac-hl-*.
 *
 * Alignment lives here rather than in eight parallel card rules: today
 * .humidity-stat centres, .rain-stat-block sets nothing (so PRECIPITATION
 * renders hard-left) and .lightning-stats-row sets flex-start.
 */
export interface StatProps {
  label: string;
  value: ReactNode;
  /** Rendered under the value, e.g. SolarUV's "Very High". */
  sublabel?: ReactNode;
  /** Extra class on the value element only, for per-card colour modifiers. */
  valueClassName?: string;
  className?: string;
}

export function Stat({ label, value, sublabel, valueClassName, className }: StatProps) {
  return (
    <div className={['stat', className].filter(Boolean).join(' ')}>
      <span className="stat-label">{label}</span>
      <span className={['stat-value', valueClassName].filter(Boolean).join(' ')}>{value}</span>
      {sublabel === undefined ? null : <span className="stat-sublabel">{sublabel}</span>}
    </div>
  );
}
