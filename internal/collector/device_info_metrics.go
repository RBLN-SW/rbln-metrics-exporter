/*
Copyright 2026 Rebellions Inc.

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

type DeviceInfoMetric struct {
	deviceInfo        *prometheus.GaugeVec
	podResourceMapper *PodResourceMapper
	nodeName          string
	includePodLabels  bool
}

func NewDeviceInfoMetric(podResourceMapper *PodResourceMapper, nodeName string, includePodLabels bool) *DeviceInfoMetric {
	labels := deviceInfoLabelNames(includePodLabels)
	return &DeviceInfoMetric{
		deviceInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "rbln_npu_device_info",
				Help: "Device identity and static attributes as labels (value is always 1)",
			}, labels,
		),
		podResourceMapper: podResourceMapper,
		nodeName:          nodeName,
		includePodLabels:  includePodLabels,
	}
}

func (d *DeviceInfoMetric) Register(registerer prometheus.Registerer) {
	registerer.MustRegister(d.deviceInfo)
}

func (d *DeviceInfoMetric) Reset() {
	d.deviceInfo.Reset()
}

func (d *DeviceInfoMetric) UpdateMetrics(ctx context.Context, devices []daemon.DeviceInfo) {
	podResourceInfo := d.podResourceMapper.Snapshot()

	for _, device := range devices {
		labels := buildDeviceInfoLabels(device, d.nodeName, podResourceInfo, d.includePodLabels)
		d.deviceInfo.With(labels).Set(1)
	}
}
