package collector

import (
	"context"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
)

var deviceStates = map[string]rblnservicespb.DeviceStatus{
	"ready":     rblnservicespb.DeviceStatus_READY,
	"busy":      rblnservicespb.DeviceStatus_BUSY,
	"init":      rblnservicespb.DeviceStatus_INIT,
	"fault":     rblnservicespb.DeviceStatus_FAULT,
	"finish":    rblnservicespb.DeviceStatus_FINISH,
	"not_found": rblnservicespb.DeviceStatus_NOT_FOUND,
}

type DeviceStateMetric struct {
	deviceStatus      *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewDeviceStateMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceStateMetric {
	labels := append(slices.Clone(labelNames(includePodLabels)), state)
	return &DeviceStateMetric{
		deviceStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_device_status",
				Help: "NPU device state machine status (1 = device is in the labeled state)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (d *DeviceStateMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.deviceStatus)
}

func (d *DeviceStateMetric) Reset() {
	d.deviceStatus.Reset()
}

func (d *DeviceStateMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, d.nodeName, podResourceInfo, d.includePodLabels)
		for stateName, stateValue := range deviceStates {
			labels[state] = stateName
			value := 0.0
			if device.DevState == stateValue {
				value = 1.0
			}
			d.deviceStatus.With(labels).Set(value)
		}
	}
}
