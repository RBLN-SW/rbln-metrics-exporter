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
		NewHardwareInfoMetric(podResourceMapper, nodeName, isKubernetes),
		NewDeviceHealthMetric(podResourceMapper, nodeName, isKubernetes),
		NewMemoryMetric(podResourceMapper, nodeName, isKubernetes),
		NewUtilizationMetric(podResourceMapper, nodeName, isKubernetes),
		NewDeviceStateMetric(podResourceMapper, nodeName, isKubernetes),
		NewPowerStateMetric(podResourceMapper, nodeName, isKubernetes),
		NewPCIeLinkMetric(podResourceMapper, nodeName, isKubernetes),
		NewDeviceInfoMetric(podResourceMapper, nodeName, isKubernetes),
	}

	// Only DRA produces a device with several claimants, so outside Kubernetes
	// the gauge would be a constant 0 on every device.
	if isKubernetes {
		metrics = append(metrics, NewSharedDeviceMetric(podResourceMapper, nodeName))
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
		// Clear the last successful values so a broken collection cycle
		// surfaces as absent metrics instead of stale-but-healthy ones.
		for _, metric := range n.metrics {
			metric.Reset()
		}
		return err
	}

	for _, metric := range n.metrics {
		metric.Reset()
		metric.UpdateMetrics(ctx, devices)
	}

	return nil
}
