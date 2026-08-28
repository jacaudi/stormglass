# Docker Compose

`deploy/docker-compose.yml` is a complete working stack: Stormglass, Litestream,
MinIO, an OTel collector, Prometheus, and Grafana with the dashboard
pre-provisioned.

```bash
cp deploy/.env.example deploy/.env      # then edit it
docker compose -f deploy/docker-compose.yml up -d
```

Grafana comes up on :3000, Prometheus on :9090, MinIO's console on :9001.

## Two details that are load-bearing

**Only the `stormglass` service uses `network_mode: host`.** Tempest stations
broadcast to UDP :50222 as link-local traffic, which the default bridge network
does not deliver. Everything else sits on the default compose network, and
Stormglass reaches those services via published host ports.

**A `stormglass-data-init` container chowns the volume before Stormglass
starts.** New deployments no longer need it — the image ships `/data` owned by
its own unprivileged user, and Docker seeds that ownership into a newly created
volume. It is kept for **upgrades**: Docker only seeds an *empty* volume, so a
volume that already holds a database keeps its original `root` ownership and
Stormglass exits with `attempt to write a readonly database`.

## Stack variables

`deploy/.env.example` defines the sidecar settings, which are **not** Stormglass
variables — they configure Litestream, MinIO and Grafana:

| Variable | Used by |
|---|---|
| `LITESTREAM_BUCKET` | Litestream — replication target |
| `LITESTREAM_ACCESS_KEY_ID` / `LITESTREAM_SECRET_ACCESS_KEY` | Litestream — S3/MinIO credentials |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | MinIO |
| `GF_SECURITY_ADMIN_PASSWORD` | Grafana |

Stormglass's own variables are in [Configuration](../configuration.md).

## The radar sidecar

Behind a compose profile, so it is off unless you ask:

```bash
docker compose -f deploy/docker-compose.yml --profile radar up -d
```

The card also needs `ENABLE_RADAR`, `RADAR_SITE` and station coordinates — see
[The dashboard](../dashboard.md).
