/** Tempest Weather Station data types based on the WeatherFlow API */

/**
 * Station identity (GET /api/station), served entirely from this server's
 * own configuration -- not from WeatherFlow. Every field is optional
 * because the server OMITS an unset value rather than emitting a zero:
 * an emitted "" would defeat the name fallback below (`??` does not fire on
 * an empty string) and emitted zero coordinates would put every default
 * deployment at 0.0000°N, 0.0000°E.
 *
 * Five fields the old WeatherFlow passthrough declared are gone --
 * station_id, device_id, timezone, firmware_revision, serial_number -- and
 * radarSite is new. STATION_TIMEZONE is deliberately server-side only: the
 * server preformats every timezone-dependent value in StationAlmanac.
 */
export interface StationMeta {
  name?: string;
  latitude?: number;
  longitude?: number;
  elevation?: number;  // metres
  radarSite?: string;  // WSR-88D site code, e.g. "TLX"
}

/**
 * A StationMeta known to carry coordinates. `hasCoordinates` narrows to
 * this, so consumers that need a lat/lon pair (the Header's location line,
 * the radar map's centre) get them as plain numbers rather than
 * `number | undefined`.
 */
export type LocatedStation = StationMeta & { latitude: number; longitude: number };

export interface CurrentObservation {
  timestamp: number;
  windLull: number;        // m/s
  windAvg: number;         // m/s
  windGust: number;        // m/s
  windDirection: number;   // degrees
  windSampleInterval: number; // seconds
  stationPressure: number; // MB
  airTemperature: number;  // °C
  relativeHumidity: number; // %
  illuminance: number;     // lux
  uvIndex: number;
  solarRadiation: number;  // W/m²
  rainAccumulated: number; // mm
  precipitationType: PrecipitationType;
  lightningStrikeAvgDistance: number; // km
  lightningStrikeCount: number;
  battery: number;         // Volts
  reportInterval: number;  // minutes
  localDayRainAccumulation: number; // mm
  feelsLike: number;       // °C (calculated)
  dewPoint: number;        // °C (calculated)
  wetBulbTemperature: number; // °C (calculated)
  heatIndex: number;       // °C (calculated)
  windChill: number;       // °C (calculated)
  pressureTrend: PressureTrend;
}

export const PrecipitationType = {
  None: 0,
  Rain: 1,
  Hail: 2,
  RainAndHail: 3,
} as const;
export type PrecipitationType = typeof PrecipitationType[keyof typeof PrecipitationType];

export const PressureTrend = {
  Falling: 'falling',
  Steady: 'steady',
  Rising: 'rising',
} as const;
export type PressureTrend = typeof PressureTrend[keyof typeof PressureTrend];

export interface ForecastDay {
  dayNum: number;
  monthNum: number;
  conditions: string;
  icon: string;
  airTempHigh: number;   // °C
  airTempLow: number;    // °C
  precipProbability: number; // %
  precipType: string;
  sunrise: number;       // epoch
  sunset: number;        // epoch
}

export interface HourlyForecast {
  timestamp: number;
  conditions: string;
  icon: string;
  airTemperature: number;
  feelsLike: number;
  relativeHumidity: number;
  windAvg: number;
  windDirection: number;
  windGust: number;
  precipProbability: number;
  uvIndex: number;
}

export interface StationStatus {
  isOnline: boolean;
  lastReport: number;
  batteryLevel: number;
  // null means NOT REPORTED, which is the state today: neither field has a
  // source in Contract C. They are carried over UDP -- device_status has rssi
  // and firmware_revision, hub_status has rssi -- but device_status is dropped
  // by the store and no firmware column exists, so nothing reaches the API.
  //
  // null rather than 0/'' on purpose. 0 is a VALID signal reading, so using it
  // to mean "unknown" is a sentinel that collides with real data -- the same
  // defect class this branch fixed eleven times in its own probes. The card
  // renders an em-dash for null and a real value for anything else, so the
  // follow-up that plumbs the data needs no further UI change.
  signalStrength: number | null; // 0-4, or null when not reported
  firmwareVersion: string | null;
}

export type TemperatureUnit = 'C' | 'F';
export type WindUnit = 'ms' | 'mph' | 'kph' | 'kts';
export type PressureUnit = 'mb' | 'inHg' | 'hPa';
export type RainUnit = 'mm' | 'in';
// Statute miles, not nautical: this is lightning distance for a person looking
// out of a window, and the wind unit already carries 'kts' for the nautical case.
export type DistanceUnit = 'km' | 'mi';

export interface TempRecord {
  high: number | null;      // °C -- null when the window holds no reading
  highDate: string | null;  // server-rendered label, e.g. "Today", "Feb 15"
  low: number | null;       // °C
  lowDate: string | null;
}

/**
 * GET /api/almanac -- computed from this station's own store and from
 * astronomy, with no upstream call.
 *
 * sunrise/sunset are PREFORMATTED station-local clock strings ("5:47 AM"),
 * not epochs, and daylightMinutes carries the derived duration separately.
 * The browser's timezone is the VIEWER's, not the station's, and
 * STATION_TIMEZONE is not on the wire -- so an epoch here could only ever be
 * rendered against the wrong zone. Render these strings verbatim.
 *
 * Either bound may INDEPENDENTLY be null: above the Arctic Circle a day can
 * have a sunrise and no sunset. daylightMinutes is null whenever either is.
 */
export interface StationAlmanac {
  today: TempRecord;
  week: TempRecord;
  month: TempRecord;
  year: TempRecord;
  sunrise: string | null;
  sunset: string | null;
  daylightMinutes: number | null;
  moonPhase: number;        // 0–1 (0 = new, 0.5 = full)
  moonPhaseName: string;
  moonIllumination: number; // 0–1
}

export interface UserPreferences {
  temperatureUnit: TemperatureUnit;
  windUnit: WindUnit;
  pressureUnit: PressureUnit;
  rainUnit: RainUnit;
  distanceUnit: DistanceUnit;
  theme: string;
  recordsWindowDays: RecordsWindowDays;
}

export type ThemeName = 'liquid-glass' | 'midnight-aurora' | 'desert-sunset' | 'nord' | 'tokyo-night' | 'catppuccin-mocha' | 'the-grid';

// Records summary window -- Contract C (GET /api/observations/summary?days=N).
// RecordsSummary mirrors the Go summaryResponse wire tags exactly (field-for-field,
// same casing); kept distinct from StationAlmanac/TempRecord, which describe
// calendar-aligned records and astronomy rather than rolling windows.
export type RecordsWindowDays = 7 | 30 | 180 | 365;

export interface RecordsMinMax {
  max: number | null;
  min: number | null;
}

export interface RecordsSummary {
  window: { days: RecordsWindowDays; from: number; to: number };
  count: number;
  coveredFrom: number | null;
  coveredTo: number | null;
  temperature: RecordsMinMax; // °C (SI)
  humidity: RecordsMinMax;    // %
  pressure: RecordsMinMax;    // mb (SI)
  windMax: number | null;     // m/s (SI)
  gustMax: number | null;     // m/s (SI)
  rainTotal: number | null;   // mm (SI)
  lightningTotal: number | null;
}

/**
 * Which optional UI features the server has enabled (GET /api/capabilities).
 *
 * These three key names are an external contract with the Go server's
 * `capabilities` struct (internal/httpserver/capabilities.go). Both sides are
 * pinned to web/src/types/__fixtures__/capabilities.json — see
 * capabilities.contract.test.ts. Issue #149 tracks generating one from the other.
 */
export interface Capabilities {
  forecast: boolean;
  radar: boolean;
  almanac: boolean;
}

