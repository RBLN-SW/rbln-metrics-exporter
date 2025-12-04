package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type UtilizationCollector struct {
	utilization       *prometheus.GaugeVec
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	nodeName          string
}

func NewUtilizationCollector(dClient *daemon.Client, isKubernetes bool, podResourceMapper *PodResourceMapper, nodeName string) *UtilizationCollector {
	return &UtilizationCollector{
		utilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:UTILIZATION",
				Help: "Utilization (%)",
			}, commonLabels,
		),
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (u *UtilizationCollector) Register(reg prometheus.Registerer) {
	reg.MustRegister(u.utilization)
}

func (u *UtilizationCollector) GetMetrics(ctx context.Context) error {
	podResourceInfo := u.podResourceMapper.Snapshot()

	deviceStatus, err := u.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}

	u.utilization.Reset()

	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":             s.Card,
			"uuid":             s.UUID,
			"name":             s.Name,
			"deviceID":         s.DeviceID,
			"hostname":         u.nodeName,
			"driver_version":   s.DriverVersion,
			"firmware_version": s.FirmwareVersion,
			"smc_version":      s.SMCVersion,
			"namespace":        podResourceInfo[DeviceName(s.Name)].Namespace,
			"pod":              podResourceInfo[DeviceName(s.Name)].Name,
			"container":        podResourceInfo[DeviceName(s.Name)].ContainerName,
		}
		u.utilization.With(labels).Set(s.Utilization)
	}
	return nil
}
