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

type UtilizationMetric struct {
	utilization       *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewUtilizationMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *UtilizationMetric {
	labels := labelNames(includePodLabels)
	return &UtilizationMetric{
		utilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:UTILIZATION", "rbln_npu_utilization"),
				Help: "Utilization (%)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (u *UtilizationMetric) Register(reg prometheus.Registerer) {
	reg.MustRegister(u.utilization)
}

func (u *UtilizationMetric) Reset() {
	u.utilization.Reset()
}

func (u *UtilizationMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := u.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, u.nodeName, podResourceInfo, u.includePodLabels)
		u.utilization.With(labels).Set(device.Utilization)
	}
}
