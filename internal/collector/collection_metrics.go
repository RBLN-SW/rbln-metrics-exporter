package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type DeviceCollectionMetric struct {
	success           *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewDeviceCollectionMetric(mapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceCollectionMetric {
	return &DeviceCollectionMetric{
		success: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "rbln_npu_collection_success",
			Help: "1 if this inventoried device's metadata, topology and telemetry queries succeeded, 0 on a reported per-device failure; not hardware health or metric availability",
		}, labelNames(includePodLabels)),
		podResourceMapper: mapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (m *DeviceCollectionMetric) Register(reg prometheus.Registerer) { reg.MustRegister(m.success) }
func (m *DeviceCollectionMetric) Reset()                             { m.success.Reset() }

func (m *DeviceCollectionMetric) UpdateMetrics(_ context.Context, devices []daemon.DeviceInfo) {
	pods, _ := m.podResourceMapper.SnapshotFor(devices)
	for _, d := range devices {
		value := 0.0
		if d.CollectionSucceeded {
			value = 1
		}
		m.success.With(buildLabels(d, m.nodeName, pods, m.includePodLabels)).Set(value)
	}
}
