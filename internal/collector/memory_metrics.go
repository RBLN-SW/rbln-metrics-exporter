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
	"context"
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

const gibToBytes = 1 << 30

type MemoryMetric struct {
	dramUsed          *prometheus.GaugeVec
	dramTotal         *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewMemoryMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *MemoryMetric {
	labels := labelNames(includePodLabels)
	return &MemoryMetric{
		dramUsed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:DRAM_USED", "rbln_npu_memory_used"),
				Help: "DRAM used (bytes)",
			}, labels,
		),
		dramTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:DRAM_TOTAL", "rbln_npu_memory_total"),
				Help: "DRAM total (bytes)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (m *MemoryMetric) Register(reg prometheus.Registerer) {
	reg.MustRegister(m.dramUsed)
	reg.MustRegister(m.dramTotal)
}

func (m *MemoryMetric) Reset() {
	m.dramUsed.Reset()
	m.dramTotal.Reset()
}

func (m *MemoryMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := m.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, m.nodeName, podResourceInfo, m.includePodLabels)

		bytesUsed := uint64(math.Round(device.DRAMUsedGiB * float64(gibToBytes)))
		bytesTotal := uint64(math.Round(device.DRAMTotalGiB * float64(gibToBytes)))

		m.dramUsed.With(labels).Set(float64(bytesUsed))
		m.dramTotal.With(labels).Set(float64(bytesTotal))
	}
}
