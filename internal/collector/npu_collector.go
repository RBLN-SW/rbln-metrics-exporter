package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type NPUCollector struct {
	metrics           []Metric
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	NodeName          string
}

func NewNPUCollector(dClient *daemon.Client, registry prometheus.Registerer, isKubernetes bool, podResourceMapper *PodResourceMapper, nodeName string) *NPUCollector {
	metrics := []Metric{
		NewHardwareInfoMetric(podResourceMapper, nodeName),
		NewDeviceHealthMetric(podResourceMapper, nodeName),
		NewMemoryMetric(podResourceMapper, nodeName),
		NewUtilizationMetric(podResourceMapper, nodeName),
	}

	return &NPUCollector{
		metrics:           metrics,
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
	}
}

func (n *NPUCollector) Register(registerer prometheus.Registerer) {
	for _, metric := range n.metrics {
		metric.Register(registerer)
	}
}

func (n *NPUCollector) GetMetrics(ctx context.Context) error {
	devices, err := n.dClient.GetDeviceInfo(ctx)
	if err != nil {
		return err
	}

	for _, metric := range n.metrics {
		metric.Reset()
		metric.UpdateMetrics(ctx, devices)
	}

	return nil
}
