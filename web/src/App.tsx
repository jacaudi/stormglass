import { useState, useEffect } from 'react';
import { useWeatherData } from './hooks/useWeatherData';
import { useUnits } from './hooks/useUnits';
import { applyTheme } from './themes/themes';
import { Header } from './components/Header';
import { TemperatureHero } from './components/TemperatureHero';
import { WindCard } from './components/WindCard';
import { PressureCard } from './components/PressureCard';
import { HumidityCard } from './components/HumidityCard';
import { SolarUVCard } from './components/SolarUVCard';
import { RainCard } from './components/RainCard';
import { LightningCard } from './components/LightningCard';
import { RecordsCard } from './components/RecordsCard';
import { ForecastStrip } from './components/ForecastStrip';
import { StationHealth } from './components/StationHealth';
import { AlmanacCard } from './components/AlmanacCard';
import { RadarCard } from './components/RadarCard';
import { SettingsPanel } from './components/SettingsPanel';
import { ErrorBoundary } from './components/ErrorBoundary';
import { hasCoordinates } from './components/formatCoord';
import type { ThemeName } from './types/weather';
import './App.css';

function App() {
  const { prefs, setPrefs } = useUnits();
  const { station, current, forecast, status, almanac, isLoading, error, lastUpdated, isStale, refresh, summary, capabilities } =
    useWeatherData(undefined, prefs.recordsWindowDays);
  const [settingsOpen, setSettingsOpen] = useState(false);

  useEffect(() => {
    applyTheme(prefs.theme as ThemeName);
  }, [prefs.theme]);

  // Only show the loading screen for an INITIAL load (no data yet). `loadData`
  // re-runs whenever a capability flips — e.g. a failed capability fetch that a
  // later poll tick retries successfully — and that must not bounce an already
  // rendered dashboard back to "Connecting to station...".
  if (isLoading && !current) {
    return (
      <div className="loading-screen">
        <div className="loading-spinner" />
        <p>Connecting to station...</p>
      </div>
    );
  }

  // Only blank the dashboard for an INITIAL failure (no prior data yet). If a
  // refresh fails but we already have `current` data, keep rendering the
  // dashboard with what we have — the Header's `lastUpdated` conveys staleness.
  if (error && !current) {
    return (
      <div className="error-screen">
        <h2>Connection Error</h2>
        <p>{error}</p>
        <button className="glass-btn" onClick={refresh}>Retry</button>
      </div>
    );
  }

  if (!current) return null;

  return (
    <div className="app">
      <div className="app-bg-orbs">
        <div className="orb orb-1" />
        <div className="orb orb-2" />
        <div className="orb orb-3" />
      </div>

      <Header
        station={station}
        status={status}
        lastUpdated={lastUpdated}
        isStale={isStale}
        onSettingsClick={() => setSettingsOpen(true)}
      />

      <ErrorBoundary>
        <main className="dashboard">
          <div className="dashboard-grid">
            <TemperatureHero current={current} unit={prefs.temperatureUnit} precipProbability={forecast[0]?.precipProbability} />
            <WindCard current={current} unit={prefs.windUnit} />
            <HumidityCard current={current} tempUnit={prefs.temperatureUnit} />
            <PressureCard current={current} unit={prefs.pressureUnit} />
            <SolarUVCard current={current} />
            <RainCard current={current} unit={prefs.rainUnit} />
            <LightningCard current={current} />
            {status && <StationHealth status={status} />}
            <RecordsCard summary={summary} prefs={prefs} />
            {capabilities?.forecast && <ForecastStrip forecast={forecast} unit={prefs.temperatureUnit} />}
            {capabilities?.almanac && almanac && <AlmanacCard almanac={almanac} unit={prefs.temperatureUnit} />}
            {capabilities?.radar && hasCoordinates(station) && (
              // Own ErrorBoundary (in addition to RadarCard's internal
              // try/catch around MapLibre init) -- belt-and-suspenders so a
              // WebGL/MapLibre failure can never blank the whole dashboard
              // grid, which shares a single outer ErrorBoundary.
              <ErrorBoundary>
                {/* No client-side site table/nearest-site lookup yet
                    (follow-up, belongs with the DOC.1 extract-sidecar work)
                    -- RadarCard degrades to "not configured" until one exists. */}
                <RadarCard station={station} />
              </ErrorBoundary>
            )}
          </div>
        </main>
      </ErrorBoundary>

      <SettingsPanel
        isOpen={settingsOpen}
        prefs={prefs}
        onPrefsChange={setPrefs}
        onClose={() => setSettingsOpen(false)}
      />
    </div>
  );
}

export default App;
