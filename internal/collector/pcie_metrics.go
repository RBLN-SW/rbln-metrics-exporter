package collector

import (
	"context"
	"strconv"
	"strings"

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
	podResourceInfo := p.podResourceMapper.Snapshot()

	for _, device := range devices {
		if device.Topology == nil {
			continue
		}
		labels := buildLabels(device, p.nodeName, podResourceInfo, p.includePodLabels)
		if speed, ok := parseLinkSpeedGTs(device.Topology.PCIeLinkSpeed); ok {
			p.linkSpeed.With(labels).Set(speed)
		}
		if width, ok := parseLinkWidth(device.Topology.PCIeLinkWidth); ok {
			p.linkWidth.With(labels).Set(width)
		}
	}
}

func parseLinkSpeedGTs(speed string) (float64, bool) {
	fields := strings.Fields(speed)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func parseLinkWidth(width string) (float64, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(width), "x")
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}
