# Run the exporter locally

Collect metrics from an NPU host without a Kubernetes deployment.

## Before you start

Use a Linux NPU host with the RBLN driver and `rbln-smd` running. This example
assumes that the daemon accepts a local connection at `127.0.0.1:50051`.
If it requires TLS or restricted access, [configure the daemon connection](connect-daemon.md).

For a published container image, follow the
[bare-metal installation guide](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_exporter.html).
The steps below run a binary built from this checkout.

## 1. Build the binary

Install the Go toolchain required by [go.mod](../../go.mod), then run from the
repository root:

```bash
make build
```

## 2. Start collection

```bash
PROMETHEUS_METRIC_NAMES=true ./bin/rbln-metrics-exporter \
  --mode=local \
  --kubernetes-mode=off \
  --rbln-daemon-url=127.0.0.1:50051 \
  --port=9090
```

Use `./bin/rbln-metrics-exporter --help` for the connection options supported by
this checkout, or see the [configuration reference](../reference/configuration.md).
The daemon must permit metric collection; see [metric-group permissions](connect-daemon.md#3-check-metric-group-permissions)
if it rejects group creation.

## 3. Verify the endpoint

In another terminal:

```bash
curl --fail http://127.0.0.1:9090/metrics
```

After a collection cycle, expect `rbln_up 1` and NPU metric samples.
If collection fails, [check the metrics and logs](check-metrics.md).

## Stop the exporter

Press Ctrl+C in the terminal running the exporter.

Next: [connect Prometheus](https://docs.rbln.ai/latest/software/system_management/kubernetes/tutorials/collect_metrics.html).
