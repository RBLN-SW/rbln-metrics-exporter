package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type PowerStateMetric struct {
	powerState        *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewPowerStateMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *PowerStateMetric {
	labels := labelNames(includePodLabels)
	return &PowerStateMetric{
		powerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_power_state",
				Help: "DVFS performance state (0 = highest performance)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (p *PowerStateMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(p.powerState)
}

func (p *PowerStateMetric) Reset() {
	p.powerState.Reset()
}

func (p *PowerStateMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := p.podResourceMapper.Snapshot()

	for _, device := range devices {
		if device.PState < 0 {
			continue
		}
		labels := buildLabels(device, p.nodeName, podResourceInfo, p.includePodLabels)
		p.powerState.With(labels).Set(float64(device.PState))
	}
}
