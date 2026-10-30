/*
Copyright 2025-2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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

func NewCollectorFactory(podResourceMapper *PodResourceMapper, registry prometheus.Registerer, dClient *daemon.Client, nodeName string, isKubernetes bool) *collectorFactory {
	return &collectorFactory{
		registry:          registry,
		dClient:           dClient,
		isKubernetes:      isKubernetes,
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
	}
}

func (cf *collectorFactory) NewCollectors() []Collector {
	collectors := []Collector{
		NewNPUCollector(cf.dClient, cf.registry, cf.isKubernetes, cf.podResourceMapper, cf.nodeName),
	}

	for _, collector := range collectors {
		collector.Register(cf.registry)
	}

	return collectors
}
