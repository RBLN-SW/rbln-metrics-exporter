# Connect the exporter to rbln-smd

Use this guide when collection reports `rbln_up 0` or the daemon requires TLS
or restricted metric-group access. The examples assume a binary built by
the [local setup guide](run-locally.md).

## 1. Select the endpoint

Local mode uses `--rbln-daemon-url=<host:port>`. For a process or host-network
container on the NPU host, the default is `127.0.0.1:50051`.
Gateway mode selects the daemon per HTTP request using `?target=<host:port>`;
see the [gateway setup](https://docs.rbln.ai/latest/software/system_management/kubernetes/metrics_exporter_gateway_mode.html).

Use a gRPC address without an HTTP scheme. An `https://` prefix alone does not
enable TLS; configure the TLS options below. With no TLS options, the daemon
must accept plaintext connections.

## 2. Configure TLS when required

For a daemon using mTLS, provide files readable by the exporter:

```bash
PROMETHEUS_METRIC_NAMES=true ./bin/rbln-metrics-exporter \
  --mode=local --kubernetes-mode=off \
  --rbln-daemon-url=<daemon-host>:50051 \
  --smd-tls-ca=<ca-pem-path> \
  --smd-tls-cert=<client-cert-pem-path> \
  --smd-tls-key=<client-key-pem-path>
```

For server-authenticated TLS, supply the CA without a client certificate/key.
For mTLS, the client certificate and key must be supplied together. In a container,
mount the files and use their container paths; flags alone do not mount them.

The equivalent environment variables are `RBLN_METRICS_EXPORTER_SMD_TLS_CA`,
`RBLN_METRICS_EXPORTER_SMD_TLS_CERT`, and `RBLN_METRICS_EXPORTER_SMD_TLS_KEY`.

## 3. Check metric-group permissions

The v1 collection path uses a dedicated clock-free metric group and creates it
when absent by default. If the daemon account cannot create groups, ask the daemon
administrator to provision a suitable group and use:

```text
--metric-group-id=<existing-group-id> --create-metric-group=false
```

The group ID must be non-empty and cannot be `default`. Environment equivalents
are `RBLN_METRICS_EXPORTER_METRIC_GROUP_ID` and
`RBLN_METRICS_EXPORTER_CREATE_METRIC_GROUP`. Omitting the ID uses the exporter's
built-in dedicated group ID.

The exporter prefers SMD v1 and supports v0 fallback when required v1 RPCs are
unimplemented. Authentication or permission failures still need to be resolved;
see the [API compatibility notes](../../api/README.md).

## 4. Verify collection

```bash
curl --fail http://127.0.0.1:9090/metrics
```

After a collection cycle, expect `rbln_up 1`. In gateway mode, add the intended
`?target=<host:port>` to the URL. If collection fails, inspect exporter logs for
connection, certificate, or permission errors. Then [check per-device metrics](check-metrics.md).
