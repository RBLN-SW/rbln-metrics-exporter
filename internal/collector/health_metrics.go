package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type DeviceHealthMetric struct {
	healthStatus      *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	NodeName          string
	includePodLabels  bool
}

func NewDeviceHealthMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceHealthMetric {
	labels := labelNames(includePodLabels)
	return &DeviceHealthMetric{
		healthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:HEALTH",
				Help: "NPU health status",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (d *DeviceHealthMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.healthStatus)
}

func (d *DeviceHealthMetric) Reset() {
	d.healthStatus.Reset()
}

func (d *DeviceHealthMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, d.NodeName, podResourceInfo, d.includePodLabels)
		d.healthStatus.With(labels).Set(float64(device.DeviceStatus))
	}
}
