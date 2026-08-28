# Other modes

The daemon is the default. Three other entry points exist; the full selection
table is in [Configuration](configuration.md).

## API export mode

Setting `TOKEN` **without a subcommand** switches the process into the full
historical-export path: it fetches historical observations over REST, writes
them to PostgreSQL and/or gzipped files, and exits.

```bash
TOKEN=your_api_token ENABLE_POSTGRES=true POSTGRES_URL=postgresql://... \
  go run ./cmd/stormglass
```

`KEEP_EXPORT_FILES=true` retains the `.gz` files instead of removing them after
a successful database write.

This is a different thing from [`backfill`](backfill.md), which is a subcommand
and leaves the daemon behaviour alone. SQLite is **not** written in export mode.

## Healthcheck

```bash
stormglass healthcheck
```

Probes a running server's `/healthz` on the same `HTTP_ADDR` the server bound,
and is what the container's `HEALTHCHECK` uses. The final image is built on
`cgr.dev/chainguard/static` and has no shell, curl or wget, so the binary probes
itself rather than running a conventional command probe.

## Subcommands bypass mode selection

`backfill` and `healthcheck` are chosen by the first CLI argument, not by
environment variables, and neither starts the UDP listener or the HTTP server.
Any other non-flag first argument is a usage error (exit 2) rather than a silent
fallthrough to daemon mode.

## Debugging the UDP path

`LOG_UDP=true` logs every broadcast received. It is the fastest way to tell
whether the station is reaching the container at all — if nothing appears, the
problem is network reachability (host networking, UDP :50222) rather than
anything inside Stormglass.
