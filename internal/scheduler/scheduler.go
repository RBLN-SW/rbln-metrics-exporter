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
	// Only touched from the Run goroutine.
	consecutiveFailures int
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
			s.runCycle(ctx)
		}
	}
}

// runCycle wraps RunOnce with failure-streak tracking so the logs mark both
// edges of an outage: each failed cycle carries its streak position, and the
// first success afterward records how many cycles were lost.
func (s *Scheduler) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.RunOnce(cycleCtx); err != nil {
		s.consecutiveFailures++
		slog.Warn("Metrics collection failed", "err", err,
			"consecutiveFailures", s.consecutiveFailures,
			"effect", "metrics cleared until next successful collect")
		return
	}
	if s.consecutiveFailures > 0 {
		slog.Info("Metrics collection recovered", "failedCycles", s.consecutiveFailures)
		s.consecutiveFailures = 0
	}
}
