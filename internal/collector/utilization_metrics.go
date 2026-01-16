package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type UtilizationMetric struct {
	utilization       *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewUtilizationMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *UtilizationMetric {
	labels := labelNames(includePodLabels)
	return &UtilizationMetric{
		utilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:UTILIZATION",
				Help: "Utilization (%)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (u *UtilizationMetric) Register(reg prometheus.Registerer) {
	reg.MustRegister(u.utilization)
}

func (u *UtilizationMetric) Reset() {
	u.utilization.Reset()
}

func (u *UtilizationMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := u.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, u.nodeName, podResourceInfo, u.includePodLabels)
		u.utilization.With(labels).Set(device.Utilization)
	}
}
