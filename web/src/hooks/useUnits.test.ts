import { describe, it, expect } from 'vitest';
import {
  convertTemp,
  convertWind,
  convertPressure,
  convertDistance,
  convertRain,
  formatTemp,
  formatWind,
  formatPressure,
  formatDistance,
  formatRain,
} from './useUnits';

describe('convertTemp', () => {
  it('passes Celsius through unchanged for unit C', () => {
    expect(convertTemp(20, 'C')).toBe(20);
  });

  it('converts Celsius to Fahrenheit for unit F', () => {
    expect(convertTemp(0, 'F')).toBeCloseTo(32);
    expect(convertTemp(100, 'F')).toBeCloseTo(212);
  });
});

describe('convertWind', () => {
  it('passes m/s through unchanged for unit ms', () => {
    expect(convertWind(10, 'ms')).toBe(10);
  });

  it('converts m/s to mph', () => {
    expect(convertWind(10, 'mph')).toBeCloseTo(22.3694);
  });

  it('converts m/s to kph', () => {
    expect(convertWind(10, 'kph')).toBeCloseTo(36);
  });

  it('converts m/s to knots', () => {
    expect(convertWind(10, 'kts')).toBeCloseTo(19.4384);
  });
});

describe('convertPressure', () => {
  it('passes mb through unchanged for unit mb', () => {
    expect(convertPressure(1013, 'mb')).toBe(1013);
  });

  it('converts mb to inHg', () => {
    expect(convertPressure(1013, 'inHg')).toBeCloseTo(29.91, 1);
  });

  it('treats hPa as numerically equal to mb', () => {
    expect(convertPressure(1013, 'hPa')).toBe(1013);
  });
});

describe('convertRain', () => {
  it('passes mm through unchanged for unit mm', () => {
    expect(convertRain(25, 'mm')).toBe(25);
  });

  it('converts mm to inches', () => {
    expect(convertRain(25.4, 'in')).toBeCloseTo(1, 1);
  });
});

describe('formatTemp', () => {
  it('rounds and appends the unit suffix', () => {
    expect(formatTemp(20.6, 'C')).toBe('21°C');
    expect(formatTemp(0, 'F')).toBe('32°F');
  });
});

describe('formatWind', () => {
  it('formats to one decimal with a unit suffix', () => {
    expect(formatWind(10, 'ms')).toBe('10.0 ms');
    expect(formatWind(10, 'mph')).toBe('22.4 mph');
  });
});

describe('formatPressure', () => {
  it('formats inHg to two decimals', () => {
    expect(formatPressure(1013, 'inHg')).toBe('29.91 inHg');
  });

  it('formats mb/hPa to one decimal', () => {
    expect(formatPressure(1013, 'mb')).toBe('1013.0 mb');
    expect(formatPressure(1013, 'hPa')).toBe('1013.0 hPa');
  });
});

describe('formatRain', () => {
  it('formats inches to two decimals', () => {
    expect(formatRain(25.4, 'in')).toBe('1.00 in');
  });

  it('formats mm to one decimal', () => {
    expect(formatRain(25.4, 'mm')).toBe('25.4 mm');
  });
});

// The Tempest reports strike distance in km and the sensor's own range is a
// round 40 km, so km stays the storage unit and the preference converts on the
// way out -- the same shape as every other unit here.
describe('convertDistance', () => {
  it('is identity for km', () => {
    expect(convertDistance(8.05, 'km')).toBe(8.05);
  });

  it('converts km to statute miles', () => {
    expect(convertDistance(8.0467, 'mi')).toBeCloseTo(5, 3);
    expect(convertDistance(40, 'mi')).toBeCloseTo(24.855, 3);
  });
});

describe('formatDistance', () => {
  it('renders one decimal in either unit', () => {
    expect(formatDistance(8.05, 'km')).toBe('8.1 km');
    expect(formatDistance(8.05, 'mi')).toBe('5.0 mi');
  });

  it('rounds to whole units when asked, for the range labels', () => {
    expect(formatDistance(40, 'km', 0)).toBe('40 km');
    expect(formatDistance(40, 'mi', 0)).toBe('25 mi');
  });
});
