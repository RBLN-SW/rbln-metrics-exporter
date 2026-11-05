package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type MemoryMetric struct {
	dramUsed          *prometheus.GaugeVec
	dramTotal         *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewMemoryMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *MemoryMetric {
	labels := labelNames(includePodLabels)
	return &MemoryMetric{
		dramUsed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:DRAM_USED", "rbln_npu_memory_used"),
				Help: "DRAM used (bytes)",
			}, labels,
		),
		dramTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:DRAM_TOTAL", "rbln_npu_memory_total"),
				Help: "DRAM total (bytes)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (m *MemoryMetric) Register(reg prometheus.Registerer) {
	reg.MustRegister(m.dramUsed)
	reg.MustRegister(m.dramTotal)
}

func (m *MemoryMetric) Reset() {
	m.dramUsed.Reset()
	m.dramTotal.Reset()
}

func (m *MemoryMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo, _ := m.podResourceMapper.SnapshotFor(devices)

	for _, device := range devices {
		labels := buildLabels(device, m.nodeName, podResourceInfo, m.includePodLabels)

		if !device.Compute {
			continue
		}
		if device.MemoryUsedBytes != nil {
			m.dramUsed.With(labels).Set(float64(*device.MemoryUsedBytes))
		}
		if device.MemoryTotalBytes != nil {
			m.dramTotal.With(labels).Set(float64(*device.MemoryTotalBytes))
		}
	}
}
