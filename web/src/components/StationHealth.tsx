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

// The em-dash the almanac already uses for "not reported", so absent data
// reads the same way everywhere on the dashboard.
const EM_DASH = '—';

// null in, null out: with no signal source the card must say "not reported"
// #196 replaced the 0-4 bar display with the raw dBm reading. WeatherFlow
// publishes no dBm-to-bars mapping, so drawing bars would have meant inventing
// thresholds and presenting a guess as a measurement. signalBars is gone
// rather than fed a unit it was never written for.
function signalText(dbm: number | null | undefined): string {
  // == null catches undefined too. The value comes from a JSON response, so a
  // server that predates #196 (or any payload that simply omits the key)
  // must render the em-dash rather than the string "undefined dBm".
  if (dbm == null) return EM_DASH;
  return `${dbm} dBm`;
}

function timeSince(epochSeconds: number): string {
  const diff = Math.floor(Date.now() / 1000 - epochSeconds);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

function StationHealthImpl({ status }: StationHealthProps) {
  const battery = batteryLevel(status.batteryLevel);

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
        <Stat label="Signal" value={signalText(status.signalDbm)} />
        <Stat label="Last Report" value={timeSince(status.lastReport)} />
        <Stat label="Firmware" value={status.firmwareVersion ?? EM_DASH} />
      </StatRow>
    </GlassCard>
  );
}

export const StationHealth = memo(StationHealthImpl);
