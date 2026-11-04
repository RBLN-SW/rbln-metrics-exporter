package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/smdtest"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

func sampleByDevice(t *testing.T, r *prometheus.Registry, metricName, device string) *dto.Metric {
	t.Helper()
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != metricName {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "name" && l.GetValue() == device {
					return m
				}
			}
		}
	}
	return nil
}

func labelValue(m *dto.Metric, key string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == key {
			return l.GetValue()
		}
	}
	return ""
}

func TestCollectionVFProfilesAndSeriesLifecycle(t *testing.T) {
	t.Setenv("PROMETHEUS_METRIC_NAMES", "true")
	pf := smdtest.Device("rbln0", false)
	pf.NpuId = nil
	vf := smdtest.Device("rbln0-0", true)
	vf.ParentName = pf.Name
	vf.PciBusId = "0000:17:00.1"
	root := t.TempDir()
	for _, bus := range []string{pf.PciBusId, vf.PciBusId} {
		if err := os.MkdirAll(filepath.Join(root, bus), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../"+pf.PciBusId, filepath.Join(root, vf.PciBusId, "physfn")); err != nil {
		t.Fatal(err)
	}
	mode := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, pf.PciBusId, "sriov_numvfs"), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mode("4")
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{pf, vf}, []*pb.MetricSample{
		smdtest.Float(pf.Name, pb.MetricId_METRIC_ID_POWER_MICRO_WATT, 50000000),
		smdtest.Float(vf.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 0),
		smdtest.Int(vf.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 1024),
		smdtest.Int(vf.Name, pb.MetricId_METRIC_ID_PSTATE, 2),
	})
	transport := smdtest.NewTLS(t)
	addr, stop := smdtest.Serve(t, s, "127.0.0.1:0", transport)
	t.Cleanup(stop)
	opts := daemon.DefaultOptions()
	opts.TLSCA = transport.CAFile
	opts.PCIDevicesPath = root
	client, err := daemon.NewLazyClient(addr, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	podClient := &fakePodResourcesClient{resp: response(podResources("ns", "old-vf4", draContainer("worker", "claim", draDriver, "rbln0-0-vf4")))}
	mapper := newTestMapper(podClient)
	mustSync(t, mapper)
	r := prometheus.NewRegistry()
	c := NewNPUCollector(client, r, true, mapper, "node")
	c.Register(r)
	collect := func() {
		t.Helper()
		if err := c.GetMetrics(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	m := sampleByDevice(t, r, "rbln_npu_utilization", vf.Name)
	if m == nil || m.GetGauge().GetValue() != 0 || labelValue(m, "pod") != "old-vf4" {
		t.Fatalf("VF zero and attribution: %v", m)
	}
	if sampleByDevice(t, r, "rbln_npu_memory_total", pf.Name) != nil {
		t.Fatal("hosting PF capacity double-counted")
	}
	if sampleByDevice(t, r, "rbln_npu_temperature", vf.Name) != nil || sampleByDevice(t, r, "rbln_npu_power", vf.Name) != nil {
		t.Fatal("fabricated VF card metrics")
	}
	if sampleByDevice(t, r, "rbln_npu_clock_frequency_mhz", pf.Name) != nil {
		t.Fatal("clocks exported")
	}
	mode("1")
	collect()
	if m = sampleByDevice(t, r, "rbln_npu_utilization", vf.Name); m == nil || labelValue(m, "pod") != "" {
		t.Fatalf("old vf4 allocation attached to vf1: %v", m)
	}
	podClient.resp = response(podResources("ns", "new-vf1", devicePluginContainer("worker", "rebellions.ai/npu-vf1", vf.Name)))
	mustSync(t, mapper)
	collect()
	if m = sampleByDevice(t, r, "rbln_npu_utilization", vf.Name); labelValue(m, "pod") != "new-vf1" {
		t.Fatal("device-plugin VF labels missing")
	}
	mode("0")
	n := uint32(0)
	pf.NpuId = &n
	s.Set([]*pb.DeviceInfo{pf}, nil)
	collect()
	if sampleByDevice(t, r, "rbln_npu_device_info", vf.Name) != nil {
		t.Fatal("removed VF series persisted")
	}
	if sampleByDevice(t, r, "rbln_npu_memory_total", pf.Name) == nil {
		t.Fatal("PF compute capacity did not return")
	}
	s.Mu.Lock()
	s.Errors = map[string]error{"samples": status.Error(codes.Unavailable, "restart")}
	s.Mu.Unlock()
	if err := c.GetMetrics(context.Background()); err == nil {
		t.Fatal("expected RPC failure")
	}
	if sampleByDevice(t, r, "rbln_npu_memory_total", pf.Name) != nil {
		t.Fatal("stale metrics survived failed cycle")
	}
	s.Mu.Lock()
	s.Errors = nil
	s.Mu.Unlock()
	collect()
	if sampleByDevice(t, r, "rbln_npu_memory_total", pf.Name) == nil {
		t.Fatal("collection did not recover")
	}
}

func TestUnspecifiedDeviceStateIsUnknownNotMissingOrHealthy(t *testing.T) {
	t.Setenv("PROMETHEUS_METRIC_NAMES", "true")
	r := prometheus.NewRegistry()
	mapper := NewNoopPodResourceMapper()
	state := NewDeviceStateMetric(mapper, "node", false)
	health := NewDeviceHealthMetric(mapper, "node", false)
	state.Register(r)
	health.Register(r)
	devices := []daemon.DeviceInfo{{Name: "unspecified", DevState: pb.DeviceState_DEVICE_STATE_UNSPECIFIED}, {Name: "future", DevState: pb.DeviceState(999)}}
	state.UpdateMetrics(context.Background(), devices)
	health.UpdateMetrics(context.Background(), devices)
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	unknown := 0
	for _, f := range families {
		if f.GetName() == "rbln_npu_health" {
			t.Fatal("unknown state reported as healthy")
		}
		if f.GetName() != "rbln_npu_device_status" {
			continue
		}
		for _, m := range f.GetMetric() {
			if labelValue(m, "state") == "not_found" {
				t.Fatal("present device reported as missing")
			}
			if m.GetGauge().GetValue() == 1 {
				if labelValue(m, "state") != "unknown" {
					t.Fatalf("unexpected state: %v", m)
				}
				unknown++
			}
		}
	}
	if unknown != 2 {
		t.Fatalf("got %d unknown devices, want 2", unknown)
	}
}

func TestHealthPreservesReadinessAndDoesNotRepresentCollection(t *testing.T) {
	for _, legacyNames := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyNames), func(t *testing.T) {
			t.Setenv("PROMETHEUS_METRIC_NAMES", fmt.Sprint(!legacyNames))
			r := prometheus.NewRegistry()
			mapper := NewNoopPodResourceMapper()
			health := NewDeviceHealthMetric(mapper, "node", false)
			collection := NewDeviceCollectionMetric(mapper, "node", false)
			health.Register(r)
			collection.Register(r)
			devices := []daemon.DeviceInfo{
				{Name: "ready", DevState: pb.DeviceState_DEVICE_STATE_READY, CollectionSucceeded: false},
				{Name: "busy", DevState: pb.DeviceState_DEVICE_STATE_BUSY, CollectionSucceeded: true},
				{Name: "init", DevState: pb.DeviceState_DEVICE_STATE_INIT, CollectionSucceeded: true},
				{Name: "fault", DevState: pb.DeviceState_DEVICE_STATE_FAULT, CollectionSucceeded: true},
				{Name: "finish", DevState: pb.DeviceState_DEVICE_STATE_FINISH, CollectionSucceeded: true},
				{Name: "unknown", DevState: pb.DeviceState_DEVICE_STATE_UNSPECIFIED, CollectionSucceeded: true},
			}
			health.UpdateMetrics(context.Background(), devices)
			collection.UpdateMetrics(context.Background(), devices)
			healthName := "rbln_npu_health"
			if legacyNames {
				healthName = "RBLN_DEVICE_STATUS:HEALTH"
			}
			for _, d := range devices {
				m := sampleByDevice(t, r, healthName, d.Name)
				if d.Name == "unknown" {
					if m != nil {
						t.Fatal("unknown state reported as health")
					}
				} else {
					want := 1.0
					if d.Name == "ready" {
						want = 0
					}
					if m == nil || m.GetGauge().GetValue() != want {
						t.Fatalf("%s health=%v want=%v", d.Name, m, want)
					}
				}
				want := 1.0
				if !d.CollectionSucceeded {
					want = 0
				}
				m = sampleByDevice(t, r, "rbln_npu_collection_success", d.Name)
				if m == nil || m.GetGauge().GetValue() != want {
					t.Fatalf("%s collection=%v want=%v", d.Name, m, want)
				}
			}
		})
	}
}

