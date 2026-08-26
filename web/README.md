# Stormglass dashboard

A weather station dashboard SPA for [WeatherFlow Tempest](https://weatherflow.com/tempest-weather-system/) stations. Built with React 19 + TypeScript.

> **Vendored copy.** This directory is vendored from the standalone
> [`tempest-display`](https://github.com/jacaudi/tempest-display) repo into
> `stormglass` so its build (`web/dist`) can be embedded directly by
> the Go server via `go:embed`. See [`PROVENANCE.md`](./PROVENANCE.md) for the
> exact source commit and what was dropped in the move (upstream's standalone
> `server/` and `Dockerfile`).

## Features

- **Live cards** — temperature, wind compass, humidity ring, pressure gauge, rain, solar & UV, lightning, station health
- **7-day forecast strip** and **almanac** (record highs/lows, sunrise/sunset, moon phase)
- **Glassmorphism UI** with six selectable themes (Liquid Glass, Midnight Aurora, Desert Sunset, Nord, Tokyo Night, Catppuccin Mocha, The Grid)
- **Real-time updates** via WebSocket (`obs_st` stream), unit conversion (°C/°F, m/s/mph/kph/kts, mb/inHg, mm/in)

## Building

```bash
task ui-build   # from the repo root: npm ci --ignore-scripts && npm run build
```

> **Build-order requirement:** the Go server's `//go:embed web/dist` directive
> requires `web/dist` to be a non-empty directory **at Go build time** — an
> embed of an empty/missing directory fails to compile. Run `task ui-build`
> (or `npm run build` in this directory) before `go build`/`go run` on the
> parent module. `web/dist/.gitkeep` keeps the directory present in git so a
> fresh checkout doesn't fail before the first UI build; it is not a
> substitute for actually running the build.

> The app currently runs on **stub data** (`src/api/stormglassApi.ts` /
> `src/api/stubData.ts`). Wiring it up to the real `/api/*` endpoints served
> by this repo's Go server is a later task in this workstream.

## Development

```bash
npm install
npm run dev        # Vite dev server at http://localhost:5173
```

## Project Structure

```
src/
  components/   # One file per card + shared GlassCard, WeatherIcon
  hooks/        # useWeatherData (data fetching), useUnits (unit prefs)
  api/          # stormglassApi.ts (fetch fns), stubData.ts (dev fixtures)
  types/        # weather.ts — all shared TypeScript interfaces
  themes/       # CSS variable sets for each theme
dist/           # Vite build output — go:embed'd by the parent Go server
```

## Backend

This vendored copy is served by the `stormglass` Go server
(`go:embed web/dist`), which also exposes the `/api/*` endpoints this UI
consumes. See the parent repo's `CLAUDE.md` for the overall architecture.
