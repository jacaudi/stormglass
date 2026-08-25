import { memo } from 'react';
import type {
  CurrentObservation,
  PressureUnit,
  RecordsMinMax,
  RecordsWindowDays,
} from '../types/weather';
import { formatPressure } from '../hooks/useUnits';
import { PressureTrend } from '../types/weather';
import { GlassCard } from './GlassCard';
import { Readout } from './primitives/Readout';
import { ScaleBar } from './primitives/ScaleBar';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface PressureCardProps {
  current: CurrentObservation;
  unit: PressureUnit;
  /**
   * The pressure extremes over the records window, or null before the summary
   * has arrived. Either bound is independently null when the window holds no
   * reading for it -- RecordsCard applies the same rule to the same data.
   */
  range: RecordsMinMax | null;
  /** Window the range covers, so the labels say what they are measuring. */
  windowDays: RecordsWindowDays;
}

// The almanac, RecordsCard and StationHealth all render absent data this way.
const EM_DASH = '\u2014';

function trendArrow(trend: PressureTrend): string {
  switch (trend) {
    case PressureTrend.Rising: return '↑';
    case PressureTrend.Falling: return '↓';
    default: return '→';
  }
}

function trendLabel(trend: PressureTrend): string {
  switch (trend) {
    case PressureTrend.Rising: return 'Rising';
    case PressureTrend.Falling: return 'Falling';
    default: return 'Steady';
  }
}

function PressureCardImpl({ current, unit, range, windowDays }: PressureCardProps) {
  // null stays null: 0 mb is not a pressure anyone should read, so an absent
  // bound renders as an em-dash rather than as a number.
  const bound = (v: number | null | undefined): string =>
    v === null || v === undefined ? EM_DASH : formatPressure(v, unit);

  return (
    <GlassCard className="pressure-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="10" />
          <path d="M12 6v6l4 2" />
        </svg>
        <span className="card-title">Pressure</span>
      </div>
      <Readout
        inline
        value={formatPressure(current.stationPressure, unit)}
        qualifier={
          <span className={`pressure-trend trend-${current.pressureTrend}`}>
            {trendArrow(current.pressureTrend)} {trendLabel(current.pressureTrend)}
          </span>
        }
      />
      <div className="pressure-gauge">
        <ScaleBar
          percent={((current.stationPressure - 980) / 60) * 100}
          fillClassName="pressure-gauge-fill"
          ticks={['Low', 'Normal', 'High']}
        />
      </div>

      <StatRow divider minColumn={110}>
        <Stat label={`${windowDays}-Day Low`} value={bound(range?.min)} />
        <Stat label={`${windowDays}-Day High`} value={bound(range?.max)} />
      </StatRow>
    </GlassCard>
  );
}

export const PressureCard = memo(PressureCardImpl);
