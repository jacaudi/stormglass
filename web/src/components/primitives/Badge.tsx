import type { ReactNode } from 'react';

/**
 * The one pill shape. Replaces five hand-built implementations in four shapes:
 * .status-badge (radius 20px), .stale-badge / .rain-active-badge /
 * .lightning-alert-badge (10px) and .records-window (999px).
 *
 * `tone` and `animation` are SEPARATE axes on purpose. The rain and lightning
 * alert badges differ in three declarations -- background, text colour, and a
 * different keyframe at a different duration -- so a tone prop alone would have
 * silently dropped one of the two animations.
 */
export type BadgeTone = 'neutral' | 'info' | 'warning' | 'danger' | 'success';
export type BadgeAnimation = 'none' | 'pulse' | 'flash';

export interface BadgeProps {
  tone?: BadgeTone;
  animation?: BadgeAnimation;
  /** Fully-rounded (999px) rather than the default 10px. */
  pill?: boolean;
  className?: string;
  role?: string;
  children: ReactNode;
}

export function Badge({
  tone = 'neutral',
  animation = 'none',
  pill = false,
  className,
  role,
  children,
}: BadgeProps) {
  const classes = [
    'badge',
    `badge-${tone}`,
    pill ? 'badge-pill' : null,
    animation === 'none' ? null : `badge-anim-${animation}`,
    className ?? null,
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <span className={classes} role={role}>
      {children}
    </span>
  );
}
