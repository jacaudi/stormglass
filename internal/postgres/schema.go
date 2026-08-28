package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateSchema creates all required tables and indexes if they don't exist.
// This is idempotent and safe to call on every startup.
func CreateSchema(ctx context.Context, pool *pgxpool.Pool) error {
	schemas := []string{
		createObservationsTable,
		createRapidWindTable,
		createHubStatusTable,
		createEventsTable,
		createDeviceStatusTable,
		createObservationsIndexes,
		createRapidWindIndexes,
		createHubStatusIndexes,
		createEventsIndexes,
		createDeviceStatusIndexes,
	}

	for _, schema := range schemas {
		if _, err := pool.Exec(ctx, schema); err != nil {
			return fmt.Errorf("failed to execute schema: %w", err)
		}
	}

	return nil
}

const createObservationsTable = `
CREATE TABLE IF NOT EXISTS stormglass_observations (
    id            UUID PRIMARY KEY,
    serial_number TEXT NOT NULL,
    timestamp     TIMESTAMPTZ NOT NULL,

    wind_lull     DOUBLE PRECISION,
    wind_avg      DOUBLE PRECISION,
    wind_gust     DOUBLE PRECISION,
    wind_direction DOUBLE PRECISION,
    wind_sample_interval DOUBLE PRECISION,

    pressure      DOUBLE PRECISION,
    temp_air      DOUBLE PRECISION,
    temp_wetbulb  DOUBLE PRECISION,
    humidity      DOUBLE PRECISION,

    illuminance   DOUBLE PRECISION,
    uv_index      DOUBLE PRECISION,
    irradiance    DOUBLE PRECISION,

    rain_rate     DOUBLE PRECISION,
    precip_type   INTEGER,

    lightning_distance DOUBLE PRECISION,
    lightning_strike_count DOUBLE PRECISION,

    battery       DOUBLE PRECISION,
    report_interval DOUBLE PRECISION,

    UNIQUE(serial_number, timestamp)
);
`

const createRapidWindTable = `
CREATE TABLE IF NOT EXISTS stormglass_rapid_wind (
    id            UUID PRIMARY KEY,
    serial_number TEXT NOT NULL,
    timestamp     TIMESTAMPTZ NOT NULL,
    wind_speed    DOUBLE PRECISION,
    wind_direction DOUBLE PRECISION,

    UNIQUE(serial_number, timestamp)
);
`

const createHubStatusTable = `
CREATE TABLE IF NOT EXISTS stormglass_hub_status (
    id            UUID PRIMARY KEY,
    serial_number TEXT NOT NULL,
    timestamp     TIMESTAMPTZ NOT NULL,
    uptime        DOUBLE PRECISION,
    rssi          DOUBLE PRECISION,
    reboot_count  DOUBLE PRECISION,
    bus_errors    DOUBLE PRECISION,

    UNIQUE(serial_number, timestamp)
);
`

const createEventsTable = `
CREATE TABLE IF NOT EXISTS stormglass_events (
    id            UUID PRIMARY KEY,
    serial_number TEXT NOT NULL,
    timestamp     TIMESTAMPTZ NOT NULL,
    event_type    TEXT NOT NULL,
    distance_km   DOUBLE PRECISION,
    energy        DOUBLE PRECISION,

    UNIQUE(serial_number, timestamp, event_type)
);
`

const createObservationsIndexes = `
CREATE INDEX IF NOT EXISTS idx_obs_time ON stormglass_observations(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_obs_serial_time ON stormglass_observations(serial_number, timestamp DESC);
`

const createRapidWindIndexes = `
CREATE INDEX IF NOT EXISTS idx_wind_time ON stormglass_rapid_wind(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_wind_serial_time ON stormglass_rapid_wind(serial_number, timestamp DESC);
`

const createHubStatusIndexes = `
CREATE INDEX IF NOT EXISTS idx_hub_time ON stormglass_hub_status(timestamp DESC);
`

const createEventsIndexes = `
CREATE INDEX IF NOT EXISTS idx_events_time ON stormglass_events(timestamp DESC);
`

// createDeviceStatusTable stores the SENSOR's own health (#196) -- distinct
// from stormglass_hub_status, which covers the hub. Its own table rather than
// columns on stormglass_observations because a device_status broadcast has no
// observation row to attach to: it is its own report type on its own cadence.
//
// Column types follow the Go source types in DeviceStatusReport
// (report.go:242-246, 260), where every field but voltage is an int --
// HubStatusReport's float64s are a different report. sensor_status is a
// bitfield, so INTEGER rather than a float.
const createDeviceStatusTable = `
CREATE TABLE IF NOT EXISTS stormglass_device_status (
    id                UUID PRIMARY KEY,
    serial_number     TEXT NOT NULL,
    timestamp         TIMESTAMPTZ NOT NULL,
    uptime            BIGINT,
    voltage           DOUBLE PRECISION,
    firmware_revision TEXT,
    rssi              INTEGER,
    hub_rssi          INTEGER,
    sensor_status     INTEGER,

    UNIQUE(serial_number, timestamp)
);
`

// Serial-leading to match the only reader's shape (WHERE serial_number = ?
// ORDER BY timestamp DESC LIMIT 1). Postgres has no reader for this table
// today -- the HTTP API reads SQLite -- but the index costs little and keeps
// the two stores' schemas honest about the intended access pattern.
const createDeviceStatusIndexes = `
CREATE INDEX IF NOT EXISTS idx_device_status_serial_time ON stormglass_device_status(serial_number, timestamp DESC);
`
