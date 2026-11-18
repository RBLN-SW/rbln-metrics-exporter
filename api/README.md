# SMD compatibility test fixtures

Production uses `rblnlib-go/pkg/smd/v1` and `pkg/system/v1` exclusively. The
schemas here are retained as independent server fixtures for the existing
exporter integration tests; they are not production client bindings.

Run `PATH=/path/to/protoc-plugins:$PATH python3 hack/generate-test-proto.py`
with protoc, protoc-gen-go v1.36.11 and protoc-gen-go-grpc v1.5.1. The generator
uses private descriptor namespaces/file names to coexist with the SDK in one
process, removes the test-only RPC option extension, and preserves every wire
field number and real gRPC service/method path. Generated test code lives in
`internal/smdtest/wire`; integration tests still exercise real TLS/gRPC.
