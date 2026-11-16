# Metrics in this checkout

The [published metrics reference](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_reference.html)
lists metric names, units, and common labels for released versions. This page
supplements it with collection semantics and device labels in this checkout.
For an installed release, read the documentation at the matching Git tag.

For legacy aliases and `PROMETHEUS_METRIC_NAMES`, see [metric-name settings](configuration.md#metric-names).

## Collection and health

| Metric | Meaning |
| --- | --- |
| `rbln_up` | `1` when daemon collection succeeds; `0` on a collection error or before the first local collection |
| `rbln_npu_collection_success` | Per device: `1` when metadata, topology, and telemetry queries succeed; `0` on a reported device query failure |
| `rbln_npu_health` | `0` for READY; `1` for another known lifecycle state, including BUSY; absent when the state is unknown |
| `rbln_npu_device_status` | One series per `state`; the current state has value `1`. `not_found` means no recognized state is available |

In local mode, `rbln_up` describes the last scheduled collection. In gateway mode,
it describes collection for the current scrape. It can be `1` while a device
reports `rbln_npu_collection_success=0`; check both levels of collection status.

Successful queries do not guarantee that every sensor reading is available or
that the device is READY. Unavailable v1 readings are omitted. The v0 scalar API
cannot distinguish absent readings from measured zero.

## Device identity and SR-IOV

`rbln_npu_device_info` includes these labels in addition to those in the published
reference:

| Label | Meaning |
| --- | --- |
| `compute` | `true` for a compute device eligible for memory/utilization metrics and workload attribution; `false` for a PF hosting VFs |
| `partition_mode` | `none` for an unpartitioned compute PF, `hosting` for a PF hosting VFs, or `vf1`/`vf4` for a VF with a known parent profile; empty when unknown |

The existing `num_vfs` label is empty when the count is unknown. It counts VFs
enabled on that function, so a VF reports `0`. VF profiles require local PCI sysfs
access and are unavailable in gateway mode.

Use `hostname` and `name` to distinguish functions; a VF may inherit its PF's UUID.
Memory and utilization are exported for compute devices. Temperature, power, and
PCIe link readings remain PF-only. See [Check NPU metrics](../guides/check-metrics.md#3-check-workload-attribution)
for Kubernetes workload labels and shared-device attribution.
