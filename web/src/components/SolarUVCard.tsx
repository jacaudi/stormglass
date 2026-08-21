import { memo } from 'react';
import type { CurrentObservation } from '../types/weather';
import { GlassCard } from './GlassCard';
import { formatX } from '../utils/format';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';
import { Readout } from './primitives/Readout';
import { ScaleBar } from './primitives/ScaleBar';

interface SolarUVCardProps {
  current: CurrentObservation;
}

function uvLabel(uv: number): string {
  if (uv <= 2) return 'Low';
  if (uv <= 5) return 'Moderate';
  if (uv <= 7) return 'High';
  if (uv <= 10) return 'Very High';
  return 'Extreme';
}

function uvColorClass(uv: number): string {
  if (uv <= 2) return 'uv-low';
  if (uv <= 5) return 'uv-moderate';
  if (uv <= 7) return 'uv-high';
  if (uv <= 10) return 'uv-very-high';
  return 'uv-extreme';
}

function solarIntensity(radiation: number): string {
  if (radiation === 0) return 'None';
  if (radiation < 200) return 'Low';
  if (radiation < 500) return 'Moderate';
  if (radiation < 800) return 'High';
  return 'Very High';
}

function SolarUVCardImpl({ current }: SolarUVCardProps) {
  return (
    <GlassCard className="solar-uv-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="5" />
          <line x1="12" y1="1" x2="12" y2="3" />
          <line x1="12" y1="21" x2="12" y2="23" />
          <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
          <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
          <line x1="1" y1="12" x2="3" y2="12" />
          <line x1="21" y1="12" x2="23" y2="12" />
          <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
          <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
        </svg>
        <span className="card-title">Solar &amp; UV</span>
      </div>

      <div className="solar-uv-grid">
        <div className="uv-section">
          <Readout
            inline
            value={formatX(current.uvIndex, (n) => n.toFixed(1))}
            qualifier={uvLabel(current.uvIndex)}
            valueClassName={uvColorClass(current.uvIndex)}
          />
          <ScaleBar
            className="uv-scale"
            percent={(current.uvIndex / 11) * 100}
            indicator
            ticks={['0', '3', '6', '8', '11+']}
          />
        </div>

        <StatRow minColumn={130}>
          <Stat
            label="Solar Radiation"
            value={`${formatX(current.solarRadiation, (n) => `${Math.round(n * 10) / 10}`)} W/m²`}
            sublabel={solarIntensity(current.solarRadiation)}
          />
          <Stat label="Illuminance" value={`${current.illuminance.toLocaleString()} lux`} />
        </StatRow>
      </div>
    </GlassCard>
  );
}

export const SolarUVCard = memo(SolarUVCardImpl);
