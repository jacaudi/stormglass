import { memo } from 'react';
import type { CurrentObservation, TemperatureUnit } from '../types/weather';
import { formatTemp } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Readout } from './primitives/Readout';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface HumidityCardProps {
  current: CurrentObservation;
  tempUnit: TemperatureUnit;
}

function humidityLevel(rh: number): string {
  if (rh < 30) return 'Dry';
  if (rh < 50) return 'Comfortable';
  if (rh < 70) return 'Humid';
  return 'Very Humid';
}

function HumidityCardImpl({ current, tempUnit }: HumidityCardProps) {
  const pct = current.relativeHumidity;
  const circumference = 2 * Math.PI * 40;
  const offset = circumference - (pct / 100) * circumference;

  return (
    <GlassCard className="humidity-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M12 2.69l5.66 5.66a8 8 0 1 1-11.31 0z" />
        </svg>
        <span className="card-title">Humidity</span>
      </div>

      <div className="humidity-ring-container">
        <svg className="humidity-ring" viewBox="0 0 100 100">
          <circle className="ring-bg" cx="50" cy="50" r="40" />
          <circle
            className="ring-fill"
            cx="50"
            cy="50"
            r="40"
            strokeDasharray={circumference}
            strokeDashoffset={offset}
            transform="rotate(-90 50 50)"
          />
        </svg>
        <div className="humidity-ring-text">
          <Readout
            value={`${Math.round(pct)}%`}
            qualifier={humidityLevel(pct)}
            className="humidity-ring-readout"
          />
        </div>
      </div>

      <StatRow divider minColumn={110}>
        <Stat label="Dew Point" value={formatTemp(current.dewPoint, tempUnit)} />
        <Stat label="Wet Bulb" value={formatTemp(current.wetBulbTemperature, tempUnit)} />
      </StatRow>
    </GlassCard>
  );
}

export const HumidityCard = memo(HumidityCardImpl);
