package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type DeviceHealthMetric struct {
	healthStatus      *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	NodeName          string
}

func NewDeviceHealthMetric(podResourceMapper *PodResourceMapper, nodeName string) *DeviceHealthMetric {
	return &DeviceHealthMetric{
		healthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:HEALTH",
				Help: "NPU health status",
			}, commonLabels,
		),
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
	}
}

func (d *DeviceHealthMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.healthStatus)
}

func (d *DeviceHealthMetric) Reset() {
	d.healthStatus.Reset()
}

func (d *DeviceHealthMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := prometheus.Labels{
			"card":             device.Card,
			"uuid":             device.UUID,
			"name":             device.Name,
			"deviceID":         device.DeviceID,
			"hostname":         d.NodeName,
			"driver_version":   device.DriverVersion,
			"firmware_version": device.FirmwareVersion,
			"namespace":        podResourceInfo[DeviceName(device.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(device.Name)].Name,
			"container":        podResourceInfo[DeviceName(device.Name)].ContainerName,
		}
		d.healthStatus.With(labels).Set(float64(device.DeviceStatus))
	}
}
