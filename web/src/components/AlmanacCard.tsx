import { memo } from 'react';
import type { StationAlmanac, TemperatureUnit } from '../types/weather';
import { formatTemp } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Stat } from './primitives/Stat';

interface AlmanacCardProps {
  almanac: StationAlmanac;
  unit: TemperatureUnit;
}

/* ---- Moon phase SVG ---- */
function MoonPhase({ phase, size = 52 }: { phase: number; size?: number }) {
  const r  = Math.floor(size / 2) - 2;
  const c  = Math.floor(size / 2);
  const DARK   = 'rgba(10,10,46,0.85)';
  const LIT    = '#ddeeff';
  const BORDER = 'rgba(180,210,255,0.35)';

  const isWaxing = phase <= 0.5;
  const angle = phase * Math.PI * 2;
  // terminatorXR > 0 → dark ellipse (crescent); < 0 → lit ellipse (gibbous)
  const terminatorXR = r * Math.cos(angle);

  // Right or left semicircle path (lit side)
  const semiPath = isWaxing
    ? `M ${c} ${c - r} A ${r} ${r} 0 0 1 ${c} ${c + r} L ${c} ${c - r} Z`
    : `M ${c} ${c - r} A ${r} ${r} 0 0 0 ${c} ${c + r} L ${c} ${c - r} Z`;

  const showEllipse = Math.abs(terminatorXR) > 1;

  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      {/* Dark base */}
      <circle cx={c} cy={c} r={r} fill={DARK} stroke={BORDER} strokeWidth="1.5" />
      {/* Lit semicircle */}
      <path d={semiPath} fill={LIT} />
      {/* Terminator ellipse */}
      {showEllipse && (
        <ellipse
          cx={c} cy={c}
          rx={Math.abs(terminatorXR)}
          ry={r}
          fill={terminatorXR > 0 ? DARK : LIT}
        />
      )}
      {/* Outer ring for definition */}
      <circle cx={c} cy={c} r={r} fill="none" stroke={BORDER} strokeWidth="1.5" />
    </svg>
  );
}

/** Rendered in place of any value the station cannot supply. */
const EM_DASH = '—';

/* ---- Individual record column ---- */
interface RecordColumnProps {
  label: string;
  high: number | null;
  highDate: string | null;
  low: number | null;
  lowDate: string | null;
  unit: TemperatureUnit;
}

function RecordColumn({ label, high, highDate, low, lowDate, unit }: RecordColumnProps) {
  return (
    <div className="almanac-record">
      <span className="almanac-period">{label}</span>
      <Stat
        label="H ↑"
        value={high === null ? EM_DASH : formatTemp(high, unit)}
        sublabel={highDate ?? EM_DASH}
        className="almanac-hl-high"
        valueClassName="almanac-temp-high"
      />
      <div className="almanac-divider" />
      <Stat
        label="L ↓"
        value={low === null ? EM_DASH : formatTemp(low, unit)}
        sublabel={lowDate ?? EM_DASH}
        className="almanac-hl-low"
        valueClassName="almanac-temp-low"
      />
    </div>
  );
}

/* ---- Daylight duration ---- */
// A pure h/m split of the server's integer, not an epoch subtraction: the
// server sends daylightMinutes precisely so the client never does timezone
// arithmetic.
function daylightDuration(totalMin: number): string {
  const h = Math.floor(totalMin / 60);
  const m = String(totalMin % 60).padStart(2, '0');
  return `${h}h ${m}m daylight`;
}

/* ---- Main card ---- */
function AlmanacCardImpl({ almanac, unit }: AlmanacCardProps) {
  return (
    <GlassCard span={3}>
      <div className="card-header">
        {/* Book / almanac icon */}
        <svg className="card-icon" width="20" height="20" viewBox="0 0 24 24" fill="none"
          stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" />
          <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
        </svg>
        <span className="card-title">Station Almanac</span>
      </div>

      {/* ---- Astronomical row ---- */}
      <div className="almanac-astro">
        {/* Sunrise */}
        <div className="almanac-sun">
          {/* Sun above horizon + upward arrow */}
          <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
            <circle cx="20" cy="15" r="7" fill="#fbbf24" />
            <line x1="20" y1="4"  x2="20" y2="2"  stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <line x1="29" y1="7"  x2="30" y2="5"  stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <line x1="11" y1="7"  x2="10" y2="5"  stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <line x1="33" y1="15" x2="35" y2="15" stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <line x1="7"  y1="15" x2="5"  y2="15" stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <line x1="2"  y1="26" x2="38" y2="26" stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" />
            <polyline points="14,37 20,30 26,37" stroke="#fbbf24" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
          </svg>
          <div className="almanac-sun-info">
            <span className="almanac-sun-label">Sunrise</span>
            <span className="almanac-sun-time">{almanac.sunrise ?? EM_DASH}</span>
          </div>
        </div>

        {/* Moon phase */}
        <div className="almanac-moon">
          <MoonPhase phase={almanac.moonPhase} size={80} />
          <div className="almanac-moon-info">
            <span className="almanac-moon-name">{almanac.moonPhaseName}</span>
            <span className="almanac-moon-illum">
              {Math.round(almanac.moonIllumination * 100)}% illuminated
            </span>
          </div>
        </div>

        {/* Sunset */}
        <div className="almanac-sun almanac-sun-right">
          <div className="almanac-sun-info almanac-sun-info-right">
            <span className="almanac-sun-label">Sunset</span>
            <span className="almanac-sun-time">{almanac.sunset ?? EM_DASH}</span>
          </div>
          {/* Sun above horizon + downward arrow */}
          <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
            <circle cx="20" cy="15" r="7" fill="#f97316" />
            <line x1="20" y1="4"  x2="20" y2="2"  stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <line x1="29" y1="7"  x2="30" y2="5"  stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <line x1="11" y1="7"  x2="10" y2="5"  stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <line x1="33" y1="15" x2="35" y2="15" stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <line x1="7"  y1="15" x2="5"  y2="15" stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <line x1="2"  y1="26" x2="38" y2="26" stroke="#f97316" strokeWidth="2" strokeLinecap="round" />
            <polyline points="14,30 20,37 26,30" stroke="#f97316" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
          </svg>
        </div>
      </div>

      {almanac.daylightMinutes !== null && (
        <div className="almanac-daylight-row">
          {daylightDuration(almanac.daylightMinutes)}
        </div>
      )}

      <div className="almanac-section-divider" />

      {/* ---- Temperature records row ---- */}
      <div className="almanac-records">
        <RecordColumn label="Today"      {...almanac.today} unit={unit} />
        <RecordColumn label="This Week"  {...almanac.week}  unit={unit} />
        <RecordColumn label="This Month" {...almanac.month} unit={unit} />
        <RecordColumn label="This Year"  {...almanac.year}  unit={unit} />
      </div>
    </GlassCard>
  );
}

export const AlmanacCard = memo(AlmanacCardImpl);
