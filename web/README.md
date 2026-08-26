# Stormglass dashboard

The web UI for [Stormglass](../README.md), a self-hosted appliance for
[WeatherFlow Tempest](https://weatherflow.com/tempest-weather-system/) stations.
React 19 + TypeScript, built with Vite.

> **Vendored copy.** This directory is vendored from the standalone
> [`tempest-display`](https://github.com/jacaudi/tempest-display) repo into
> `stormglass` so its build (`web/dist`) can be embedded directly by
> the Go server via `go:embed`. See [`PROVENANCE.md`](./PROVENANCE.md) for the
> exact source commit and what was dropped in the move (upstream's standalone
> `server/` and `Dockerfile`).

## Features

- **Live cards** — current conditions, wind compass, humidity ring, pressure
  gauge, precipitation, solar & UV, lightning, station health
- **Records strip** and **station almanac** (record highs/lows, sunrise/sunset,
  moon phase, daylight length)
- **Optional radar card**, served by the radar sidecar
- **Seven themes** — Liquid Glass, Midnight Aurora, Desert Sunset, Nord,
  Tokyo Night, Catppuccin Mocha, The Grid
- **Unit conversion** — °C/°F, m/s / mph / kph / kts, mb / inHg, mm / in.
  The choice is remembered per browser under the `stormglass-prefs` key.

There is no forecast card: Stormglass has no forecast provider yet
([#81](https://github.com/jacaudi/stormglass/issues/81)).

The optional cards are gated server-side. The UI asks `GET /api/capabilities`
and renders only what the server reports as enabled, so a disabled card is
absent rather than broken.

## How it gets data

By **polling plain JSON**, every 30s (`POLL_INTERVAL_MS` in
`hooks/useWeatherData.ts`). There is no WebSocket backend — the server's
contract is ordinary request/response JSON on `/api/*`.

That is a deliberate match for the source data: a Tempest station reports about
once a minute, so a persistent socket would spend almost all of its time idle.

## Building

From the repo root:

```bash
task build        # node:build then go:build, in that order
task node:build   # this directory only
```

> **Build order matters.** `web/embed.go` carries `//go:embed all:dist`, so the
> Go binary embeds whatever `web/dist` holds at compile time. Build Go first and
> you ship the previous UI — or, on a clean checkout, just the tracked
> `.gitkeep`. `task build` sequences them correctly.
>
> `web/dist/.gitkeep` keeps the directory present in git so a fresh checkout
> compiles at all; it is not a substitute for running the build.

## Development

```bash
npm install
npm run dev        # Vite dev server at http://localhost:5173
npm test           # vitest
```

The dev server has no backend of its own. Run the Go server alongside it, or
point at an existing instance, so the `/api/*` calls resolve.

## Project structure

```
src/
  components/   # One file per card + shared GlassCard, WeatherIcon
  hooks/        # useWeatherData (polling), useUnits (unit prefs)
  api/          # stormglassApi.ts — the fetch layer for /api/*
  types/        # weather.ts — shared TypeScript interfaces
  themes/       # CSS variable sets for each theme
dist/           # Vite build output — go:embed'd by the parent Go server
```

## Backend

Served by the Stormglass Go server, which embeds `web/dist` and exposes the
`/api/*` endpoints this UI consumes. The endpoint list and the configuration
that gates each card are in the [top-level README](../README.md).
