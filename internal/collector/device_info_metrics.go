package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type DeviceInfoMetric struct {
	deviceInfo        *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewDeviceInfoMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceInfoMetric {
	labels := deviceInfoLabelNames(includePodLabels)
	return &DeviceInfoMetric{
		deviceInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_device_info",
				Help: "Device identity and static attributes as labels (value is always 1)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (d *DeviceInfoMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.deviceInfo)
}

func (d *DeviceInfoMetric) Reset() {
	d.deviceInfo.Reset()
}

func (d *DeviceInfoMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildDeviceInfoLabels(device, d.nodeName, podResourceInfo, d.includePodLabels)
		d.deviceInfo.With(labels).Set(1)
	}
}
