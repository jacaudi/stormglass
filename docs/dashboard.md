# The dashboard

Served at `/` on `HTTP_ADDR` (default `:8080`), embedded in the binary — there
is no separate web server to run.

The core cards need no configuration: current conditions, wind compass,
humidity, pressure, solar and UV, precipitation, lightning, station health, and
a records strip.

## Optional cards

Three cards are gated, default off, and each is independent of the storage and
metrics choices. Their variables are in
[Configuration](configuration.md).

| Card | Requires |
|---|---|
| Almanac | coordinates + a store |
| Radar | `RADAR_SITE` + coordinates + the radar sidecar |
| Forecast | **nothing yet — there is no forecast provider** |

An unmet precondition is never fatal. The card is not mounted, an `ERROR` names
what is missing, `/api/capabilities` reports it false, and ingestion continues.
A card flag cannot take down the data path.

A *malformed* value — an unparseable coordinate, an unknown `STATION_TIMEZONE`,
half a coordinate pair — is a different matter and **is** a fatal startup
error, naming every offending variable at once. (`TZ` failures are not: see
[Configuration](configuration.md#timezone).)

## Station identity

No UDP message carries the station's location, so it is configuration. Nothing
is required, and no combination of missing values prevents startup. See
[Configuration](configuration.md) for the variables, ranges and defaults.

The one setting worth calling out is `TZ`: with coordinates set and no timezone
configured, the almanac still mounts but renders every time on UTC, and logs a
`WARN`. The timezone is server-side only — the server preformats every
timezone-dependent value, so it never appears on the wire.

## HTTP endpoints

`GET /` (dashboard) · `/healthz` · `/api/capabilities` · `/api/station` ·
`/api/observations/current` · `/api/observations/history` ·
`/api/observations/summary` · `/api/almanac` · `/api/radar/{site}`

A disabled feature's route is **not registered at all** — it returns 404, and
`/api/capabilities` reports it false.

## Radar basemap

The radar card renders NEXRAD reflectivity over an OpenStreetMap basemap that is
**operator-supplied and deliberately not bundled**. With none present the map
still renders — radar returns over an empty background, plus a 404 in the
browser console for the missing asset. **That is the intended graceful degrade,
not a defect.**

It is not baked into the image for two reasons: the operator's region is
unknowable at build time (a whole-planet basemap is hundreds of MB and pointless
for a single-station kiosk), and `build.protomaps.com`'s dated builds expire in
about a week, so a build-time fetch would silently start failing days after the
image was built.

To add one, place a [Protomaps](https://protomaps.com/) `.pmtiles` file at
`web/public/basemap/osm.pmtiles`. It is read directly in the browser over the
`pmtiles://` MapLibre protocol using byte-range requests — no tile server and no
API key. `web/public/basemap/PROVENANCE.md` records the full rationale.
