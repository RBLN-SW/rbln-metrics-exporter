package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type HardwareInfoCollector struct {
	temperature *prometheus.GaugeVec
	power       *prometheus.GaugeVec
	dClient     *daemon.Client
}

func NewHardwareInfoCollector(dClient *daemon.Client) *HardwareInfoCollector {
	return &HardwareInfoCollector{
		temperature: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:TEMPERATURE",
				Help: "NPU temperature (C)",
			}, commonLabels,
		),
		power: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:CARD_POWER",
				Help: "Card power usage (W)",
			}, commonLabels,
		),
		dClient: dClient,
	}
}

func (h *HardwareInfoCollector) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(h.temperature)
	registerer.MustRegister(h.power)
}

func (h *HardwareInfoCollector) GetMetrics(ctx context.Context) error {
	deviceStatus, err := h.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}
	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":   s.Card,
			"uuid":   s.UUID,
			"device": s.DeviceNode,
		}
		h.temperature.With(labels).Set(s.Temperature)
		h.power.With(labels).Set(s.Power)
	}
	return nil
}
