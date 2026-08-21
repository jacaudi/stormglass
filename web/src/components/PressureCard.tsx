import { memo } from 'react';
import type { CurrentObservation, PressureUnit } from '../types/weather';
import { formatPressure } from '../hooks/useUnits';
import { PressureTrend } from '../types/weather';
import { GlassCard } from './GlassCard';
import { Readout } from './primitives/Readout';
import { ScaleBar } from './primitives/ScaleBar';

interface PressureCardProps {
  current: CurrentObservation;
  unit: PressureUnit;
}

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

function PressureCardImpl({ current, unit }: PressureCardProps) {
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
    </GlassCard>
  );
}

export const PressureCard = memo(PressureCardImpl);
