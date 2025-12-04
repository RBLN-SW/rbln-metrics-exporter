package collector

import (
	"context"
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

const gibToBytes = 1 << 30

type MemoryCollector struct {
	dramUsed          *prometheus.GaugeVec
	dramTotal         *prometheus.GaugeVec
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	nodeName          string
}

func NewMemoryCollector(dClient *daemon.Client, isKubernetes bool, podResourceMapper *PodResourceMapper, nodeName string) *MemoryCollector {
	return &MemoryCollector{
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
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (m *MemoryCollector) Register(reg prometheus.Registerer) {
	reg.MustRegister(m.dramUsed)
	reg.MustRegister(m.dramTotal)
}

func (m *MemoryCollector) GetMetrics(ctx context.Context) error {
	podResourceInfo := m.podResourceMapper.Snapshot()

	deviceStatus, err := m.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}

	m.dramUsed.Reset()
	m.dramTotal.Reset()

	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":             s.Card,
			"uuid":             s.UUID,
			"name":             s.Name,
			"deviceID":         s.DeviceID,
			"hostname":         m.nodeName,
			"driver_version":   s.DriverVersion,
			"firmware_version": s.FirmwareVersion,
			"smc_version":      s.SMCVersion,
			"namespace":        podResourceInfo[DeviceName(s.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(s.Name)].Name,
			"container":        podResourceInfo[DeviceName(s.Name)].ContainerName,
		}

		bytesUsed := uint64(math.Round(s.DRAMUsedGiB * float64(gibToBytes)))
		bytesTotal := uint64(math.Round(s.DRAMTotalGiB * float64(gibToBytes)))

		m.dramUsed.With(labels).Set(float64(bytesUsed))
		m.dramTotal.With(labels).Set(float64(bytesTotal))
	}
	return nil
}
