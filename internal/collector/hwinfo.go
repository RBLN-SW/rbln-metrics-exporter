package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type HardwareInfoCollector struct {
	temperature       *prometheus.GaugeVec
	power             *prometheus.GaugeVec
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	NodeName          string
}

func NewHardwareInfoCollector(dClient *daemon.Client, isKubernetes bool, podResourceMapper *PodResourceMapper, nodeName string) *HardwareInfoCollector {
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
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
	}
}

func (h *HardwareInfoCollector) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(h.temperature)
	registerer.MustRegister(h.power)
}

func (h *HardwareInfoCollector) GetMetrics(ctx context.Context) error {
	podResourceInfo := h.podResourceMapper.Snapshot()

	deviceStatus, err := h.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}

	h.temperature.Reset()
	h.power.Reset()

	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":             s.Card,
			"uuid":             s.UUID,
			"name":             s.Name,
			"deviceID":         s.DeviceID,
			"hostname":         h.NodeName,
			"driver_version":   s.DriverVersion,
			"firmware_version": s.FirmwareVersion,
			"smc_version":      s.SMCVersion,
			"namespace":        podResourceInfo[DeviceName(s.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(s.Name)].Name,
			"container":        podResourceInfo[DeviceName(s.Name)].ContainerName,
		}
		h.temperature.With(labels).Set(s.Temperature)
		h.power.With(labels).Set(s.Power)
	}
	return nil
}
