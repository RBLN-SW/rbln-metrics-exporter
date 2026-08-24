package collector

import (
	"context"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

// SharedDeviceMetric marks the devices whose pod labels name only one of several
// claimants. DRA lets more than one pod reference a ResourceClaim, but a series
// carries a single set of labels, so such a device's telemetry is credited
// entirely to the claimant deviceMapFromPodResources picked. Without this signal
// a dashboard reads that partial attribution as exclusive; the warning the
// mapper logs is not visible to whoever is looking at the graph.
type SharedDeviceMetric struct {
	shared            *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
}

func NewSharedDeviceMetric(podResourceMapper *PodResourceMapper, nodeName string) *SharedDeviceMetric {
	return &SharedDeviceMetric{
		shared: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_device_shared",
				Help: "1 if the device is claimed by more than one pod, so its pod labels name only one of them",
			}, commonLabels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (s *SharedDeviceMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(s.shared)
}

func (s *SharedDeviceMetric) Reset() {
	s.shared.Reset()
}

func (s *SharedDeviceMetric) UpdateMetrics(_ context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo, sharedDevices := s.podResourceMapper.SharedSnapshot()

	// Every device reports, exclusive ones as 0, so that alerting on == 1 does
	// not have to distinguish "not shared" from "not collected".
	for _, device := range devices {
		labels := buildLabels(device, s.nodeName, podResourceInfo, true)
		value := 0.0
		if slices.Contains(sharedDevices, DeviceName(device.Name)) {
			value = 1.0
		}
		s.shared.With(labels).Set(value)
	}
}
