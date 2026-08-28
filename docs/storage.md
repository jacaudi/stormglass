# Storage

Stormglass always writes observations somewhere. Which store is a configuration
choice, and the two are not exclusive — see
[Configuration](configuration.md) for the variables and the
SQLite/Postgres/both matrix.

## What gets stored

Both stores use the same four tables — `stormglass_observations`,
`stormglass_rapid_wind`, `stormglass_hub_status`, `stormglass_events` — with
UUIDv7 primary keys generated in Go, so no database extensions are required.
The schema is created automatically at startup via an embedded versioned
migration.

**All UDP values are stored raw — no unit conversions:**

- Pressure: `mb` (millibars), from field 6
- Report interval: `minutes`, from field 17
- All other fields: stored exactly as received

This matters when you query the tables directly: the numbers are the station's
own, in the station's units, not normalised to anything.

## SQLite (the default)

With no `ENABLE_POSTGRES`, observations are written to a local SQLite database
at `SQLITE_PATH` (default `/data/stormglass.db`).

Pure-Go driver (`modernc.org/sqlite`, `CGO_ENABLED=0`, preserving the static
image), WAL journal mode, `synchronous=NORMAL`, `foreign_keys=ON`, and batched
inserts.

**`/data` must be writable.** SQLite is the default store and the process
**exits at startup** if the database cannot be opened, rather than silently
running without persistence. The published image ships `/data` owned by its own
unprivileged user, so `docker run -v stormglass-data:/data` works as documented;
a bind mount needs host-side ownership.

**WAL checkpointing is deliberately left to Litestream.** Stormglass does not
set an aggressive `wal_autocheckpoint` — overriding it would silently break
replication.

## Litestream

Run [Litestream](https://litestream.io) as a sidecar against the same `/data`
volume and it streams the WAL to S3/MinIO continuously, giving point-in-time
restore. `deploy/litestream.yml` is a working config, and
`deploy/docker-compose.yml` wires up the whole path including a local MinIO.
This is the reason Stormglass does not checkpoint the WAL itself — Litestream
owns it.

## PostgreSQL

Opt-in, and able to run alongside SQLite (fan-out) when both are configured.
Connection settings and tuning are in
[Configuration](configuration.md).

The same four tables are created automatically on startup.

## Choosing

- **SQLite alone** is the appliance default: one file, no server, Litestream for
  durability. Right for a single station on a single host.
- **Postgres alone** suits an existing database you already operate and back up.
  Set `ENABLE_POSTGRES=true` and leave `SQLITE_PATH` unset.
- **Both** fans out every write. Useful while migrating between them. Note that
  the [`backfill`](backfill.md) subcommand repairs one store per run and refuses
  to guess which, so it requires `--store` in this configuration.
