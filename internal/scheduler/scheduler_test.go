package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/collector"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

type fakeCollector struct {
	errs []error
}

func (f *fakeCollector) Register(prometheus.Registerer) {}

func (f *fakeCollector) GetMetrics(context.Context) error {
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger, err := logging.New(&buf, "info", "json")
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func jsonRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %v: %s", err, line)
		}
		recs = append(recs, m)
	}
	return recs
}

func TestRunCycleLogsFailureStreakAndRecovery(t *testing.T) {
	buf := captureLogs(t)
	fake := &fakeCollector{errs: []error{nil, errors.New("daemon down"), errors.New("daemon down")}}
	s := NewScheduler(collector.NewNoopPodResourceMapper(), []collector.Collector{fake}, time.Second, collector.NewUpGauge())
	ctx := context.Background()

	s.runCycle(ctx) // first collect: the daemon was up
	s.runCycle(ctx) // fail 1
	s.runCycle(ctx) // fail 2
	s.runCycle(ctx) // success -> recovery record

	recs := jsonRecords(t, buf)
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (2 failures + 1 recovery): %s", len(recs), buf.String())
	}
	for i, wantStreak := range []float64{1, 2} {
		if recs[i]["level"] != "warn" || recs[i]["msg"] != "Metrics collection failed" {
			t.Fatalf("record %d = %v, want failure warn", i, recs[i])
		}
		if recs[i]["consecutiveFailures"] != wantStreak {
			t.Fatalf("record %d consecutiveFailures = %v, want %v", i, recs[i]["consecutiveFailures"], wantStreak)
		}
	}
	rec := recs[2]
	if rec["level"] != "info" || rec["msg"] != "Metrics collection recovered" {
		t.Fatalf("record 2 = %v, want recovery info", rec)
	}
	if rec["failedCycles"] != float64(2) {
		t.Fatalf("failedCycles = %v, want 2", rec["failedCycles"])
	}
}

func TestRunCycleStaysQuietOnSteadySuccess(t *testing.T) {
	buf := captureLogs(t)
	s := NewScheduler(collector.NewNoopPodResourceMapper(), []collector.Collector{&fakeCollector{}}, time.Second, collector.NewUpGauge())
	ctx := context.Background()

	s.runCycle(ctx)
	s.runCycle(ctx)

	if recs := jsonRecords(t, buf); len(recs) != 0 {
		t.Fatalf("got %d records on steady success, want 0: %s", len(recs), buf.String())
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestRunCycleWaitsForFirstCollectAtInfoThenWarns(t *testing.T) {
	buf := captureLogs(t)
	down := errors.New("connection refused")
	fake := &fakeCollector{errs: []error{down, down, down}}
	s := NewScheduler(collector.NewNoopPodResourceMapper(), []collector.Collector{fake}, 5*time.Second, collector.NewUpGauge())
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	s.now = clock.now
	ctx := context.Background()

	s.runCycle(ctx) // daemon not up yet
	clock.t = clock.t.Add(daemonWaitWarnAfter - time.Second)
	s.runCycle(ctx) // still inside the budget
	clock.t = clock.t.Add(time.Second)
	s.runCycle(ctx) // budget spent -> alertable
	s.runCycle(ctx) // daemon arrives: first collect, not a recovery

	recs := jsonRecords(t, buf)
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (2 waiting infos + 1 warn): %s", len(recs), buf.String())
	}
	for i, want := range []struct {
		level  string
		msg    string
		waited float64
	}{
		{"info", "Waiting for rbln-smd, retrying", 0},
		{"info", "Waiting for rbln-smd, retrying", (daemonWaitWarnAfter - time.Second).Seconds()},
		{"warn", "Still waiting for rbln-smd, retrying", daemonWaitWarnAfter.Seconds()},
	} {
		rec := recs[i]
		if rec["level"] != want.level || rec["msg"] != want.msg {
			t.Fatalf("record %d = %v, want %s %q", i, rec, want.level, want.msg)
		}
		if rec["waitedSeconds"] != want.waited {
			t.Fatalf("record %d waitedSeconds = %v, want %v", i, rec["waitedSeconds"], want.waited)
		}
		if rec["err"] != down.Error() {
			t.Fatalf("record %d err = %v, want the wait reason %q", i, rec["err"], down.Error())
		}
	}
}
