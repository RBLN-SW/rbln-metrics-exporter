package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type DeviceHealthCollector struct {
	healthStatus      *prometheus.GaugeVec
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	NodeName          string
}

func NewDeviceHealthCollector(dClient *daemon.Client, isKubernetes bool, podResourceMapper *PodResourceMapper, nodeName string) *DeviceHealthCollector {
	return &DeviceHealthCollector{
		healthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:HEALTH",
				Help: "NPU health status",
			}, commonLabels,
		),
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
	}
}

func (d *DeviceHealthCollector) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.healthStatus)
}

func (d *DeviceHealthCollector) GetMetrics(ctx context.Context) error {
	podResourceInfo := d.podResourceMapper.Snapshot()

	deviceStatus, err := d.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}

	d.healthStatus.Reset()

	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":             s.Card,
			"uuid":             s.UUID,
			"name":             s.Name,
			"deviceID":         s.DeviceID,
			"hostname":         d.NodeName,
			"driver_version":   s.DriverVersion,
			"firmware_version": s.FirmwareVersion,
			"smc_version":      s.SMCVersion,
			"namespace":        podResourceInfo[DeviceName(s.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(s.Name)].Name,
			"container":        podResourceInfo[DeviceName(s.Name)].ContainerName,
		}
		d.healthStatus.With(labels).Set(float64(s.DeviceStatus))
	}
	return nil
}
