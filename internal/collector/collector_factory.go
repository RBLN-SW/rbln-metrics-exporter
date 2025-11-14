package collector

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type collectorFactory struct {
	registry prometheus.Registerer
	dClient  *daemon.Client
}

func NewCollectorFactory(registry prometheus.Registerer, dClient *daemon.Client) *collectorFactory {
	return &collectorFactory{registry: registry, dClient: dClient}
}

func (cf *collectorFactory) NewCollectors() []Collector {
	collectors := []Collector{
		NewHardwareInfoCollector(cf.dClient),
		NewMemoryCollector(cf.dClient),
		NewUtilizationCollector(cf.dClient),
	}

	for _, collector := range collectors {
		collector.Register(cf.registry)
	}

	return collectors
}
