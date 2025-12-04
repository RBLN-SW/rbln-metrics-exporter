package collector

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type collectorFactory struct {
	registry          prometheus.Registerer
	dClient           *daemon.Client
	isKubernetes      bool
	podResourceMapper *PodResourceMapper
	nodeName          string
}

func NewCollectorFactory(podResourceMapper *PodResourceMapper, registry prometheus.Registerer, dClient *daemon.Client, nodeName string) *collectorFactory {
	return &collectorFactory{
		registry:          registry,
		dClient:           dClient,
		isKubernetes:      IsKubernetes(),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (cf *collectorFactory) NewCollectors() []Collector {
	collectors := []Collector{
		NewHardwareInfoCollector(cf.dClient, cf.isKubernetes, cf.podResourceMapper, cf.nodeName),
		NewMemoryCollector(cf.dClient, cf.isKubernetes, cf.podResourceMapper, cf.nodeName),
		NewUtilizationCollector(cf.dClient, cf.isKubernetes, cf.podResourceMapper, cf.nodeName),
		NewDeviceHealthCollector(cf.dClient, cf.isKubernetes, cf.podResourceMapper, cf.nodeName),
	}

	for _, collector := range collectors {
		collector.Register(cf.registry)
	}

	return collectors
}
