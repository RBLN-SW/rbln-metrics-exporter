# Metrics Exporter settings

These settings describe this checkout. For an installed release, read this file
at the matching Git tag.

## Collection options

Flags override environment variables. For the table below, prefix each environment
suffix with `RBLN_METRICS_EXPORTER_`, for example `RBLN_METRICS_EXPORTER_PORT`.

| Flag | Environment suffix | Default / purpose |
| --- | --- | --- |
| `--mode` | `MODE` | `local`; also accepts `gateway` |
| `--rbln-daemon-url` | `RBLN_DAEMON_URL` | `127.0.0.1:50051`; local-mode gRPC endpoint |
| `--port` | `PORT` | `9090`; HTTP metrics port |
| `--interval` | `INTERVAL` | `5`; local collection interval in seconds, from `1` to `60` |
| `--oneshot` | `ONESHOT` | `false`; accepted but has no effect in this checkout |
| `--kubernetes-mode` | `KUBERNETES_MODE` | `auto`; also accepts `on` or `off` |
| `--pci-devices-path` | `PCI_DEVICES_PATH` | `/sys/bus/pci/devices`; local VF profile discovery |

`--node-name` uses `NODE_NAME`, falling back to the host name. It supplies the
`hostname` metric label in local mode. Gateway mode derives the label from the
target host and does not attach Kubernetes workload labels.

For TLS, metric groups, and collection permissions, see
[Connect to the daemon](../guides/connect-daemon.md).

## Metric names

Set `PROMETHEUS_METRIC_NAMES=true` in the exporter process environment to use
the `rbln_npu_*` names for health, memory, temperature, power, and utilization.
Otherwise those metrics use legacy `RBLN_DEVICE_STATUS:*` names.
`rbln_up` and newer metrics such as `rbln_npu_collection_success` keep their names.

The [local example](../guides/run-locally.md) sets this variable explicitly.
Check the running container's environment when existing dashboards cannot find
the expected metric names.

## Logging

| Environment variable | Default | Accepted values |
| --- | --- | --- |
| `RBLN_METRICS_EXPORTER_LOG_LEVEL` | `info` | `error`, `warning`, `info`, `debug`, `trace` |
| `RBLN_METRICS_EXPORTER_LOG_FORMAT` | `json` | `json`, `text` |

Use `warning` in configuration; emitted log records spell the level `warn`.

For an operator deployment, use `metricsExporter.logging.level` and
`metricsExporter.logging.format` in your existing Helm values.
For a local process, set the environment variables before starting it. Logs go
to stdout; invalid logging values fall back with a warning. Restore `info` after
diagnosis.

Next: [check the collected metrics](../guides/check-metrics.md).
