package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

type UtilizationCollector struct {
	utilization *prometheus.GaugeVec
	dClient     *daemon.Client
}

func NewUtilizationCollector(dClient *daemon.Client) *UtilizationCollector {
	return &UtilizationCollector{
		utilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "RBLN_DEVICE_STATUS:UTILIZATION",
				Help: "Utilization (%)",
			}, commonLabels,
		),
		dClient: dClient,
	}
}

func (u *UtilizationCollector) Register(reg prometheus.Registerer) {
	reg.MustRegister(u.utilization)
}

func (u *UtilizationCollector) GetMetrics(ctx context.Context) error {
	deviceStatus, err := u.dClient.GetDeviceStatus(ctx)
	if err != nil {
		return err
	}
	for _, s := range deviceStatus {
		labels := prometheus.Labels{
			"card":   s.Card,
			"uuid":   s.UUID,
			"device": s.DeviceNode,
		}
		u.utilization.With(labels).Set(s.Utilization)
	}
	return nil
}
