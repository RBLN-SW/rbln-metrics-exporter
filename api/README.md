# SMD API bindings

`rbln_smd_v1.proto` defines the preferred API, with bindings in
`pkg/rblnservicesv1pb`. `rbln_services.proto` and `pkg/rblnservicespb` provide
v0 compatibility when required v1 RPCs are unimplemented, and supply missing
v1 lifecycle state through `getTotalInfo.dev_status`.

The v1 path uses Inventory, Metric and Topology services.
Inventory supplies `npu_id`, `is_vf` and `parent_name`; readiness monitoring
uses v1 `state` when known, otherwise v0 state when available. Missing inline
static specs are retrieved through `GetVersion` and `GetMemorySpec`.
Telemetry comes from `SampleMetrics`.

After updating the schema, regenerate the bindings with:

```sh
protoc -I api \
  --go_out=. --go_opt=module=github.com/rebellions-sw/rbln-metrics-exporter \
  --go-grpc_out=. --go-grpc_opt=module=github.com/rebellions-sw/rbln-metrics-exporter \
  api/rbln_services.proto api/rbln_smd_v1.proto
go mod vendor
```
