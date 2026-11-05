package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type PCIeLinkMetric struct {
	linkSpeed         *prometheus.GaugeVec
	linkWidth         *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewPCIeLinkMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *PCIeLinkMetric {
	labels := labelNames(includePodLabels)
	return &PCIeLinkMetric{
		linkSpeed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_pcie_link_speed_gts",
				Help: "Current PCIe link speed (GT/s)",
			}, labels,
		),
		linkWidth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_pcie_link_width",
				Help: "Current PCIe link width (lanes)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (p *PCIeLinkMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(p.linkSpeed)
	registerer.MustRegister(p.linkWidth)
}

func (p *PCIeLinkMetric) Reset() {
	p.linkSpeed.Reset()
	p.linkWidth.Reset()
}

func (p *PCIeLinkMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo, _ := p.podResourceMapper.SnapshotFor(devices)

	for _, device := range devices {
		if device.IsVF {
			continue
		}
		labels := buildLabels(device, p.nodeName, podResourceInfo, p.includePodLabels)
		if device.PCIeLinkSpeed != nil {
			p.linkSpeed.With(labels).Set(*device.PCIeLinkSpeed)
		}
		if device.PCIeLinkWidth != nil {
			p.linkWidth.With(labels).Set(float64(*device.PCIeLinkWidth))
		}
	}
}
