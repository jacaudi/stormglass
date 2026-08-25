import { memo } from 'react';
import type { RecordsSummary, UserPreferences } from '../types/weather';
import { formatTemp, formatWind, formatPressure, formatRain } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { Badge } from './primitives/Badge';
import { Stat } from './primitives/Stat';

interface RecordsCardProps {
  summary: RecordsSummary | null;
  prefs: UserPreferences;
}

const EM_DASH = '—';

function fmt(value: number | null, formatter: (v: number) => string): string {
  return value === null ? EM_DASH : formatter(value);
}

function RecordsCardImpl({ summary, prefs }: RecordsCardProps) {
  const windowDays = summary?.window.days ?? prefs.recordsWindowDays;

  // A window with no observations (or not yet loaded) renders every aggregate
  // as an em-dash, regardless of what the individual fields happen to hold.
  const isEmpty = summary === null || summary.count === 0;
  const s = isEmpty ? null : summary;

  const tempMax = s?.temperature.max ?? null;
  const tempMin = s?.temperature.min ?? null;
  const humidityMax = s?.humidity.max ?? null;
  const humidityMin = s?.humidity.min ?? null;
  const pressureMax = s?.pressure.max ?? null;
  const pressureMin = s?.pressure.min ?? null;
  const windMax = s?.windMax ?? null;
  const gustMax = s?.gustMax ?? null;
  const rainTotal = s?.rainTotal ?? null;
  const lightningTotal = s?.lightningTotal ?? null;

  return (
    <GlassCard className="records-card">
      <div className="records-header">
        <span className="records-title">Records</span>
        <Badge pill className="records-window-slot">Last {windowDays} days</Badge>
      </div>

      <div className="records-grid">
        <div className="rstat">
          <span className="stat-label">Temperature</span>
          <div className="rstat-body rstat-pair">
            <div className="rpair-row">
              <span className="rpair-tag">High</span>
              <span className="rpair-val">{fmt(tempMax, (v) => formatTemp(v, prefs.temperatureUnit))}</span>
            </div>
            <div className="rpair-row">
              <span className="rpair-tag">Low</span>
              <span className="rpair-val">{fmt(tempMin, (v) => formatTemp(v, prefs.temperatureUnit))}</span>
            </div>
          </div>
        </div>

        <div className="rstat">
          <span className="stat-label">Humidity</span>
          <div className="rstat-body rstat-pair">
            <div className="rpair-row">
              <span className="rpair-tag">High</span>
              <span className="rpair-val">{fmt(humidityMax, (v) => `${Math.round(v)}%`)}</span>
            </div>
            <div className="rpair-row">
              <span className="rpair-tag">Low</span>
              <span className="rpair-val">{fmt(humidityMin, (v) => `${Math.round(v)}%`)}</span>
            </div>
          </div>
        </div>

        <div className="rstat">
          <span className="stat-label">Pressure</span>
          <div className="rstat-body rstat-pair">
            <div className="rpair-row">
              <span className="rpair-tag">High</span>
              <span className="rpair-val">{fmt(pressureMax, (v) => formatPressure(v, prefs.pressureUnit))}</span>
            </div>
            <div className="rpair-row">
              <span className="rpair-tag">Low</span>
              <span className="rpair-val">{fmt(pressureMin, (v) => formatPressure(v, prefs.pressureUnit))}</span>
            </div>
          </div>
        </div>

        <div className="rstat">
          <span className="stat-label">Wind</span>
          <div className="rstat-body rstat-pair">
            <div className="rpair-row">
              <span className="rpair-tag">Sustained</span>
              <span className="rpair-val">{fmt(windMax, (v) => formatWind(v, prefs.windUnit))}</span>
            </div>
            <div className="rpair-row">
              <span className="rpair-tag">Gust</span>
              <span className="rpair-val">{fmt(gustMax, (v) => formatWind(v, prefs.windUnit))}</span>
            </div>
          </div>
        </div>

        <div className="rstat">
          <Stat label="Rain" value={fmt(rainTotal, (v) => formatRain(v, prefs.rainUnit))} />
        </div>

        <div className="rstat">
          <Stat
            label="Lightning"
            value={
              <>
                {fmt(lightningTotal, (v) => `${v}`)}
                {lightningTotal !== null && <span className="rstat-unit">strikes</span>}
              </>
            }
          />
        </div>
      </div>
    </GlassCard>
  );
}

export const RecordsCard = memo(RecordsCardImpl);
