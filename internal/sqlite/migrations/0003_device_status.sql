-- 0003: device_status storage (#196).
--
-- The Tempest broadcasts device_status roughly once a minute carrying the
-- SENSOR's own health -- distinct from hub_status, which covers the hub. Both
-- writers dropped it in their `default:` arm, so the shipped Station Health
-- card rendered SIGNAL -- and a blank FIRMWARE forever on a station that was
-- sending both every minute.
--
-- Its own table rather than columns on stormglass_observations: a
-- device_status broadcast has no observation row to attach to -- it is its own
-- report type on its own cadence -- so columns there would be NULL on every
-- row the observation writer inserts. This mirrors stormglass_hub_status,
-- which is the same shape for the hub's side.
--
-- Column affinities follow the GO SOURCE types, not hub_status's: every field
-- but voltage is an int in DeviceStatusReport (report.go:242-246, 260) where
-- HubStatusReport uses float64. sensor_status is a BITFIELD, so INTEGER for
-- the same reason precip_type is INTEGER in 0002 -- a categorical, not a
-- measurement.
--
-- No separate index: UNIQUE(serial_number, timestamp) creates a
-- serial-leading index, and the only reader is serial-SCOPED
-- (WHERE serial_number = ? ORDER BY timestamp DESC LIMIT 1), which that index
-- serves directly. An UNSCOPED timestamp sort could not use it -- that is the
-- I1 defect idx_obs_time exists to fix in 0002 -- which is one reason the
-- read is scoped.
CREATE TABLE IF NOT EXISTS stormglass_device_status (
  id TEXT PRIMARY KEY,                 -- UUIDv7
  serial_number TEXT NOT NULL,
  timestamp INTEGER NOT NULL,          -- unix-epoch seconds (UTC)
  uptime INTEGER,
  voltage REAL,                        -- the one genuine measurement
  firmware_revision TEXT,              -- TEXT: vendor forms are mixed across report types
  rssi INTEGER,
  hub_rssi INTEGER,
  sensor_status INTEGER,               -- bitfield, not a measurement
  UNIQUE(serial_number, timestamp)
);
