import { memo } from 'react';
import type { CurrentObservation, DistanceUnit } from '../types/weather';
import { formatDistance } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Badge } from './primitives/Badge';
import { Readout } from './primitives/Readout';
import { StatRow } from './primitives/StatRow';

// The sensor's own reach. Fixed in km because it is a property of the hardware,
// not of the display: the rings and the indicator are both fractions of it.
const SENSOR_RANGE_KM = 40;
const RING_KM = [10, 20, 40] as const;

interface LightningCardProps {
  current: CurrentObservation;
  unit: DistanceUnit;
}

function LightningCardImpl({ current, unit }: LightningCardProps) {
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
        <StatRow>
          <Readout
            value={current.lightningStrikeCount}
            qualifier="strikes today"
            valueClassName="lightning-count-value"
          />
          <Readout
            value={
              hasStrikes && current.lightningStrikeAvgDistance > 0
                ? formatDistance(current.lightningStrikeAvgDistance, unit)
                : '—'
            }
            qualifier="avg distance"
            valueClassName={hasStrikes ? undefined : 'lightning-distance-none'}
          />
        </StatRow>
        {!hasStrikes && (
          <div className="lightning-clear">
            <span className="lightning-clear-text">No lightning detected</span>
            <span className="lightning-range-text">
              Range: up to {formatDistance(SENSOR_RANGE_KM, unit, 0)}
            </span>
          </div>
        )}
      </div>

      {hasStrikes && (
        <div className="lightning-distance-rings">
          {RING_KM.map((km) => (
            <div key={km} className={`ring ring-${km}`}>
              {formatDistance(km, unit, 0)}
            </div>
          ))}
          {current.lightningStrikeAvgDistance > 0 && (
            <div
              className="lightning-indicator"
              style={{
                // From the km value, not the converted one: the rings are
                // fractions of the sensor's km reach, so converting here would
                // slide the indicator off its own scale.
                top: `${Math.min(90, (current.lightningStrikeAvgDistance / SENSOR_RANGE_KM) * 90)}%`,
              }}
            />
          )}
        </div>
      )}
    </GlassCard>
  );
}

export const LightningCard = memo(LightningCardImpl);
