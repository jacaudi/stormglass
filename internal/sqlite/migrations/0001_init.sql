-- All timestamps stored as INTEGER unix-epoch seconds (UTC). UUIDv7 text PKs generated in Go.
CREATE TABLE IF NOT EXISTS tempest_observations (
  id TEXT PRIMARY KEY,                 -- UUIDv7
  serial_number TEXT NOT NULL,
  timestamp INTEGER NOT NULL,          -- ob[0] epoch
  wind_lull REAL, wind_avg REAL, wind_gust REAL, wind_direction REAL,
  wind_sample_interval REAL,
  pressure REAL, temp_air REAL, temp_wetbulb REAL, humidity REAL,
  illuminance REAL, uv_index REAL, irradiance REAL, rain_rate REAL,
  precip_type INTEGER,                 -- categorical enum (0 none, 1 rain, 2 hail, 3 rain+hail), not a measurement
  lightning_distance REAL, lightning_strike_count REAL,
  battery REAL, report_interval REAL,
  UNIQUE(serial_number, timestamp)     -- backs ON CONFLICT DO NOTHING
);
CREATE TABLE IF NOT EXISTS tempest_rapid_wind (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  wind_speed REAL, wind_direction REAL, UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_hub_status (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  uptime INTEGER, rssi REAL, reboot_count INTEGER, bus_errors INTEGER,
  UNIQUE(serial_number, timestamp)
);
CREATE TABLE IF NOT EXISTS tempest_events (
  id TEXT PRIMARY KEY, serial_number TEXT NOT NULL, timestamp INTEGER NOT NULL,
  event_type TEXT NOT NULL, distance_km REAL, energy REAL,
  UNIQUE(serial_number, timestamp, event_type)
);
CREATE INDEX IF NOT EXISTS idx_obs_serial_time ON tempest_observations(serial_number, timestamp);
-- The read hot-path (LatestObservationAny's ORDER BY timestamp DESC LIMIT 1,
-- HistoryPoints' WHERE timestamp BETWEEN ?) filters/sorts on timestamp alone.
-- idx_obs_serial_time leads with serial_number, so neither query can use it and
-- both fall back to a full table scan + sort, serialized against the single
-- writer connection (SGE review I1). This index leads with timestamp so both
-- queries can use it directly.
CREATE INDEX IF NOT EXISTS idx_obs_time ON tempest_observations(timestamp);
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
