package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
)

type Scheduler struct {
	collectors        []collector.Collector
	interval          time.Duration
	podResourceMapper *collector.PodResourceMapper
	up                prometheus.Gauge
}

func NewScheduler(podResourceMapper *collector.PodResourceMapper, collectors []collector.Collector, interval time.Duration, up prometheus.Gauge) *Scheduler {
	return &Scheduler{
		collectors:        collectors,
		interval:          interval,
		podResourceMapper: podResourceMapper,
		up:                up,
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	s.podResourceMapper.TriggerSync()
	for _, collector := range s.collectors {
		if err := collector.GetMetrics(ctx); err != nil {
			s.up.Set(0)
			return err
		}
	}
	s.up.Set(1)
	return nil
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := s.RunOnce(cycleCtx); err != nil {
				slog.Warn("Metrics collection failed", "err", err,
					"effect", "metrics cleared until next successful collect")
			}
			cancel()
		}
	}
}
