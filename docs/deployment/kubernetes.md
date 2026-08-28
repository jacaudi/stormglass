# Stormglass on Kubernetes

A working [bjw-s `app-template`](https://bjw-s-labs.github.io/helm-charts/docs/app-template/)
deployment. Four manifests, all under **`deploy/kubernetes/`** in the repository:

| File | What it is |
|---|---|
| `values.yaml` | The deployment config, and the single source of truth for it |
| `helmrelease.yaml` | Flux wrapper. Reads `values.yaml` from a ConfigMap rather than restating it |
| `helmrepository.yaml` | The chart source for the above. Skip if you already have one |
| `servicemonitor.yaml` | Optional, for the deprecated Prometheus scrape path |

```bash
helm install stormglass bjw-s-labs/app-template \
  --version 5.1.0 -f deploy/kubernetes/values.yaml
```

`values.yaml` was rendered with `helm template` against app-template 5.1.0 and
the output validated with `kubectl apply --dry-run=client`. It has **not** been
applied to a live cluster — treat the station coordinates, radar site, image
tags and the OTLP endpoint as placeholders to change.

## Two settings you cannot drop

**`hostNetwork: true`.** Tempest stations broadcast to UDP :50222 as link-local
traffic, which does not cross the CNI pod network. A pod without host networking
starts cleanly, passes every probe, serves the dashboard — and receives no
observations at all. This is the single most likely reason a Kubernetes
deployment looks healthy and stays empty.

**`dnsPolicy: ClusterFirstWithHostNet`.** It has to accompany `hostNetwork`.
Without it the pod inherits the node's `/etc/resolv.conf` and loses cluster DNS,
so in-cluster names — the OTel collector, a Postgres service — stop resolving.
The default `ClusterFirst` is silently wrong here rather than an error.

Host networking also means the pod binds :8080, :9000 and :8081 **on the node**.
Check those are free, and note it effectively pins you to one replica per node.

## Storage and Litestream

`/data` must be writable or the process exits at startup — that is deliberate
fail-loud behaviour, not a bug to work around.

The controller is a **StatefulSet, not a Deployment**, on purpose: `/data` holds
one SQLite database with a WAL that Litestream is streaming, and a rolling
Deployment update would briefly run two pods with two writers on one file.
`ReadWriteOnce` is fine, since host networking pins the pod to a node anyway.

The `litestream` sidecar shares the `/data` mount and streams the WAL to S3 or
MinIO. It **owns WAL checkpointing** — that is why Stormglass does not configure
`wal_autocheckpoint` itself. If you drop the sidecar, drop its two volumes too;
nothing else depends on it.

It needs a ConfigMap `stormglass-litestream` holding `litestream.yml` (use
`deploy/litestream.yml` as the content, with the path `/data/stormglass.db`) and
a Secret of the same name with `access-key-id` and `secret-access-key`.

## Radar sidecar

The `radar` container is optional and only useful with `ENABLE_RADAR: "true"`
and a valid `RADAR_SITE`. Because the pod is host-networked, the app reaches it
on `localhost:8081` with no service required.

Its memory request is 512Mi and its limit 2Gi. That is not padding — decoding a
NEXRAD volume scan loads the whole thing into memory, and a single tile response
can be several megabytes of GeoJSON.

If `ENABLE_RADAR` is true but the sidecar is absent, the app still starts and
still ingests; radar tile requests simply fail at runtime.

## Metrics

`values.yaml` uses the OTel path (`ENABLE_OTEL`), which is the supported one and
the one the bundled Grafana dashboard is built for. Point
`OTEL_EXPORTER_OTLP_ENDPOINT` at your collector.

`servicemonitor.yaml` covers the older scrape path if you want it. Read the
comments in that file first — it needs two other changes to work, and the names
it scrapes do not match the bundled dashboard.

## Probes

`/healthz` on the app's HTTP port is the liveness and readiness endpoint. The
container image's own `HEALTHCHECK` shells out to `stormglass healthcheck`, which
probes the same endpoint; Kubernetes ignores that and uses the probes above.
