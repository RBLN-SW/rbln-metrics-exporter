package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type MemoryCollector struct {
	dramUsed  *prometheus.GaugeVec
	dramTotal *prometheus.GaugeVec
	dClient   *daemon.Client
}

func NewMemoryCollector(dClient *daemon.Client) *MemoryCollector {
	return &MemoryCollector{
		dramUsed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:DRAM_USED",
				Help: "DRAM used (GiB)",
			}, commonLabels,
		),
		dramTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:DRAM_TOTAL",
				Help: "DRAM total (GiB)",
			}, commonLabels,
		),
		dClient: dClient,
	}
}

func (m *MemoryCollector) Register(reg prometheus.Registerer) {
	reg.MustRegister(m.dramUsed)
	reg.MustRegister(m.dramTotal)
}

func (m *MemoryCollector) GetMetrics(ctx context.Context) error {
	deviceStatus, err := m.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}
	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":   s.Card,
			"uuid":   s.UUID,
			"device": s.DeviceNode,
		}
		m.dramUsed.With(labels).Set(s.DRAMUsedGiB)
		m.dramTotal.With(labels).Set(s.DRAMTotalGiB)
	}
	return nil
}
