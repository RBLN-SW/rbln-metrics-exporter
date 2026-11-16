# RBLN Metrics Exporter

**Monitor Rebellions NPUs with Prometheus.**

[Get started](#get-started) · [Guides](#guides) · [Metrics](docs/reference/metrics.md) · [Releases](https://github.com/RBLN-SW/rbln-metrics-exporter/releases)

The exporter provides NPU utilization, memory, temperature, power, and health
metrics. On Kubernetes, it also associates allocated devices with their Pods.
Use the metrics in Prometheus queries, Grafana dashboards, and alerts.

## Get started

Choose the guide for your environment:

| Environment | Start here |
| --- | --- |
| Kubernetes | [Deploy through the NPU Operator](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_exporter.html) |
| Bare metal | [Run the exporter locally](docs/guides/run-locally.md) |
| Multiple remote hosts | [Set up gateway mode](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_exporter_gateway_mode.html) |

After deployment, [check the metrics endpoint](docs/guides/check-metrics.md).
A successful collection reports `rbln_up 1`.

## Guides

| I want to… | Guide |
| --- | --- |
| Store and query NPU metrics | [Connect Prometheus](https://docs.rbln.ai/latest/software/system_management/kubernetes/tutorials/collect_metrics.html) |
| Confirm collection and device health | [Check NPU metrics](docs/guides/check-metrics.md) |
| Understand metric names and labels | [Read the metrics reference](docs/reference/metrics.md) |
| Collect from remote NPU hosts | [Use gateway mode](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_exporter_gateway_mode.html) |
| Configure daemon TLS or collection permissions | [Connect to the daemon](docs/guides/connect-daemon.md) |
| Change metric names, intervals, or logging | [Configuration reference](docs/reference/configuration.md) |

## Choose a collection mode

| Mode | Use it for | Collection | Workload labels |
| --- | --- | --- | --- |
| `local` | One exporter per NPU node | On a schedule | Available on Kubernetes |
| `gateway` | One exporter collecting from remote hosts | On each scrape request | Hardware metrics only |

Both modes require access to the RBLN System Management Daemon (`rbln-smd`).
See the [daemon connection settings](docs/guides/connect-daemon.md) for your deployment.

## Development

See [Contributing](CONTRIBUTING.md) to build and test the exporter.

## Support

Report bugs and feature requests in [GitHub issues](https://github.com/RBLN-SW/rbln-metrics-exporter/issues).
Include the exporter and daemon versions, collection mode, and relevant logs.

[License](LICENSE)
