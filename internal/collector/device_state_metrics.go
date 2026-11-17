package collector

import (
	"context"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

var deviceStates = map[string]system.State{
	"ready":     system.StateReady,
	"busy":      system.StateBusy,
	"init":      system.StateInit,
	"fault":     system.StateFault,
	"finish":    system.StateFinish,
	"not_found": system.StateUnknown,
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
		if !device.HasKnownState() {
			device.DevState = system.StateUnknown
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
