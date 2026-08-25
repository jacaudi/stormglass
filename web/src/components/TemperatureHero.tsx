import { memo } from 'react';
import type { CurrentObservation, TemperatureUnit } from '../types/weather';
import { formatTemp } from '../hooks/useUnits';
import { GlassCard } from './GlassCard';
import { WeatherIcon } from './WeatherIcon';
import { Readout } from './primitives/Readout';
import { Stat } from './primitives/Stat';
import { StatRow } from './primitives/StatRow';

interface TemperatureHeroProps {
  current: CurrentObservation;
  unit: TemperatureUnit;
  precipProbability?: number;
}

function getConditionLabel(obs: CurrentObservation): string {
  if (obs.lightningStrikeCount > 0) return 'Thunderstorm';
  if (obs.rainAccumulated > 0) return 'Rainy';
  if (obs.solarRadiation > 800) return 'Sunny';
  if (obs.solarRadiation > 400) return 'Partly Cloudy';
  if (obs.solarRadiation > 100) return 'Mostly Cloudy';
  if (obs.solarRadiation > 0) return 'Overcast';
  return 'Clear Night';
}

function getConditionIcon(obs: CurrentObservation): string {
  if (obs.lightningStrikeCount > 0) return 'thunderstorm';
  if (obs.rainAccumulated > 0) return 'rainy';
  if (obs.solarRadiation > 800) return 'clear-day';
  if (obs.solarRadiation > 400) return 'partly-cloudy-day';
  if (obs.solarRadiation > 100) return 'cloudy';
  if (obs.solarRadiation > 0) return 'cloudy';
  return 'clear-night';
}

function TemperatureHeroImpl({ current, unit, precipProbability }: TemperatureHeroProps) {
  const condition = getConditionLabel(current);
  const icon = getConditionIcon(current);

  return (
    <GlassCard className="hero-card" span={2}>
      <div className="hero-content">
        <div className="hero-icon"><WeatherIcon icon={icon} size={72} /></div>
        <div className="hero-temp-block">
          <Readout
            size="hero"
            value={formatTemp(current.airTemperature, unit)}
            qualifier={
              <>
                <span className="hero-condition">{condition}</span>
                <span className="hero-feels-like">
                  Feels like {formatTemp(current.feelsLike, unit)}
                </span>
              </>
            }
          />
        </div>
        <StatRow className="hero-details" minColumn={88}>
          <Stat label="Heat Index" value={formatTemp(current.heatIndex, unit)} />
          <Stat label="Wind Chill" value={formatTemp(current.windChill, unit)} />
          <Stat label="Rain Chance" value={`${Math.round(precipProbability ?? 0)}%`} />
          <Stat label="UV Index" value={current.uvIndex.toFixed(1)} />
        </StatRow>
      </div>
    </GlassCard>
  );
}

export const TemperatureHero = memo(TemperatureHeroImpl);
