# Check NPU metrics

Confirm that metrics are being collected and distinguish collection failures
from device health problems.

## 1. Open the endpoint

For an operator deployment, replace `rbln-system` if needed:

```bash
kubectl get pods -n rbln-system -o wide
kubectl get services -n rbln-system
kubectl port-forward -n rbln-system service/rbln-metrics-exporter-service 9090:9090
```

Use the exporter Service name shown by your installation. In another terminal:

```bash
curl --fail http://127.0.0.1:9090/metrics
```

For a local binary, query its configured port directly. Stop port forwarding
with Ctrl+C when finished.

For gateway mode, include the daemon's address in every request. This example
uses a gateway on port `9200`; replace the address and port for your deployment:

```bash
curl --fail 'http://127.0.0.1:9200/metrics?target=npu-host-1:50051'
```

The target must be reachable from the gateway. A request without `target`
returns HTTP 400.

## 2. Check collection and health

| Metric | How to read it |
| --- | --- |
| `rbln_up` | `1` means the last daemon collection succeeded |
| `rbln_npu_collection_success` | `1` means queries for that device succeeded |
| `rbln_npu_health` | `0` means READY; `1` means another known device state |
| `rbln_npu_device_status` | The sample with value `1` identifies the current state |

These names describe this checkout; see the [metrics reference](../reference/metrics.md)
for collection semantics and device labels. For an installed release, read the
documentation at the matching Git tag.
For health, memory, and other legacy metrics, `PROMETHEUS_METRIC_NAMES=true`
selects the names above; see [metric-name settings](../reference/configuration.md#metric-names).
A successful query does not guarantee every sensor value is available.
Unavailable readings may be omitted, and missing health data does not mean READY.

If `rbln_up` is `0`, check the daemon endpoint, connectivity, and collection
permissions in the exporter logs. An HTTP response alone does not confirm
successful device collection.
Follow [Connect to the daemon](connect-daemon.md) for TLS and metric-group settings.

## 3. Check workload attribution

In Kubernetes local mode, look for `namespace`, `pod`, and `container` labels
on allocated devices. The exporter needs access to kubelet's pod-resources socket.
DRA attribution requires Kubernetes 1.34 or later.

If several Pods share a device, one claimant is represented in the labels;
`rbln_npu_device_shared 1` identifies that partial attribution. Gateway mode
provides hardware metrics without Kubernetes workload labels.

For SR-IOV devices, use `hostname` and `name` to distinguish functions. Memory
and utilization belong to compute devices; physical readings such as temperature
and power remain on the parent PF.

Next: [configure Prometheus collection](https://docs.rbln.ai/latest/software/system_management/kubernetes/tutorials/collect_metrics.html).
