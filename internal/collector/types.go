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
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type Collector interface {
	Register(prometheus.Registerer)
	GetMetrics(context.Context) error
}

type Metric interface {
	Register(prometheus.Registerer)
	UpdateMetrics(context.Context, []daemon.DeviceInfo)
	Reset()
}

func metricName(legacy, current string) string {
	if os.Getenv("PROMETHEUS_METRIC_NAMES") == "true" {
		return current
	}
	return legacy
}

func NewUpGauge() prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "rbln_up",
		Help: "1 if the last metrics collection from rbln-smd succeeded, 0 otherwise",
	})
}
