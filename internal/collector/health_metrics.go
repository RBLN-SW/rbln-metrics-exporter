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

func NewDeviceHealthMetric(mapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceHealthMetric {
	return &DeviceHealthMetric{
		healthStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricName("RBLN_DEVICE_STATUS:HEALTH", "rbln_npu_health"),
			Help: "NPU readiness: 0 when READY, 1 for another known lifecycle state; absent when state is unknown",
		}, labelNames(includePodLabels)),
		podResourceMapper: mapper,
		NodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (m *DeviceHealthMetric) Register(reg prometheus.Registerer) { reg.MustRegister(m.healthStatus) }
func (m *DeviceHealthMetric) Reset()                             { m.healthStatus.Reset() }

func (m *DeviceHealthMetric) UpdateMetrics(_ context.Context, devices []daemon.DeviceInfo) {
	pods, _ := m.podResourceMapper.SnapshotFor(devices)
	for _, d := range devices {
		if value, known := d.Health(); known {
			m.healthStatus.With(buildLabels(d, m.NodeName, pods, m.includePodLabels)).Set(value)
		}
	}
}
