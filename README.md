# RBLN Metrics Exporter

The RBLN Metrics Exporter exposes detailed telemetry for RBLN NPUs in [Prometheus](https://prometheus.io/) format so that you can build Grafana dashboards, alert on thermal or utilization anomalies, and correlate accelerator health with Kubernetes workloads.

---

## Key Features

- **Native Prometheus endpoint** on `/metrics` served by a lightweight Go HTTP server.
- **Two collection modes**: `local` (default) collects from the node-local RBLN daemon on a schedule; `gateway` follows the Prometheus [multi-target exporter pattern](https://prometheus.io/docs/guides/multi-target-exporter/) so a single central instance can collect from many remote hosts — see [Gateway Mode](#gateway-mode-multi-target).
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
      --kubernetes-mode string   Kubernetes mode: auto, on, off (default "auto")
      --mode string              Exporter mode: local (collect from the local daemon on a schedule), gateway (collect from the rbln-smd given by /metrics?target=<host:port> on each scrape) (default "local")
      --node-name string         Name of the node (defaults to NODE_NAME env or hostname)
      --oneshot                  Collect once and exit
      --port int                 Port to listen for requests (default 9090)
      --rbln-daemon-url string   Endpoint to RBLN daemon grpc server (local mode only) (default "127.0.0.1:50051")
```

### Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `RBLN_METRICS_EXPORTER_MODE` | `local` | Exporter mode: `local` or `gateway` |
| `RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL` | `127.0.0.1:50051` | gRPC endpoint of the RBLN daemon (local mode only) |
| `RBLN_METRICS_EXPORTER_PORT` | `9090` | Port for the `/metrics` HTTP server |
| `RBLN_METRICS_EXPORTER_INTERVAL` | `5` | Collection interval in seconds (1–60) |
| `RBLN_METRICS_EXPORTER_KUBERNETES_MODE` | `auto` | Kubernetes integration: `auto`, `on`, or `off` |
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

## Gateway Mode (Multi-Target)

Gateway mode turns a single exporter instance into a stateless collection gateway for many hosts, following the Prometheus [multi-target exporter pattern](https://prometheus.io/docs/guides/multi-target-exporter/) (the same design as `blackbox_exporter` and `snmp_exporter`). It is intended for bare-metal / non-Kubernetes fleets where running one exporter per host is impractical.

|  | `local` (default) | `gateway` |
| --- | --- | --- |
| Deployment | One per node (DaemonSet) | One central instance |
| Collection | Node-local daemon, on a schedule | Remote `rbln-smd` given per scrape, on demand |
| Target selection | None (own node) | `?target=<host:port>` query parameter |
| Kubernetes pod labels | Yes | No (hardware metrics only) |

### How It Works

Each Prometheus scrape of `/metrics?target=<host:port>` makes the gateway connect to that host's `rbln-smd` over gRPC, collect the full metric set on the spot, and answer in Prometheus format. The exporter keeps no metric state between requests and holds no target list — targets live entirely in the Prometheus scrape configuration. gRPC connections are cached per target and reconnect automatically.

Every response includes `rbln_up`: `1` when the target daemon answered, `0` when it was unreachable (the HTTP scrape itself still succeeds, so a dead NPU host is reported rather than hidden).

### Running the Gateway

```bash
$ rbln-metrics-exporter --mode=gateway --port=9200
```

Verify with a manual scrape (the target is the `rbln-smd` gRPC address as reachable *from the gateway*):

```bash
$ curl "http://localhost:9200/metrics?target=npu-host-1:50051"
```

### Prometheus Configuration

Operators list the NPU hosts as targets; relabeling turns each one into a `?target=` parameter and redirects the actual request to the gateway:

```yaml
scrape_configs:
  - job_name: rbln-npu-gateway
    metrics_path: /metrics
    static_configs:
      - targets:
          - npu-host-1:50051   # rbln-smd gRPC address on each NPU host
          - npu-host-2:50051
    relabel_configs:
      # 1. Copy the listed address into the ?target= query parameter.
      - source_labels: [__address__]
        target_label: __param_target
      # 2. Keep it as the instance label so series stay distinguishable per host.
      - source_labels: [__param_target]
        target_label: instance
      # 3. Send the actual HTTP request to the gateway instead of the NPU host.
      - target_label: __address__
        replacement: rbln-gateway:9200   # gateway exporter address
```

With this in place Prometheus scrapes `http://rbln-gateway:9200/metrics?target=npu-host-1:50051` for each host and stores every series with `instance="npu-host-1:50051"`. Rule order matters: the copy rules must run before `__address__` is overwritten.

### Alerting

Two failure signals exist and mean different things:

| Expression | Meaning |
| --- | --- |
| `up == 0` | The gateway itself (or the network path to it) is down — affects all targets |
| `up == 1 and rbln_up == 0` | That specific NPU host's `rbln-smd` is unreachable |

### Notes and Limitations

- Collection happens inside the scrape request, so slow targets consume scrape time. The gateway honors the `X-Prometheus-Scrape-Timeout-Seconds` header Prometheus sends (10s cap by default) — raise `scrape_timeout` for slow links.
- Pod/namespace/container labels are not available: the kubelet pod-resources API is node-local and cannot describe remote hosts. Use `local` mode (DaemonSet) on Kubernetes clusters.
- The `hostname` label is filled with the host part of the target address.
- The gRPC connection to targets is plaintext; ensure the daemon port is reachable from the gateway and restricted to trusted networks.

---

## Metrics Reference

| Name | Description | Unit |
| --- | --- | --- |
| `rbln_npu_temperature` | Device temperature | °C |
| `rbln_npu_power` | Card power draw | W |
| `rbln_npu_memory_used` | DRAM currently in use | bytes |
| `rbln_npu_memory_total` | Total DRAM | bytes |
| `rbln_npu_utilization` | SM utilization | % |
| `rbln_npu_health` | Binary health (0 = active, 1 = inactive) | 0/1 |
| `rbln_npu_device_status` | Device state machine status; one series per `state`, 1 marks the current state | 0/1 |
| `rbln_npu_power_state` | DVFS performance state (0 = highest performance); absent when the daemon has no reading | ordinal |
| `rbln_npu_pcie_link_speed_gts` | Current PCIe link speed | GT/s |
| `rbln_npu_pcie_link_width` | Current PCIe link width | lanes |
| `rbln_npu_device_info` | Device identity and static attributes as labels | always 1 |
| `rbln_up` | 1 if the last metrics collection from `rbln-smd` succeeded, 0 otherwise (local mode: the last scheduled collection cycle; gateway mode: the collection performed for this scrape) | 0/1 |

### Common Label Set

| Label | Description |
| --- | --- |
| `name` | Character device node (`rbln0`, `rbln1`, …) |
| `uuid` | Unique NPU UUID |
| `card` | Marketing card name (e.g., `RBLN-CA25`) |
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
# TYPE rbln_npu_memory_total gauge
rbln_npu_memory_total{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln0",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="55668c63-d739-4193-8212-ad7ba933520c"} 16877882368
# TYPE rbln_npu_temperature gauge
rbln_npu_temperature{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln1",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="84389d45-ebf3-4b74-9d80-6ec8a09d8be4"} 54
# HELP rbln_npu_health NPU health status
rbln_npu_health{card="RBLN-CA25",container="ubuntu",deviceID="1250",driver_version="2.0.1",firmware_version="2.0.1",hostname="sw-mpc-clsdk-bm-worker-01",name="rbln3",namespace="default",pod="rebel-device-plugin-testpod-1",smc_version="15.10.13.14",uuid="8e65fc0d-df7d-4e21-a81b-a76a1a1e69ab"} 0
```

---

## Troubleshooting

| Symptom | Possible Cause | Action |
| --- | --- | --- |
| `rbln_up` is `0` and NPU metrics are absent | Unable to reach RBLN daemon | Verify `RBLN_METRICS_EXPORTER_RBLN_DAEMON_URL`, ensure daemon is listening, check firewall |
| No Kubernetes labels | Pod-resources socket missing | Confirm `/var/lib/kubelet/pod-resources/kubelet.sock` is mounted and kubelet exposes the API |
| Scrape errors in Prometheus | Authorization/namespace mismatch | Ensure Service or ServiceMonitor selects the exporter pods and Prometheus is allowed to scrape the namespace |
| Gateway returns HTTP 400 | Missing `?target=` parameter | Check the `relabel_configs` copy rules run before `__address__` is replaced |
| `rbln_up 0` for one target | That host's `rbln-smd` unreachable from the gateway | Verify the daemon is running on the target and its gRPC port is reachable from the gateway host |
| Prometheus scrapes the NPU host directly | `__address__` replacement rule missing | Add the final relabel rule pointing `__address__` at the gateway |

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
