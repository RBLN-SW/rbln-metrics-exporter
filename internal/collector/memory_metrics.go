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
}

func NewMemoryMetric(podResourceMapper *PodResourceMapper, nodeName string) *MemoryMetric {
	return &MemoryMetric{
		dramUsed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:DRAM_USED",
				Help: "DRAM used (bytes)",
			}, commonLabels,
		),
		dramTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:DRAM_TOTAL",
				Help: "DRAM total (bytes)",
			}, commonLabels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
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
		labels := prometheus.Labels{
			"card":             device.Card,
			"uuid":             device.UUID,
			"name":             device.Name,
			"deviceID":         device.DeviceID,
			"hostname":         m.nodeName,
			"driver_version":   device.DriverVersion,
			"firmware_version": device.FirmwareVersion,
			"namespace":        podResourceInfo[DeviceName(device.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(device.Name)].Name,
			"container":        podResourceInfo[DeviceName(device.Name)].ContainerName,
		}

		bytesUsed := uint64(math.Round(device.DRAMUsedGiB * float64(gibToBytes)))
		bytesTotal := uint64(math.Round(device.DRAMTotalGiB * float64(gibToBytes)))

		m.dramUsed.With(labels).Set(float64(bytesUsed))
		m.dramTotal.With(labels).Set(float64(bytesTotal))
	}
}
