import { memo } from 'react';
import type { CurrentObservation, RainUnit } from '../types/weather';
import { PrecipitationType } from '../types/weather';
import { formatRain } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Badge } from './primitives/Badge';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface RainCardProps {
  current: CurrentObservation;
  unit: RainUnit;
}

const RAINDROP_COUNT = 12;

interface RaindropStyle {
  left: string;
  animationDelay: string;
  animationDuration: string;
}

// Randomised once at module load rather than inside a useMemo. Math.random()
// is impure and a useMemo callback runs during render, which is exactly what
// react-hooks/purity forbids. Hoisting also serves the original intent better:
// the positions must not reshuffle on every 3s data tick (P2.13), and module
// scope fixes them for the lifetime of the page rather than merely per mount.
const RAINDROP_STYLES: RaindropStyle[] = Array.from({ length: RAINDROP_COUNT }, () => ({
  left: `${8 + Math.random() * 84}%`,
  animationDelay: `${Math.random() * 1}s`,
  animationDuration: `${0.5 + Math.random() * 0.5}s`,
}));

function precipLabel(type: PrecipitationType): string {
  switch (type) {
    case PrecipitationType.Rain: return 'Rain';
    case PrecipitationType.Hail: return 'Hail';
    case PrecipitationType.RainAndHail: return 'Rain & Hail';
    default: return 'None';
  }
}

function RainCardImpl({ current, unit }: RainCardProps) {
  const isRaining = current.precipitationType !== PrecipitationType.None;

  return (
    <GlassCard className="rain-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M20 17.58A5 5 0 0 0 18 8h-1.26A8 8 0 1 0 4 16.25" />
          <line x1="8" y1="16" x2="8" y2="20" />
          <line x1="12" y1="18" x2="12" y2="22" />
          <line x1="16" y1="16" x2="16" y2="20" />
        </svg>
        <span className="card-title">Precipitation</span>
        {isRaining && <Badge tone="info" animation="pulse" className="alert-badge-slot">Active</Badge>}
      </div>

      <StatRow className="rain-grid" minColumn={110}>
        <Stat
          label="Current"
          value={formatRain(current.rainAccumulated, unit)}
          sublabel={precipLabel(current.precipitationType)}
        />
        <Stat label="Today" value={formatRain(current.localDayRainAccumulation, unit)} />
      </StatRow>

      {isRaining && (
        <div className="rain-animation">
          {RAINDROP_STYLES.map((style, i) => (
            <div key={i} className="raindrop" style={style} />
          ))}
        </div>
      )}
    </GlassCard>
  );
}

export const RainCard = memo(RainCardImpl);
