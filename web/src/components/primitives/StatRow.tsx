import type { CSSProperties, ReactNode } from 'react';

/**
 * Two or three Stats across a card, with one distribution rule and one wrap
 * floor. Replaces the seven row wrappers the census found (.wind-stats,
 * .humidity-stats-row, .rain-grid, .lightning-stats-row, .health-grid,
 * .solar-section and .hero-details).
 *
 * The wrap floor is a custom property rather than a media query because a card
 * is 1, 2 or 3 grid tracks wide and the row has to wrap on its OWN width, not
 * the viewport's.
 */
export interface StatRowProps {
  children: ReactNode;
  /** Minimum column width before the row wraps. Default 120. */
  minColumn?: number;
  /** Hairline above the row, as .wind-stats and .humidity-stats-row have. */
  divider?: boolean;
  className?: string;
}

export function StatRow({ children, minColumn = 120, divider = false, className }: StatRowProps) {
  const style = { '--stat-row-min': `${minColumn}px` } as CSSProperties;
  return (
    <div
      className={['stat-row', divider ? 'stat-row-divided' : null, className]
        .filter(Boolean)
        .join(' ')}
      style={style}
    >
      {children}
    </div>
  );
}
