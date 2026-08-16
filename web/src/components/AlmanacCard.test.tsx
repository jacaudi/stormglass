import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { AlmanacCard } from './AlmanacCard';
import type { StationAlmanac, TempRecord } from '../types/weather';

const RECORD: TempRecord = { high: 25, highDate: 'Today', low: 10, lowDate: 'Today' };
const EMPTY_RECORD: TempRecord = { high: null, highDate: null, low: null, lowDate: null };

function makeAlmanac(overrides: Partial<StationAlmanac> = {}): StationAlmanac {
  return {
    today: RECORD,
    week: RECORD,
    month: RECORD,
    year: RECORD,
    sunrise: '5:47 AM',
    sunset: '8:17 PM',
    daylightMinutes: 14 * 60 + 30,
    moonPhase: 0.25,
    moonPhaseName: 'First Quarter',
    moonIllumination: 0.42,
    ...overrides,
  };
}

describe('AlmanacCard sunrise/sunset', () => {
  // The server preformats these in the STATION's timezone, because the
  // browser's timezone is the viewer's -- a Colorado station checked from
  // Tokyo would otherwise show "Sunrise 8:47 PM". The card must render the
  // string verbatim and must not reformat it.
  it('renders the server-supplied clock strings verbatim', () => {
    render(<AlmanacCard almanac={makeAlmanac()} unit="F" />);
    expect(screen.getByText('5:47 AM')).toBeInTheDocument();
    expect(screen.getByText('8:17 PM')).toBeInTheDocument();
  });

  it('renders an em-dash for a null sunrise and a null sunset', () => {
    render(<AlmanacCard almanac={makeAlmanac({ sunrise: null, sunset: null, daylightMinutes: null })} unit="F" />);
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(2);
  });

  // Above the Arctic Circle a day can have a sunrise and no sunset: the two
  // events refine at different Julian Days, so the polar guard is evaluated
  // independently for each. The card must handle one bound being null.
  it('renders a half-populated pair with only the missing bound dashed', () => {
    render(<AlmanacCard almanac={makeAlmanac({ sunrise: '10:58 AM', sunset: null, daylightMinutes: null })} unit="F" />);
    expect(screen.getByText('10:58 AM')).toBeInTheDocument();
    expect(screen.getByText('—')).toBeInTheDocument();
  });
});

describe('AlmanacCard daylight duration', () => {
  it('splits the server-supplied minute count into hours and minutes', () => {
    render(<AlmanacCard almanac={makeAlmanac()} unit="F" />);
    expect(screen.getByText('14h 30m daylight')).toBeInTheDocument();
  });

  it('zero-pads the minutes', () => {
    render(<AlmanacCard almanac={makeAlmanac({ daylightMinutes: 9 * 60 + 5 })} unit="F" />);
    expect(screen.getByText('9h 05m daylight')).toBeInTheDocument();
  });

  it('suppresses the daylight line entirely when daylightMinutes is null', () => {
    render(<AlmanacCard almanac={makeAlmanac({ sunrise: null, sunset: null, daylightMinutes: null })} unit="F" />);
    expect(screen.queryByText(/daylight/)).not.toBeInTheDocument();
  });
});

describe('AlmanacCard temperature records', () => {
  it('renders values and labels when the window has data', () => {
    render(<AlmanacCard almanac={makeAlmanac()} unit="C" />);
    expect(screen.getAllByText('25°C').length).toBeGreaterThan(0);
  });

  // A freshly provisioned appliance has no year of history. It must render
  // em-dashes, never NaN°C.
  it('renders em-dashes for an empty window instead of NaN', () => {
    render(<AlmanacCard almanac={makeAlmanac({ year: EMPTY_RECORD })} unit="C" />);
    expect(screen.queryByText(/NaN/)).not.toBeInTheDocument();
  });

  it('renders em-dashes for every column when the store is empty', () => {
    render(
      <AlmanacCard
        almanac={makeAlmanac({ today: EMPTY_RECORD, week: EMPTY_RECORD, month: EMPTY_RECORD, year: EMPTY_RECORD })}
        unit="C"
      />
    );
    expect(screen.queryByText(/NaN/)).not.toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(8);
  });
});

describe('AlmanacCard moon phase', () => {
  it('renders the moon phase name and rounded illumination percentage', () => {
    render(<AlmanacCard almanac={makeAlmanac({ moonPhaseName: 'Waxing Gibbous', moonIllumination: 0.678 })} unit="F" />);
    expect(screen.getByText('Waxing Gibbous')).toBeInTheDocument();
    expect(screen.getByText('68% illuminated')).toBeInTheDocument();
  });
});