// Freeze the pre-migration PF names, units and common labels using known
// sensor readings. New scope labels belong only on the info series.
func TestPFMetricContractFromKnownReadings(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			t.Setenv("PROMETHEUS_METRIC_NAMES", fmt.Sprint(modern))
			d := smdtest.Device("rbln0", false)
			d.DevId = "1234"
			d.CardName = "test-card"
			d.Versions.SmcVersion = "test-smc"
			d.Memory.TotalBytes = 16 << 30
			s := &smdtest.Server{Entries: []*pb.TopologyEntry{{DeviceName: d.Name, PcieLinkSpeed: "32 GT/s PCIe", PcieLinkWidth: "x16"}}}
			s.Set([]*pb.DeviceInfo{d}, []*pb.MetricSample{
				smdtest.Float(d.Name, pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS, 35500),
				smdtest.Float(d.Name, pb.MetricId_METRIC_ID_POWER_MICRO_WATT, 40000000),
				smdtest.Float(d.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 12500),
				smdtest.Int(d.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 1<<30),
				smdtest.Int(d.Name, pb.MetricId_METRIC_ID_PSTATE, 2),
			})
			transport := smdtest.NewTLS(t)
			addr, stop := smdtest.Serve(t, s, "127.0.0.1:0", transport)
			t.Cleanup(stop)
			opts := daemon.DefaultOptions()
			opts.TLSCA = transport.CAFile
			client, err := daemon.NewLazyClient(addr, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			mapper := newTestMapper(&fakePodResourcesClient{resp: response(podResources("ns", "consumer", devicePluginContainer("workload", dpResource, d.Name)))})
			mustSync(t, mapper)
			reg := prometheus.NewRegistry()
			c := NewNPUCollector(client, reg, true, mapper, "node")
			c.Register(reg)
			if err := c.GetMetrics(context.Background()); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				current, legacy string
				value           float64
			}{
				{"rbln_npu_temperature", "RBLN_DEVICE_STATUS:TEMPERATURE", 35.5},
				{"rbln_npu_power", "RBLN_DEVICE_STATUS:CARD_POWER", 40},
				{"rbln_npu_memory_used", "RBLN_DEVICE_STATUS:DRAM_USED", 1 << 30},
				{"rbln_npu_memory_total", "RBLN_DEVICE_STATUS:DRAM_TOTAL", 16 << 30},
				{"rbln_npu_utilization", "RBLN_DEVICE_STATUS:UTILIZATION", 12.5},
				{"rbln_npu_health", "RBLN_DEVICE_STATUS:HEALTH", 0},
				{"rbln_npu_power_state", "rbln_npu_power_state", 2},
				{"rbln_npu_pcie_link_speed_gts", "rbln_npu_pcie_link_speed_gts", 32},
				{"rbln_npu_pcie_link_width", "rbln_npu_pcie_link_width", 16},
				{"rbln_npu_device_shared", "rbln_npu_device_shared", 0},
			}
			labels := map[string]string{"name": d.Name, "uuid": "shared-uuid", "deviceID": "1234", "card": "test-card", "hostname": "node", "driver_version": "inline-driver", "firmware_version": "inline-firmware", "namespace": "ns", "pod": "consumer", "container": "workload"}
			for _, tc := range cases {
				metric := tc.current
				if !modern {
					metric = tc.legacy
				}
				sample := sampleByDevice(t, reg, metric, d.Name)
				if sample == nil || sample.GetGauge().GetValue() != tc.value {
					t.Fatalf("%s: got %v want %v", metric, sample, tc.value)
				}
				if len(sample.GetLabel()) != len(labels) {
					t.Fatalf("%s changed common label set: %v", metric, sample.GetLabel())
				}
				for key, want := range labels {
					if labelValue(sample, key) != want {
						t.Fatalf("%s label %s=%q want %q", metric, key, labelValue(sample, key), want)
					}
				}
			}
			info := sampleByDevice(t, reg, "rbln_npu_device_info", d.Name)
			if labelValue(info, "compute") != "true" || labelValue(info, "partition_mode") != "none" {
				t.Fatalf("missing info scope: %v", info)
			}
		})
	}
}
