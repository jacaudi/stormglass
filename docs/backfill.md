# Backfill

Fills holes in the local observation history from the Tempest REST API. This is
the one feature that needs a WeatherFlow token. It is safe to run against a live
database and is idempotent — re-running inserts nothing new.

```bash
TOKEN=your_api_token stormglass backfill
TOKEN=... stormglass backfill --dry-run
TOKEN=... stormglass backfill --from 2026-07-01T00:00:00Z --to 2026-07-05T00:00:00Z
```

Unlike [API export mode](modes.md), which is selected by setting `TOKEN` and
runs the whole export path, this is an explicit subcommand and does not start
the UDP listener or the HTTP server.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--from` / `--to` | auto-detect | Explicit window, RFC3339 **UTC**. Must be given together |
| `--min-gap` | `30m` | Smallest interval that counts as a gap. Raise it for stations with a long `report_interval` |
| `--dry-run` | `false` | Detect and plan only — zero observation fetches, zero writes. It still lists devices, so it *does* validate the token |
| `--store` | — | `sqlite` or `postgres`. **Required** when both stores are configured |

## `--store` and the fan-out configuration

With `ENABLE_POSTGRES=true` *and* `SQLITE_PATH` set, the daemon writes every
observation to **both** stores. Backfill repairs one store per run, so in that
configuration it refuses to start without `--store` rather than guessing.
Silently repairing Postgres while leaving the Litestream-replicated SQLite
database holed — and then reporting success — would be worse than failing.

## Multiple sensors

`backfill` enumerates every `ST` device on the account, so a station with two
Tempest units has both repaired. This differs from `TOKEN`-mode API export,
which sees one device per station.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success — including `--help`, and including permanent holes (windows the station was genuinely offline for, which the API cannot fill either) |
| `1` | One or more gaps failed, or a runtime error |
| `2` | Usage error |

## Scope

`stormglass_observations` only. There is no historical REST endpoint for rapid
wind, hub status or discrete events. Lightning is partially recovered in
aggregate through the observation columns, but not as `stormglass_events` rows.

## Safety

If the API's station serials and the store's serials have **no overlap at all**
on a non-empty store, backfill stops and writes nothing. That is the signature
of a serial-format mismatch, which would otherwise create a parallel series that
never dedupes.

A *newly added* station, or one this host has never heard over UDP, is **not** a
mismatch: its serial simply has no rows yet, and backfill fetches its whole
history normally.
