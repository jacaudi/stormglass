import { memo, useState, useEffect, useRef } from 'react';
import type { CurrentObservation, WindUnit } from '../types/weather';
import { formatWind } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Readout } from './primitives/Readout';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface WindCardProps {
  current: CurrentObservation;
  unit: WindUnit;
}

function degToCompass(deg: number): string {
  const dirs = ['N', 'NNE', 'NE', 'ENE', 'E', 'ESE', 'SE', 'SSE',
                'S', 'SSW', 'SW', 'WSW', 'W', 'WNW', 'NW', 'NNW'];
  return dirs[Math.round(deg / 22.5) % 16];
}

function WindCardImpl({ current, unit }: WindCardProps) {
  const compassDir = degToCompass(current.windDirection);

  // Track accumulated rotation so the needle always takes the shortest arc
  const [displayDeg, setDisplayDeg] = useState(current.windDirection);
  const prevRawRef = useRef(current.windDirection);

  useEffect(() => {
    const raw = current.windDirection;
    let delta = raw - prevRawRef.current;
    if (delta > 180) delta -= 360;
    if (delta < -180) delta += 360;
    setDisplayDeg(prev => prev + delta);
    prevRawRef.current = raw;
  }, [current.windDirection]);

  return (
    <GlassCard className="wind-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M9.59 4.59A2 2 0 1 1 11 8H2m10.59 11.41A2 2 0 1 0 14 16H2m15.73-8.27A2.5 2.5 0 1 1 19.5 12H2" />
        </svg>
        <span className="card-title">Wind</span>
      </div>

      <div className="wind-compass">
        <div className="compass-ring">
          <span className="compass-label compass-n">N</span>
          <span className="compass-label compass-e">E</span>
          <span className="compass-label compass-s">S</span>
          <span className="compass-label compass-w">W</span>
          <div
            className="compass-needle"
            style={{ transform: `rotate(${displayDeg}deg)` }}
          >
            <div className="needle-arrow" />
          </div>
        </div>
        <div className="compass-center-text">
          <Readout
            value={formatWind(current.windAvg, unit)}
            qualifier={`${compassDir} (${current.windDirection}°)`}
          />
        </div>
      </div>

      <StatRow divider minColumn={110}>
        <Stat label="Lull" value={formatWind(current.windLull, unit)} />
        <Stat label="Gust" value={formatWind(current.windGust, unit)} />
      </StatRow>
    </GlassCard>
  );
}

export const WindCard = memo(WindCardImpl);
