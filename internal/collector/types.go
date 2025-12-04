package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type Collector interface {
	Register(prometheus.Registerer)
	GetMetrics(context.Context) error
}

type Metric interface {
	Register(prometheus.Registerer)
	UpdateMetrics(context.Context, []daemon.DeviceInfo)
	Reset()
}
