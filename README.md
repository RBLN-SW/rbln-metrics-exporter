# RBLN Metrics Exporter

The RBLN Metrics Exporter exposes detailed telemetry for RBLN NPUs in [Prometheus](https://prometheus.io/) format so that you can build Grafana dashboards, alert on thermal or utilization anomalies, and correlate accelerator health with Kubernetes workloads.

---

## Key Features

- **Native Prometheus endpoint** on `/metrics` served by a lightweight Go HTTP server.
- **NPU-aware scheduling** via DaemonSet affinities that target nodes labeled by NPU Feature Discovery add-on.
- **Kubernetes context labels** (namespace, pod, container) populated by integrating with `kubelet` pod-resources API.
- **Binary or container deployment** with configurable scrape interval, port, and daemon gRPC endpoint.

---

## Compatibility and Prerequisites

| Requirement | Details |
| --- | --- |
| RBLN Driver | `>= 1.3.40` |
| RBLN Daemon | Installed alongside the driver to serve metrics over gRPC (`:50051` by default) |
| Operating System | Linux kernel with access to `/sys`; when running on Kubernetes ensure `/var/lib/kubelet/pod-resources` is accessible |
| Prometheus | Any Prometheus-compatible scraper (Vanilla, Helm chart, or Prometheus Operator). The exporter can run without Prometheus at first, but you need a scraper to persist or visualize the metrics. |
| Grafana (optional) | For dashboards that visualize the exported metrics |

---

## Quick Start (Standalone Binary)

1. Build the binary locally via `make build`, which outputs `./bin/rbln-metrics-exporter`.
2. Ensure the host can reach the RBLN daemon (default `127.0.0.1:50051`).
3. Run the exporter:
   ```bash
   $ ./rbln-metrics-exporter \
       --port 9090 \
       --interval 5 \
       --rbln-daemon-url 127.0.0.1:50051
   ```
4. Verify the endpoint:
   ```bash
   $ curl http://[NODE_IP]:9090/metrics
   ```

---

## Command-Line Interface

```text
$ rbln-metrics-exporter --help
Expose RBLN device metrics via Prometheus

Usage:
  rbln-metrics-exporter [flags]

Flags:
  -h, --help                     help for rbln-metrics-exporter
      --interval int             Interval of collecting metrics (1-60 seconds) (default 5)
      --oneshot                  Collect once and exit
      --port int                 Port to listen for requests (default 9090)
      --rbln-daemon-url string   Endpoint to RBLN daemon grpc server (default "127.0.0.1:50051")
      --node-name string         Override detected node name (defaults to hostname or NODE_NAME env)
```

### Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL` | `127.0.0.1:50051` | gRPC endpoint of the RBLN daemon |
| `RBLN_METRICS_EXPORTER_PORT` | `9090` | Port for the `/metrics` HTTP server |
| `RBLN_METRICS_EXPORTER_INTERVAL` | `5` | Collection interval in seconds (1–60) |
| `RBLN_METRICS_EXPORTER_ONESHOT` | `false` | When `true`, scrape once and exit |
| `NODE_NAME` | auto-detected | Overrides the node label inserted into metrics |


---

## Kubernetes Deployment

### Step 1: Deploy the DaemonSet

Apply the reference manifest:

```bash
$ kubectl apply -f https://raw.githubusercontent.com/rebellions-sw/rbln-metrics-exporter/refs/heads/main/deployments/kubernetes/daemonset.yaml
```

Highlights of the manifest:

- Pins scheduling with `nodeAffinity` requiring `rebellions.ai/npu.deploy.metrics-exporter=true`. If you depend on the `rebellions.ai/npu.present=true` label emitted by [rbln-npu-feature-discovery](https://github.com/rebellions-sw/rbln-npu-feature-discovery), swap in the bundled snippet:
  ```yaml
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: rebellions.ai/npu.present
                operator: In
                values:
                  - "true"
  ```
- Mounts:
  - `/var/lib/kubelet/pod-resources` (read-only) to correlate device allocations with workloads.
  - `/sys` for low-level device metadata.
- Set the `RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL` environment variable so that it connects to the local RBLN Daemon on each node.

### Step 2: Install Prometheus

Deploy Prometheus using Helm (`prometheus-community/kube-prometheus-stack`) or the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator). The exporter can run without Prometheus, but metrics will only be stored once the scraper is active.

### Step 3: Add a ServiceMonitor

If you installed Prometheus Operator, create the resource below (update the namespace/labels to match your stack):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: rbln-metrics-exporter
  namespace: monitoring
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: rbln-metrics-exporter
  namespaceSelector:
    matchNames:
      - rbln-system
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
      scrapeTimeout: 10s
```

Apply with `kubectl apply -f servicemonitor.yaml`.

### Step 4 (Optional): Grafana Dashboards

Deploy Grafana via Helm or the Grafana Operator and import dashboards that visualize:

- Temperature vs. power draw per card
- Memory utilization by namespace/pod
- Binary health alarms (0 = Active, 1 = Inactive)

---

## Metrics Reference

All metrics share the `RBLN_DEVICE_STATUS:` prefix and are reported as Prometheus gauges per device.

| Name | Description | Unit |
| --- | --- | --- |
| `RBLN_DEVICE_STATUS:TEMPERATURE` | Device temperature | °C |
| `RBLN_DEVICE_STATUS:CARD_POWER` | Card power draw | W |
| `RBLN_DEVICE_STATUS:DRAM_USED` | DRAM currently in use | GiB |
| `RBLN_DEVICE_STATUS:DRAM_TOTAL` | Total DRAM | GiB |
| `RBLN_DEVICE_STATUS:UTILIZATION` | SM utilization | % |
| `RBLN_DEVICE_STATUS:HEALTH` | Binary health (0 = active, 1 = inactive) | 0/1 |

### Common Label Set

| Label | Description |
| --- | --- |
| `name` | Character device node (`rbln0`, `rbln1`, …) |
| `uuid` | Unique NPU UUID |
| `card` | Marketing card name (e.g., `RBLN-CA25`) resolved from PCIe device ID |
| `deviceID` | PCIe device ID |
| `hostname` | Host node name |
| `driver_version` | Kernel driver build |
| `firmware_version` | Accelerator firmware |
| `smc_version` | SMC firmware |

### Kubernetes-Specific Labels

| Label | Description |
| --- | --- |
| `namespace` | Namespace of the pod consuming the device |
| `pod` | Pod name |
| `container` | Container name |

### Example Output

```text
# TYPE RBLN_DEVICE_STATUS:DRAM_TOTAL gauge
RBLN_DEVICE_STATUS:DRAM_TOTAL{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln0",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="55668c63-d739-4193-8212-ad7ba933520c"} 15.71875
# TYPE RBLN_DEVICE_STATUS:TEMPERATURE gauge
RBLN_DEVICE_STATUS:TEMPERATURE{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln1",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="84389d45-ebf3-4b74-9d80-6ec8a09d8be4"} 54
# HELP RBLN_DEVICE_STATUS:HEALTH NPU health status
RBLN_DEVICE_STATUS:HEALTH{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln3",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="8e65fc0d-df7d-4e21-a81b-a76a1a1e69ab"} 0
```

---

## Troubleshooting

| Symptom | Possible Cause | Action |
| --- | --- | --- |
| `/metrics` is empty | Unable to reach RBLN daemon | Verify `RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL`, ensure daemon is listening, check firewall |
| No Kubernetes labels | Pod-resources socket missing | Confirm `/var/lib/kubelet/pod-resources/kubelet.sock` is mounted and kubelet exposes the API |
| Scrape errors in Prometheus | Authorization/namespace mismatch | Ensure Service or ServiceMonitor selects the exporter pods and Prometheus is allowed to scrape the namespace |

---

## Licensing

The exporter is provided under the Rebellions Software User License Agreement (see [`LICENSE`](./LICENSE)). Review the agreement before distributing or embedding the binary or container image.

---

## Additional Resources

- [RBLN Device Plugin](https://github.com/rebellions-sw/rbln-k8s-device-plugin)
- [rbln-npu-feature-discovery](https://github.com/rebellions-sw/rbln-npu-feature-discovery)
- [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator)
- [Grafana Helm Charts](https://github.com/grafana/helm-charts)

Monitor confidently and keep your Rebellions NPUs healthy!
