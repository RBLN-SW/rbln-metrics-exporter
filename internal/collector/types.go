package collector

import (
	"context"
	"os"

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

func metricName(legacy, current string) string {
	if os.Getenv("PROMETHEUS_METRIC_NAMES") == "true" {
		return current
	}
	return legacy
}

func NewUpGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rbln_up",
		Help: "1 if the last metrics collection from rbln-smd succeeded, 0 otherwise",
	})
}
