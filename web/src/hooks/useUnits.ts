import { useState, useCallback } from 'react';
import type {
  TemperatureUnit,
  WindUnit,
  PressureUnit,
  DistanceUnit,
  RainUnit,
  UserPreferences,
} from '../types/weather';

const DEFAULT_PREFS: UserPreferences = {
  temperatureUnit: 'F',
  windUnit: 'mph',
  pressureUnit: 'mb',
  rainUnit: 'in',
  distanceUnit: 'mi',
  theme: 'nord',
  recordsWindowDays: 7,
};

function loadPrefs(): UserPreferences {
  try {
    const stored = localStorage.getItem('tempest-prefs');
    if (stored) return { ...DEFAULT_PREFS, ...JSON.parse(stored) };
  } catch { /* ignore */ }
  return DEFAULT_PREFS;
}

function savePrefs(prefs: UserPreferences) {
  localStorage.setItem('tempest-prefs', JSON.stringify(prefs));
}

// --- Conversion helpers ---

export function convertTemp(celsius: number, unit: TemperatureUnit): number {
  if (unit === 'F') return celsius * 9 / 5 + 32;
  return celsius;
}

export function convertWind(ms: number, unit: WindUnit): number {
  switch (unit) {
    case 'mph': return ms * 2.23694;
    case 'kph': return ms * 3.6;
    case 'kts': return ms * 1.94384;
    default: return ms;
  }
}

export function convertPressure(mb: number, unit: PressureUnit): number {
  switch (unit) {
    case 'inHg': return mb * 0.02953;
    case 'hPa': return mb; // 1 mb = 1 hPa
    default: return mb;
  }
}

// 1 statute mile = 1.609344 km exactly (international definition).
const KM_PER_MILE = 1.609344;

export function convertDistance(km: number, unit: DistanceUnit): number {
  if (unit === 'mi') return km / KM_PER_MILE;
  return km;
}

/**
 * `decimals` exists for the range labels, which read as round numbers ("40 km",
 * "25 mi") rather than as measurements. Everything else takes the default 1.
 */
export function formatDistance(km: number, unit: DistanceUnit, decimals = 1): string {
  return `${convertDistance(km, unit).toFixed(decimals)} ${unit}`;
}

export function convertRain(mm: number, unit: RainUnit): number {
  if (unit === 'in') return mm * 0.03937;
  return mm;
}

export function formatTemp(celsius: number, unit: TemperatureUnit): string {
  const val = convertTemp(celsius, unit);
  return `${Math.round(val)}°${unit}`;
}

export function formatWind(ms: number, unit: WindUnit): string {
  const val = convertWind(ms, unit);
  return `${val.toFixed(1)} ${unit}`;
}

export function formatPressure(mb: number, unit: PressureUnit): string {
  const val = convertPressure(mb, unit);
  if (unit === 'inHg') return `${val.toFixed(2)} ${unit}`;
  return `${val.toFixed(1)} ${unit}`;
}

export function formatRain(mm: number, unit: RainUnit): string {
  const val = convertRain(mm, unit);
  if (unit === 'in') return `${val.toFixed(2)} in`;
  return `${val.toFixed(1)} mm`;
}

export function useUnits() {
  const [prefs, setPrefsState] = useState<UserPreferences>(loadPrefs);

  const setPrefs = useCallback((update: Partial<UserPreferences>) => {
    setPrefsState((prev) => {
      const next = { ...prev, ...update };
      savePrefs(next);
      return next;
    });
  }, []);

  return { prefs, setPrefs };
}
