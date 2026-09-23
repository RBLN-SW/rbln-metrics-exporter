package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
)

// daemonWaitWarnAfter is how long failing to reach rbln-smd before the first
// successful collect counts as a normal start-up wait. rbln-smd starts
// independently of the exporter and cannot serve until the NPU driver is
// loaded, which on a fresh install can take up to 10 minutes; a daemon still
// unreachable after that is not coming up on its own, so the wait escalates
// to warn and becomes alertable.
const daemonWaitWarnAfter = 10 * time.Minute

type Scheduler struct {
	collectors        []collector.Collector
	interval          time.Duration
	podResourceMapper *collector.PodResourceMapper
	up                prometheus.Gauge
	now               func() time.Time
	// Only touched from the Run goroutine.
	consecutiveFailures int
	collectedOnce       bool
	waitStart           time.Time
}

func NewScheduler(podResourceMapper *collector.PodResourceMapper, collectors []collector.Collector, interval time.Duration, up prometheus.Gauge) *Scheduler {
	return &Scheduler{
		collectors:        collectors,
		interval:          interval,
		podResourceMapper: podResourceMapper,
		up:                up,
		now:               time.Now,
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
// first success afterward records how many cycles were lost. Failures before
// the first success are a start-up wait instead, reported by logWaiting.
func (s *Scheduler) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.RunOnce(cycleCtx); err != nil {
		if !s.collectedOnce {
			s.logWaiting(err)
			return
		}
		s.consecutiveFailures++
		slog.Warn("Metrics collection failed", "err", err,
			"consecutiveFailures", s.consecutiveFailures,
			"effect", "metrics cleared until next successful collect")
		return
	}
	s.collectedOnce = true
	if s.consecutiveFailures > 0 {
		slog.Info("Metrics collection recovered", "failedCycles", s.consecutiveFailures)
		s.consecutiveFailures = 0
	}
}

// logWaiting reports a failed cycle before the first successful collect: at
// info while the wait fits daemonWaitWarnAfter, at warn once it does not.
// Either way the retry continues — exiting would only trade this record for a
// kubelet restart loop.
func (s *Scheduler) logWaiting(err error) {
	now := s.now()
	if s.waitStart.IsZero() {
		s.waitStart = now
	}
	waited := now.Sub(s.waitStart)
	attrs := []any{
		"err", err,
		"waitedSeconds", waited.Round(time.Second).Seconds(),
		"retryInSeconds", s.interval.Seconds(),
		"effect", "rbln_up 0 and no device metrics until the first successful collect",
	}
	if waited >= daemonWaitWarnAfter {
		slog.Warn("Still waiting for rbln-smd, retrying", attrs...)
		return
	}
	slog.Info("Waiting for rbln-smd, retrying", attrs...)
}
