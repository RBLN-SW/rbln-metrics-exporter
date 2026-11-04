# SMD API bindings

`rbln_smd_v1.proto` defines the SMD v1 API used by the exporter. Generated Go
bindings live in `pkg/rblnservicesv1pb`.

The deployed SMD must implement Inventory, Metric and Topology services.
Inventory supplies `npu_id`, `is_vf` and `parent_name`; readiness monitoring
requires populated `state`. Missing inline static specs are retrieved through
`GetVersion` and `GetMemorySpec`. Telemetry comes from `SampleMetrics`.

After updating the schema, regenerate the bindings with:

```sh
protoc -I api \
  --go_out=. --go_opt=module=github.com/rebellions-sw/rbln-metrics-exporter \
  --go-grpc_out=. --go-grpc_opt=module=github.com/rebellions-sw/rbln-metrics-exporter \
  api/rbln_smd_v1.proto
go mod vendor
```
