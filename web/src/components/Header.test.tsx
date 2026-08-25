import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { formatCoord } from './formatCoord';
import { Header } from './Header';
import type { StationMeta } from '../types/weather';

describe('formatCoord', () => {
  it('renders a northern, western station as N/W', () => {
    expect(formatCoord(40.7128, -74.006)).toBe('40.7128°N, 74.0060°W');
  });

  it('renders a southern, eastern station as S/E', () => {
    expect(formatCoord(-33.8688, 151.2093)).toBe('33.8688°S, 151.2093°E');
  });

  it('renders a southern, western station as S/W', () => {
    expect(formatCoord(-15.7801, -47.9292)).toBe('15.7801°S, 47.9292°W');
  });

  it('treats zero latitude/longitude as N/E', () => {
    expect(formatCoord(0, 0)).toBe('0.0000°N, 0.0000°E');
  });
});

describe('Header stale indicator (M6a)', () => {
  const baseProps = {
    station: null,
    status: null,
    lastUpdated: new Date(2026, 0, 1, 12, 30),
    onSettingsClick: () => {},
  };

  it('renders a stale indicator when isStale is true', () => {
    render(<Header {...baseProps} isStale={true} />);
    expect(screen.getByText(/stale/i)).toBeInTheDocument();
  });

  it('does not render a stale indicator when isStale is false', () => {
    render(<Header {...baseProps} isStale={false} />);
    expect(screen.queryByText(/stale/i)).not.toBeInTheDocument();
  });
});

describe('Header station location guard', () => {
  it('renders no location line when the station has no usable coordinates', () => {
    // /api/station answers an empty bearer with a 200 and WeatherFlow's status
    // envelope, so `station` is truthy but has no fields. Rendering it produced
    // "NaN°N, NaN°E · m".
    const envelopeOnly = { status: { status_code: 0 } } as unknown as StationMeta;

    render(
      <Header
        station={envelopeOnly}
        status={null}
        lastUpdated={null}
        onSettingsClick={() => {}}
      />
    );

    expect(screen.queryByText(/NaN/)).toBeNull();
    expect(document.querySelector('.station-location')).toBeNull();
  });

  it('renders a placeholder rather than "undefinedm" when only elevation is missing', () => {
    // hasCoordinates validates latitude/longitude only, so a response with
    // coordinates but no elevation still reaches the elevation span. React
    // renders `undefined` as nothing (not the string "undefined"), so a
    // not-toContain('undefined') assertion here could never fail -- it
    // asserts a positive presence of the `?? '—'` fallback instead.
    const noElevation = {
      station_id: 1,
      name: 'Test',
      latitude: 35.4676,
      longitude: -97.5164,
    } as unknown as StationMeta;

    render(
      <Header
        station={noElevation}
        status={null}
        lastUpdated={null}
        onSettingsClick={() => {}}
      />
    );

    expect(document.querySelector('.station-location')?.textContent).toContain('—m');
  });

  it('renders the location line for a station with real coordinates', () => {
    const station = {
      name: 'Test',
      latitude: 35.4676,
      longitude: -97.5164,
      elevation: 361,
    } satisfies StationMeta;

    render(
      <Header
        station={station}
        status={null}
        lastUpdated={null}
        onSettingsClick={() => {}}
      />
    );

    expect(document.querySelector('.station-location')).not.toBeNull();
    expect(screen.queryByText(/NaN/)).toBeNull();
  });
});

describe('Header fallbacks on omitted station fields', () => {
  it('renders the default name when the server omits name', () => {
    render(<Header station={{ latitude: 35.4, longitude: -97.6 }} status={null} lastUpdated={null} onSettingsClick={() => {}} />);
    expect(screen.getByText('Tempest Station')).toBeInTheDocument();
  });

  it('renders an em-dash for the elevation when the server omits it', () => {
    render(<Header station={{ latitude: 35.4, longitude: -97.6 }} status={null} lastUpdated={null} onSettingsClick={() => {}} />);
    expect(screen.getByText(/—m/)).toBeInTheDocument();
  });

  // This is the failure the server's omit-don't-zero rule exists to prevent:
  // hasCoordinates accepts 0 as finite, so zero-valued coordinates would
  // render a real-looking location for an unconfigured station.
  it('renders no location line at all when coordinates are omitted', () => {
    const { container } = render(
      <Header station={{ name: 'Backyard' }} status={null} lastUpdated={null} onSettingsClick={() => {}} />
    );
    expect(container.querySelector('.station-location')).toBeNull();
    expect(screen.queryByText(/0\.0000°N/)).not.toBeInTheDocument();
  });

  it('puts a space before the coordinate/elevation separator', () => {
    // JSX drops the newline-plus-indent around a comment-only line, so the
    // separator rendered as "122.2043°W· 150m" until an explicit {' '} was
    // added. Asserted on textContent because it is a whitespace defect that no
    // getByText query would notice.
    render(
      <Header
        station={{ name: 'Test', latitude: 35.4676, longitude: -97.5164, elevation: 361 }}
        status={null}
        lastUpdated={null}
        onSettingsClick={() => {}}
      />
    );
    expect(document.querySelector('.station-location')?.textContent).toMatch(/°[WE] ·/);
  });
});
