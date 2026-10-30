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

type DeviceHealthMetric struct {
	healthStatus      *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	NodeName          string
	includePodLabels  bool
}

func NewDeviceHealthMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceHealthMetric {
	labels := labelNames(includePodLabels)
	return &DeviceHealthMetric{
		healthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricName("RBLN_DEVICE_STATUS:HEALTH", "rbln_npu_health"),
				Help: "NPU health status",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		NodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (d *DeviceHealthMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.healthStatus)
}

func (d *DeviceHealthMetric) Reset() {
	d.healthStatus.Reset()
}

func (d *DeviceHealthMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildLabels(device, d.NodeName, podResourceInfo, d.includePodLabels)
		d.healthStatus.With(labels).Set(float64(device.ErrStatus))
	}
}
