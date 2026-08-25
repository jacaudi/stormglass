/**
 * A horizontal track with a proportional fill, an optional position marker and
 * optional tick labels. Replaces .gauge-* (pressure) and .uv-bar-* (solar) --
 * the same component built twice, which is why one of them looked right and the
 * other did not.
 *
 * Clamping lives here rather than at the call sites: PressureCard clamped
 * inline and SolarUVCard clamped only the upper bound, so a negative UV index
 * would have rendered a negative width.
 */
export interface ScaleBarProps {
  /** 0-100. Clamped by the primitive, not by callers. */
  percent: number;
  /** Draw a position marker on top of the fill. Default false. */
  indicator?: boolean;
  /** Tick labels under the track. */
  ticks?: string[];
  fillClassName?: string;
  className?: string;
}

export function ScaleBar({ percent, indicator = false, ticks, fillClassName, className }: ScaleBarProps) {
  const pct = Math.min(100, Math.max(0, Number.isFinite(percent) ? percent : 0));
  return (
    <div className={['scale-bar', className].filter(Boolean).join(' ')}>
      <div className="scale-bar-track">
        <div
          className={['scale-bar-fill', fillClassName].filter(Boolean).join(' ')}
          style={{ width: `${pct}%` }}
        />
        {indicator ? <div className="scale-bar-indicator" style={{ left: `${pct}%` }} /> : null}
      </div>
      {ticks === undefined ? null : (
        <div className="scale-bar-ticks">
          {ticks.map((t) => (
            <span key={t}>{t}</span>
          ))}
        </div>
      )}
    </div>
  );
}
