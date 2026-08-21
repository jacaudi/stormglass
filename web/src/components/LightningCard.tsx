import { memo } from 'react';
import type { CurrentObservation } from '../types/weather';
import { GlassCard } from './GlassCard';
import { Badge } from './primitives/Badge';
import { Readout } from './primitives/Readout';
import { StatRow } from './primitives/StatRow';

interface LightningCardProps {
  current: CurrentObservation;
}

function LightningCardImpl({ current }: LightningCardProps) {
  const hasStrikes = current.lightningStrikeCount > 0;

  return (
    <GlassCard className={`lightning-card ${hasStrikes ? 'lightning-active' : ''}`}>
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
        </svg>
        <span className="card-title">Lightning</span>
        {hasStrikes && <Badge tone="warning" animation="flash" className="alert-badge-slot">Detected</Badge>}
      </div>

      <div className="lightning-content">
        <StatRow minColumn={130}>
          <Readout
            value={current.lightningStrikeCount}
            qualifier="strikes today"
            valueClassName="lightning-count-value"
          />
          <Readout
            value={
              hasStrikes && current.lightningStrikeAvgDistance > 0
                ? `${current.lightningStrikeAvgDistance.toFixed(1)} km`
                : '—'
            }
            qualifier="avg distance"
            valueClassName={hasStrikes ? undefined : 'lightning-distance-none'}
          />
        </StatRow>
        {!hasStrikes && (
          <div className="lightning-clear">
            <span className="lightning-clear-text">No lightning detected</span>
            <span className="lightning-range-text">Range: up to 40 km</span>
          </div>
        )}
      </div>

      {hasStrikes && (
        <div className="lightning-distance-rings">
          <div className="ring ring-10">10km</div>
          <div className="ring ring-20">20km</div>
          <div className="ring ring-40">40km</div>
          {current.lightningStrikeAvgDistance > 0 && (
            <div
              className="lightning-indicator"
              style={{
                top: `${Math.min(90, (current.lightningStrikeAvgDistance / 40) * 90)}%`,
              }}
            />
          )}
        </div>
      )}
    </GlassCard>
  );
}

export const LightningCard = memo(LightningCardImpl);
