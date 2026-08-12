import type { StationMeta, StationStatus } from '../types/weather';
import { formatCoord, hasCoordinates } from './formatCoord';

interface HeaderProps {
  station: StationMeta | null;
  status: StationStatus | null;
  lastUpdated: Date | null;
  // True when the core observation's last refresh attempt failed and the
  // data shown is held over from an earlier successful fetch (§14 P1.6).
  // Optional so existing callers/tests that don't care about staleness
  // don't need to pass it; treated as "not stale" when omitted.
  isStale?: boolean;
  onSettingsClick: () => void;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export function Header({ station, status, lastUpdated, isStale, onSettingsClick }: HeaderProps) {
  return (
    <header className="app-header">
      <div className="header-left">
        <h1 className="station-name">
          <span className="logo-icon">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
            </svg>
          </span>
          {station?.name ?? 'Tempest Station'}
        </h1>
        {hasCoordinates(station) && (
          <span className="station-location">
            {formatCoord(station.latitude, station.longitude)}
            {/* hasCoordinates validates lat/lon only -- a response carrying
                coordinates but no elevation would otherwise render "undefinedm". */}
            &middot; {station.elevation ?? '—'}m
          </span>
        )}
      </div>
      <div className="header-right">
        {status && (
          <div className={`status-badge ${status.isOnline ? 'online' : 'offline'}`}>
            <span className="status-dot" />
            {status.isOnline ? 'Live' : 'Offline'}
          </div>
        )}
        {lastUpdated && (
          <span
            className={`last-updated${isStale ? ' stale' : ''}`}
            title={isStale ? 'Live data unavailable; showing the last successful update' : undefined}
          >
            Updated {formatTime(lastUpdated)}
            {isStale && (
              <span className="stale-badge" role="status">
                Stale
              </span>
            )}
          </span>
        )}
        <button className="settings-btn" onClick={onSettingsClick} aria-label="Settings">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="3" />
            <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
          </svg>
        </button>
      </div>
    </header>
  );
}
