package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type HardwareInfoMetric struct {
	temperature       *prometheus.GaugeVec
	power             *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	NodeName          string
	includePodLabels  bool
}

func NewHardwareInfoMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *HardwareInfoMetric {
	labels := labelNames(includePodLabels)
	return &HardwareInfoMetric{
		temperature: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:TEMPERATURE",
				Help: "NPU temperature (C)",
			}, labels,
		),
		power: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:CARD_POWER",
				Help: "Card power usage (W)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (h *HardwareInfoMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(h.temperature)
	registerer.MustRegister(h.power)
}

func (h *HardwareInfoMetric) Reset() {
	h.temperature.Reset()
	h.power.Reset()
}

func (h *HardwareInfoMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := h.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, h.NodeName, podResourceInfo, h.includePodLabels)
		h.temperature.With(labels).Set(device.Temperature)
		h.power.With(labels).Set(device.Power)
	}
}
