import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { RecordsCard } from './RecordsCard';
import type { RecordsSummary, UserPreferences } from '../types/weather';

const prefs: UserPreferences = {
  temperatureUnit: 'F',
  windUnit: 'mph',
  pressureUnit: 'inHg',
  rainUnit: 'in',
  distanceUnit: 'mi',
  theme: 'nord',
  recordsWindowDays: 7,
};

const fullSummary: RecordsSummary = {
  window: { days: 7, from: 1700000000, to: 1700600000 },
  count: 42,
  coveredFrom: 1700000000,
  coveredTo: 1700600000,
  temperature: { max: 30, min: 5 },
  humidity: { max: 90, min: 20 },
  pressure: { max: 1020, min: 995 },
  windMax: 12,
  gustMax: 20,
  rainTotal: 15,
  lightningTotal: 3,
};

describe('RecordsCard', () => {
  it('renders tile labels and formatted values for a full summary', () => {
    render(<RecordsCard summary={fullSummary} prefs={prefs} />);

    expect(screen.getByText('Last 7 days')).toBeInTheDocument();
    expect(screen.getAllByText('High').length).toBe(3);
    expect(screen.getAllByText('Low').length).toBe(3);
    expect(screen.getByText('Sustained')).toBeInTheDocument();
    expect(screen.getByText('Gust')).toBeInTheDocument();
    // 30C -> 86F (formatTemp rounds and appends unit)
    expect(screen.getByText('86°F')).toBeInTheDocument();
    // Lightning is a raw integer count with a "strikes" unit label
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('strikes')).toBeInTheDocument();
  });

  it('renders em-dashes when count is 0, even if aggregates are non-null', () => {
    const emptySummary: RecordsSummary = { ...fullSummary, count: 0 };
    render(<RecordsCard summary={emptySummary} prefs={prefs} />);

    // 6 tiles, each with at least one value slot -> every value shows an em-dash
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(8);
    expect(screen.queryByText('86°F')).not.toBeInTheDocument();
  });

  it('renders em-dashes for individually-null aggregates', () => {
    const nullAggSummary: RecordsSummary = {
      ...fullSummary,
      count: 1,
      temperature: { max: null, min: null },
      windMax: null,
      gustMax: null,
      rainTotal: null,
      lightningTotal: null,
    };
    render(<RecordsCard summary={nullAggSummary} prefs={prefs} />);

    // temperature high/low, wind sustained/gust, rain, lightning = 6 em-dashes
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(6);
    // humidity/pressure were left non-null, so they should still render
    expect(screen.getByText('90%')).toBeInTheDocument();
    // a null lightning count shows an em-dash, not "— strikes"
    expect(screen.queryByText('strikes')).not.toBeInTheDocument();
  });

  it('renders a safe placeholder when summary is null, without crashing', () => {
    render(<RecordsCard summary={null} prefs={prefs} />);

    expect(screen.getByText('Last 7 days')).toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});
