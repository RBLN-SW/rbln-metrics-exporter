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
}

func NewHardwareInfoMetric(podResourceMapper *PodResourceMapper, nodeName string) *HardwareInfoMetric {
	return &HardwareInfoMetric{
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
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
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
		labels := prometheus.Labels{
			"card":             device.Card,
			"uuid":             device.UUID,
			"name":             device.Name,
			"deviceID":         device.DeviceID,
			"hostname":         h.NodeName,
			"driver_version":   device.DriverVersion,
			"firmware_version": device.FirmwareVersion,
			"namespace":        podResourceInfo[DeviceName(device.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(device.Name)].Name,
			"container":        podResourceInfo[DeviceName(device.Name)].ContainerName,
		}
		h.temperature.With(labels).Set(device.Temperature)
		h.power.With(labels).Set(device.Power)
	}
}
