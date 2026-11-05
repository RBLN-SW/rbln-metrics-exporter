package collector

import (
	"context"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

var deviceStates = map[string]pb.DeviceState{
	"ready":     pb.DeviceState_DEVICE_STATE_READY,
	"busy":      pb.DeviceState_DEVICE_STATE_BUSY,
	"init":      pb.DeviceState_DEVICE_STATE_INIT,
	"fault":     pb.DeviceState_DEVICE_STATE_FAULT,
	"finish":    pb.DeviceState_DEVICE_STATE_FINISH,
	"not_found": pb.DeviceState_DEVICE_STATE_UNSPECIFIED,
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
				Help: "NPU device state (1 = current state; not_found = no recognized state reported by SMD)",
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
	podResourceInfo, _ := d.podResourceMapper.SnapshotFor(devices)

	for _, device := range devices {
		if _, known := pb.DeviceState_name[int32(device.DevState)]; !known {
			device.DevState = pb.DeviceState_DEVICE_STATE_UNSPECIFIED
		}
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
