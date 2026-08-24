package collector

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
)

// sharedGaugeByDevice gathers rbln_npu_device_shared and keys each sample by the
// device its "name" label reports, alongside the pod that got the labels.
func sharedGaugeByDevice(t *testing.T, registry *prometheus.Registry) map[string]struct {
	value float64
	pod   string
} {
	t.Helper()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	got := make(map[string]struct {
		value float64
		pod   string
	})
	for _, family := range families {
		if family.GetName() != "rbln_npu_device_shared" {
			continue
		}
		for _, metric := range family.GetMetric() {
			var deviceName, podName string
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case name:
					deviceName = label.GetValue()
				case pod:
					podName = label.GetValue()
				}
			}
			got[deviceName] = struct {
				value float64
				pod   string
			}{metric.GetGauge().GetValue(), podName}
		}
	}
	return got
}

func TestSharedDeviceMetricFlagsOnlyMultiPodDevices(t *testing.T) {
	client := &fakePodResourcesClient{resp: response(
		// rbln0 is claimed by two pods; rbln1 belongs to one pod alone.
		podResources("team-a", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln0")),
		podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")),
		podResources("team-a", "sole-pod", draContainer("worker", "sole-claim", draDriver, "rbln1")),
	)}
	mapper := newTestMapper(client)
	mustSync(t, mapper)

	registry := prometheus.NewRegistry()
	metric := NewSharedDeviceMetric(mapper, "test-node")
	metric.Register(registry)
	metric.UpdateMetrics(context.Background(), []daemon.DeviceInfo{{Name: "rbln0"}, {Name: "rbln1"}})

	got := sharedGaugeByDevice(t, registry)
	if len(got) != 2 {
		t.Fatalf("got %d series, want one per device: %v", len(got), got)
	}
	if got["rbln0"].value != 1 {
		t.Errorf("rbln0: want 1 (two pods claim it), got %v", got["rbln0"].value)
	}
	// The flagged device still carries the winning pod's labels, so a dashboard
	// can tell which attribution is the partial one.
	if got["rbln0"].pod != "consumer-a" {
		t.Errorf("rbln0 pod label: want consumer-a, got %q", got["rbln0"].pod)
	}
	// Exclusive devices report 0 rather than going absent.
	if got["rbln1"].value != 0 {
		t.Errorf("rbln1: want 0 (one pod claims it), got %v", got["rbln1"].value)
	}
	if got["rbln1"].pod != "sole-pod" {
		t.Errorf("rbln1 pod label: want sole-pod, got %q", got["rbln1"].pod)
	}
}

// The gauge must follow the mapper down as well as up, otherwise a resolved
// share stays flagged until the process restarts.
func TestSharedDeviceMetricClearsWhenSharingEnds(t *testing.T) {
	client := &fakePodResourcesClient{resp: response(
		podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")),
		podResources("team-b", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln0")),
	)}
	mapper := newTestMapper(client)
	mustSync(t, mapper)

	registry := prometheus.NewRegistry()
	metric := NewSharedDeviceMetric(mapper, "test-node")
	metric.Register(registry)

	devices := []daemon.DeviceInfo{{Name: "rbln0"}}
	metric.UpdateMetrics(context.Background(), devices)
	if got := sharedGaugeByDevice(t, registry); got["rbln0"].value != 1 {
		t.Fatalf("rbln0 before release: want 1, got %v", got["rbln0"].value)
	}

	client.resp = response(
		podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")),
	)
	mustSync(t, mapper)
	metric.Reset()
	metric.UpdateMetrics(context.Background(), devices)

	if got := sharedGaugeByDevice(t, registry); got["rbln0"].value != 0 {
		t.Errorf("rbln0 after release: want 0, got %v", got["rbln0"].value)
	}
}

// Outside Kubernetes nothing can claim a device, so the gauge must not be
// collected at all rather than reporting 0 on every device forever.
func TestSharedDeviceMetricOnlyCollectedInKubernetes(t *testing.T) {
	for _, tt := range []struct {
		name         string
		isKubernetes bool
		want         bool
	}{
		{name: "kubernetes", isKubernetes: true, want: true},
		{name: "local", isKubernetes: false, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			npc := NewNPUCollector(nil, prometheus.NewRegistry(), tt.isKubernetes,
				NewNoopPodResourceMapper(), "test-node")

			var got bool
			for _, metric := range npc.metrics {
				if _, ok := metric.(*SharedDeviceMetric); ok {
					got = true
				}
			}
			if got != tt.want {
				t.Errorf("isKubernetes=%v: shared metric present = %v, want %v",
					tt.isKubernetes, got, tt.want)
			}
		})
	}
}
