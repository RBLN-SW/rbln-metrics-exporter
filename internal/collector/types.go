package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

type Collector interface {
	Register(prometheus.Registerer)
	GetMetrics(context.Context) error
}
