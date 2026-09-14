ARG GOLANG_VERSION=1.26.7
# Pinned to the build host: the Go toolchain cross-compiles for TARGETARCH
# below instead of running under QEMU when the image is built for arm64.
FROM --platform=$BUILDPLATFORM golang:${GOLANG_VERSION}-alpine AS builder
RUN apk add --no-cache build-base git

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ENV CGO_ENABLED=0
ARG VERSION
# Set by buildx per target platform (amd64, arm64); empty on a classic
# `docker build`, which then targets the host.
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOARCH=${TARGETARCH} go build -ldflags "-X github.com/rebellions-sw/rbln-metrics-exporter/internal/cmd.Version=${VERSION:-dev}" \
    -o /usr/local/bin/rbln-metrics-exporter ./cmd/rbln-metrics-exporter

FROM redhat/ubi9-minimal:9.8
ARG VERSION

RUN microdnf install -y shadow-utils && \
    groupadd -r rbln && \
    useradd -r -g rbln -d /home/rbln -s /sbin/nologin -c "RBLN Metrics Exporter user" rbln && \
    mkdir -p /home/rbln && \
    chown rbln:rbln /home/rbln && \
    microdnf clean all

COPY LICENSE /licenses/LICENSE.txt

LABEL \
    name="rbln-metrics-exporter" \
    vendor="Rebellions" \
    version="${VERSION}" \
    release="N/A" \
    summary="Rebellions Metrics Exporter" \
    description="The Metrics Exporter exposes detailed NPU device metrics in Prometheus format for visualization" \
    maintainer="Rebellions sw_devops@rebellions.ai" \
    io.k8s.display-name="Rebellions Metrics Exporter" \
    com.redhat.component="rbln-metrics-exporter"

COPY --from=builder /usr/local/bin/rbln-metrics-exporter /usr/local/bin/rbln-metrics-exporter
RUN chown rbln:rbln /usr/local/bin/rbln-metrics-exporter && \
    chmod 755 /usr/local/bin/rbln-metrics-exporter

USER rbln

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/rbln-metrics-exporter"]
