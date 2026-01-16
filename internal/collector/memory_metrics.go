package collector

import (
	"context"
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

const gibToBytes = 1 << 30

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
				Name: "RBLN_DEVICE_STATUS:DRAM_USED",
				Help: "DRAM used (bytes)",
			}, labels,
		),
		dramTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:DRAM_TOTAL",
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
	podResourceInfo := m.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, m.nodeName, podResourceInfo, m.includePodLabels)

		bytesUsed := uint64(math.Round(device.DRAMUsedGiB * float64(gibToBytes)))
		bytesTotal := uint64(math.Round(device.DRAMTotalGiB * float64(gibToBytes)))

		m.dramUsed.With(labels).Set(float64(bytesUsed))
		m.dramTotal.With(labels).Set(float64(bytesTotal))
	}
}
