package collector

import (
	"context"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type ClockMetric struct {
	frequency         *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewClockMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *ClockMetric {
	labels := append(slices.Clone(labelNames(includePodLabels)), clock)
	return &ClockMetric{
		frequency: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_clock_frequency_mhz",
				Help: "Clock frequency of the labeled block (MHz)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (c *ClockMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(c.frequency)
}

func (c *ClockMetric) Reset() {
	c.frequency.Reset()
}

func (c *ClockMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := c.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, c.nodeName, podResourceInfo, c.includePodLabels)
		for clockName, mhz := range device.Clocks {
			labels[clock] = clockName
			c.frequency.With(labels).Set(mhz)
		}
	}
}
