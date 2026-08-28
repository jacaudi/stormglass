# Metrics

Stormglass exposes metrics by three routes, and **they do not all emit the same
names.** That matters more than it sounds — read "The direct and OTel paths emit
different metric names" below before wiring up a dashboard.

Variables, defaults and which are required are in
[Configuration](configuration.md).

> **`ENABLE_PROMETHEUS_*` is deprecated.** The bespoke pushgateway writer and
> scrape server are slated for removal in the next release; `ENABLE_OTEL` is the
> replacement. Enabling either logs a one-time `WARN` at startup. New
> deployments should use the OTel path, which is also the one the shipped
> Grafana dashboard is built for.

## The supported path: OpenTelemetry

Set `ENABLE_OTEL=true` and point `OTEL_EXPORTER_OTLP_ENDPOINT` at a collector.
`deploy/otel-collector-config.yaml` is a working collector config with the
Prometheus exporter in legacy underscore naming mode.

## The deprecated paths

**Scrape.** `ENABLE_PROMETHEUS_METRICS=true` serves `/metrics` on
`PROMETHEUS_METRICS_PORT` (default 9000):

```yaml
scrape_configs:
  - job_name: 'stormglass'
    static_configs:
      - targets: ['localhost:9000']
```

**Push.** `ENABLE_PROMETHEUS_PUSHGATEWAY=true` with
`PROMETHEUS_PUSHGATEWAY_URL` pushes instead. Weather stations broadcast
sporadically, which is why a push path exists at all.

## The direct and OTel paths emit different metric names

> **If you import the bundled Grafana dashboard, you need the OTel path.**

The deprecated Prometheus endpoints emit 18 metric families named from the
exporter's own descriptors, labelled by `instance`:

`stormglass_temperature_c` · `stormglass_humidity_percent` ·
`stormglass_pressure_mb` · `stormglass_wind_ms` ·
`stormglass_wind_direction_degrees` · `stormglass_rain_rate_mm_min` ·
`stormglass_rainfall_total` · `stormglass_lightning_distance_km` ·
`stormglass_lightning_strike_count` · `stormglass_uv_index` ·
`stormglass_irradiance_w_m2` · `stormglass_illuminance_lux` ·
`stormglass_battery_volts` · `stormglass_report_interval_minutes` ·
`stormglass_rssi_dbm` · `stormglass_uptime_seconds_total` ·
`stormglass_reboots_total` · `stormglass_bus_errors_total`

`stormglass_temperature_c` carries a `kind` label (`air`, `wetbulb`) and
`stormglass_wind_ms` carries `kind` (`lull`, `avg`, `gust`, `rapid`).

The OTel path emits a deliberately different set, labelled by `serial`. It adds
`stormglass_dewpoint_c`, `stormglass_heat_index_c` and `stormglass_wetbulb_c`,
drops `report_interval`, and renames four:

| Direct | OTel |
|---|---|
| `stormglass_wind_ms` | `stormglass_wind_meters_per_second` |
| `stormglass_uptime_seconds_total` | `stormglass_uptime_seconds` |
| `stormglass_rainfall_total` | `stormglass_rainfall_mm_total` |
| `stormglass_lightning_strike_count` | `stormglass_lightning_strike_count_total` |

`deploy/grafana/dashboards/weather-nerd.json` queries **the OTel names, keyed on
`serial`**. Pointed at a direct scrape it renders empty panels — the names and
the label are both wrong for it.
