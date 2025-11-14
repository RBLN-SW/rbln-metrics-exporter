# RBLN Metrics Exporter

This repository contains Rebellions' NPU metrics exporter for [Prometheus](https://prometheus.io/).

## Prerequisites

- RBLN driver >= 1.3.40

## Command line interface

```bash
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
```

## Metrics

The below metrics are exported for each NPU devices, labeled with device UUID, card name and character device node (`rblnN`).

|Name|Description|
|----|-----------|
|`RBLN_DEVICE_STATUS:TEMPERATURE`|temperature (°C)|
|`RBLN_DEVICE_STATUS:CARD_POWER`|power usages (W)|
|`RBLN_DEVICE_STATUS:DRAM_USED`|DRAM in use (GiB)|
|`RBLN_DEVICE_STATUS:DRAM_TOTAL`|DRAM total (GiB)|
|`RBLN_DEVICE_STATUS:UTILIZATION`|utilization (%)|
