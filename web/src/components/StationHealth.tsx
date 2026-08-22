import { memo } from 'react';
import type { StationStatus } from '../types/weather';
import { GlassCard } from './GlassCard';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface StationHealthProps {
  status: StationStatus;
}

function batteryLevel(volts: number): { pct: number; label: string } {
  // Tempest LTO battery: ~2.8V full, ~2.2V low
  const pct = Math.min(100, Math.max(0, ((volts - 2.2) / 0.6) * 100));
  let label = 'Good';
  if (pct < 20) label = 'Low';
  else if (pct < 50) label = 'Fair';
  return { pct, label };
}

function signalBars(strength: number): number[] {
  return [1, 2, 3, 4].map((n) => (n <= strength ? 1 : 0));
}

function timeSince(epochSeconds: number): string {
  const diff = Math.floor(Date.now() / 1000 - epochSeconds);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

function StationHealthImpl({ status }: StationHealthProps) {
  const battery = batteryLevel(status.batteryLevel);
  const bars = signalBars(status.signalStrength);

  return (
    <GlassCard className="station-health-card">
      <div className="card-header">
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <rect x="1" y="6" width="18" height="12" rx="2" ry="2" />
          <line x1="23" y1="10" x2="23" y2="14" />
        </svg>
        <span className="card-title">Station Health</span>
      </div>

      <StatRow>
        <Stat
          label="Battery"
          value={
            <div className="battery-display">
              <div className="battery-bar">
                <div
                  className="battery-fill"
                  style={{
                    width: `${battery.pct}%`,
                    backgroundColor: battery.pct < 20 ? 'var(--danger-color)' :
                      battery.pct < 50 ? 'var(--warning-color)' : 'var(--success-color)',
                  }}
                />
              </div>
              <span className="battery-text">{status.batteryLevel.toFixed(2)}V &middot; {battery.label}</span>
            </div>
          }
        />
        <Stat
          label="Signal"
          value={
            <div className="signal-display">
              <div className="signal-bars">
                {bars.map((active, i) => (
                  <div
                    key={i}
                    className={`signal-bar ${active ? 'active' : ''}`}
                    style={{ height: `${(i + 1) * 6}px` }}
                  />
                ))}
              </div>
              <span className="signal-text">{status.signalStrength}/4</span>
            </div>
          }
        />
        <Stat label="Last Report" value={timeSince(status.lastReport)} />
        <Stat label="Firmware" value={status.firmwareVersion} />
      </StatRow>
    </GlassCard>
  );
}

export const StationHealth = memo(StationHealthImpl);
