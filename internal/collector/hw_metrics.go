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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type HardwareInfoMetric struct {
	temperature       *prometheus.GaugeVec
	power             *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	NodeName          string
	includePodLabels  bool
}

func NewHardwareInfoMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *HardwareInfoMetric {
	labels := labelNames(includePodLabels)
	return &HardwareInfoMetric{
		temperature: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:TEMPERATURE", "rbln_npu_temperature"),
				Help: "NPU temperature (C)",
			}, labels,
		),
		power: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:CARD_POWER", "rbln_npu_power"),
				Help: "Card power usage (W)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (h *HardwareInfoMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(h.temperature)
	registerer.MustRegister(h.power)
}

func (h *HardwareInfoMetric) Reset() {
	h.temperature.Reset()
	h.power.Reset()
}

func (h *HardwareInfoMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := h.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, h.NodeName, podResourceInfo, h.includePodLabels)
		h.temperature.With(labels).Set(device.Temperature)
		h.power.With(labels).Set(device.Power)
	}
}
