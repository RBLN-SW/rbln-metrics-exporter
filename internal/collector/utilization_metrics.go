package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type UtilizationMetric struct {
	utilization       *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
}

func NewUtilizationMetric(podResourceMapper *PodResourceMapper, nodeName string) *UtilizationMetric {
	return &UtilizationMetric{
		utilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:UTILIZATION",
				Help: "Utilization (%)",
			}, commonLabels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (u *UtilizationMetric) Register(reg prometheus.Registerer) {
	reg.MustRegister(u.utilization)
}

func (u *UtilizationMetric) Reset() {
	u.utilization.Reset()
}

func (u *UtilizationMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := u.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := prometheus.Labels{
			"card":             device.Card,
			"uuid":             device.UUID,
			"name":             device.Name,
			"deviceID":         device.DeviceID,
			"hostname":         u.nodeName,
			"driver_version":   device.DriverVersion,
			"firmware_version": device.FirmwareVersion,
			"namespace":        podResourceInfo[DeviceName(device.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(device.Name)].Name,
			"container":        podResourceInfo[DeviceName(device.Name)].ContainerName,
		}
		u.utilization.With(labels).Set(device.Utilization)
	}
}
