package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/smdtest"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

func startFake(t *testing.T, s *smdtest.Server, options ...Options) *Client {
	t.Helper()
	transport := smdtest.NewTLS(t)
	addr, stop := smdtest.Serve(t, s, "127.0.0.1:0", transport)
	t.Cleanup(stop)
	opts := DefaultOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	opts.TLSCA = transport.CAFile
	c, err := NewLazyClient(addr, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func collect(t *testing.T, c *Client) []DeviceInfo {
	t.Helper()
	d, err := c.GetDeviceInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	l, err := logging.New(&b, "info", "json")
	if err != nil {
		t.Fatal(err)
	}
	old := slog.Default()
	slog.SetDefault(l)
	t.Cleanup(func() { slog.SetDefault(old) })
	return &b
}

func jsonRecords(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestV1InventoryAndSampleSemantics(t *testing.T) {
	pf := smdtest.Device("rbln0", false)
	pf.NpuId = nil
	vf := smdtest.Device("rbln0-0", true)
	vf.ParentName = "rbln0"
	vf.PciBusId = "0000:17:00.1"
	s := &smdtest.Server{PageSize: 1, Entries: []*pb.TopologyEntry{{DeviceName: pf.Name, PcieLinkSpeed: "32 GT/s PCIe", PcieLinkWidth: "x16"}, {DeviceName: vf.Name, PcieLinkSpeed: "32 GT/s PCIe", PcieLinkWidth: "x16"}}}
	s.Set([]*pb.DeviceInfo{pf, vf}, []*pb.MetricSample{
		smdtest.Float(pf.Name, pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS, 45000),
		smdtest.Float(pf.Name, pb.MetricId_METRIC_ID_POWER_MICRO_WATT, 120_000_000),
		smdtest.Float(pf.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 99000),
		smdtest.Float(vf.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 12500),
		smdtest.Int(vf.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 123456789),
		// VF1 may repeat card power. It must never create a second card series.
		smdtest.Float(vf.Name, pb.MetricId_METRIC_ID_POWER_MICRO_WATT, 120_000_000),
	})
	got := collect(t, startFake(t, s))
	if len(got) != 2 || got[0].Name != pf.Name || got[1].Name != vf.Name {
		t.Fatalf("inventory: %+v", got)
	}
	if got[0].Compute || got[0].Utilization != nil || got[0].MemoryTotalBytes != nil {
		t.Fatalf("hosting PF counted as compute: %+v", got[0])
	}
	if got[0].Temperature == nil || *got[0].Temperature != 45 || *got[0].Power != 120 || *got[0].PCIeLinkSpeed != 32 {
		t.Fatalf("PF units: %+v", got[0])
	}
	if got[1].Utilization == nil || *got[1].Utilization != 12.5 || *got[1].MemoryUsedBytes != 123456789 || *got[1].MemoryTotalBytes != 35<<30 {
		t.Fatalf("VF readings: %+v", got[1])
	}
	if got[1].Temperature != nil || got[1].Power != nil || got[1].PCIeLinkSpeed != nil || got[1].PState != nil {
		t.Fatalf("fabricated/shared readings: %+v", got[1])
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.InventoryRequests) != 2 || s.VersionCalls != 0 || s.MemoryCalls != 0 {
		t.Fatal("pagination/static spec contract")
	}
	for _, r := range s.InventoryRequests {
		if r.GetFilter() != pb.DeviceListFilter_DEVICE_LIST_FILTER_ALL || r.GetDetail() != pb.DeviceListDetail_DEVICE_LIST_DETAIL_FULL {
			t.Fatal(r)
		}
	}
}

func TestMissingInlineStaticSpecsAndZero(t *testing.T) {
	d := smdtest.Device("rbln0", false)
	d.Versions = nil
	d.Memory = nil
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{d}, []*pb.MetricSample{smdtest.Float(d.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 0), smdtest.Int(d.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 0), smdtest.Int(d.Name, pb.MetricId_METRIC_ID_PSTATE, 0)})
	got := collect(t, startFake(t, s))[0]
	if got.Utilization == nil || *got.Utilization != 0 || got.MemoryUsedBytes == nil || *got.MemoryUsedBytes != 0 || got.PState == nil || *got.PState != 0 {
		t.Fatal("real zeros were lost")
	}
	if got.DriverVersion != "test-driver" || got.MemoryTotalBytes == nil || *got.MemoryTotalBytes != 35<<30 {
		t.Fatalf("static fallback: %+v", got)
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.VersionCalls != 1 || s.MemoryCalls != 1 {
		t.Fatal("static fallback RPCs not called")
	}
}

func TestMetricGroupSafetyAndRestart(t *testing.T) {
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{smdtest.Device("rbln0", false)}, nil)
	c := startFake(t, s)
	collect(t, c)
	collect(t, c)
	s.Mu.Lock()
	if len(s.Created) != 1 {
		t.Fatal("group not reused")
	}
	want := []pb.MetricId{100, 101, 200, 300, 500}
	if !slices.Equal(s.Created[0].MetricIds, want) {
		t.Fatalf("unexpected query (clocks must be absent): %v", s.Created[0].MetricIds)
	}
	s.Groups = nil
	s.Mu.Unlock()
	collect(t, c)
	s.Mu.Lock()
	if len(s.Created) != 2 {
		t.Fatal("daemon restart did not recreate group")
	}
	s.Groups[DefaultMetricGroupID].MetricIds = append(s.Groups[DefaultMetricGroupID].MetricIds, pb.MetricId_METRIC_ID_CLOCK_DC_0_MHZ)
	calls := s.SampleCalls
	s.Mu.Unlock()
	if _, err := c.GetDeviceInfo(context.Background()); err == nil {
		t.Fatal("accepted a group containing clocks")
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.SampleCalls != calls {
		t.Fatal("unsafe group was sampled")
	}
}

func TestMetricGroupReadOnlyAndPermissionErrors(t *testing.T) {
	for _, readonly := range []bool{true, false} {
		s := &smdtest.Server{Errors: map[string]error{"create": status.Error(codes.PermissionDenied, "denied")}}
		s.Set([]*pb.DeviceInfo{smdtest.Device("rbln0", false)}, nil)
		o := DefaultOptions()
		o.CreateMetricGroup = !readonly
		c := startFake(t, s, o)
		if _, err := c.GetDeviceInfo(context.Background()); err == nil {
			t.Fatal("missing permission/group ignored")
		}
		s.Mu.Lock()
		if s.SampleCalls != 0 {
			t.Fatal("fell back to default")
		}
		s.Mu.Unlock()
	}
}

func TestPartialTelemetryDoesNotClearOtherDevices(t *testing.T) {
	a, b := smdtest.Device("rbln0", false), smdtest.Device("rbln1", false)
	s := &smdtest.Server{Unreachable: []*pb.EntityStatus{{Ref: b.Name}}}
	s.Set([]*pb.DeviceInfo{a, b}, []*pb.MetricSample{smdtest.Int(a.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 100), smdtest.Int(b.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 200)})
	got := collect(t, startFake(t, s))
	if got[0].MemoryUsedBytes == nil || got[1].MemoryUsedBytes != nil {
		t.Fatal("partial failure not isolated")
	}
	if !got[0].CollectionSucceeded || got[1].CollectionSucceeded {
		t.Fatal("per-device collection result does not reflect unreachable")
	}
}

func TestInvalidSamplesAreAbsent(t *testing.T) {
	d := DeviceInfo{Compute: true}
	for _, s := range []*pb.MetricSample{
		smdtest.Float("", pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, math.NaN()),
		smdtest.Float("", pb.MetricId_METRIC_ID_POWER_MICRO_WATT, -1),
		smdtest.Int("", pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, -1),
		smdtest.Int("", pb.MetricId_METRIC_ID_PSTATE, -1),
		{MetricId: pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS},
	} {
		applySample(&d, s)
	}
	if d.Utilization != nil || d.Power != nil || d.MemoryUsedBytes != nil || d.PState != nil || d.Temperature != nil {
		t.Fatalf("bad sample accepted: %+v", d)
	}
}

func TestPStateUsesTheDaemonSampleWithoutClientSideOrdinalLimit(t *testing.T) {
	d := DeviceInfo{}
	applySample(&d, smdtest.Int("", pb.MetricId_METRIC_ID_PSTATE, 15))
	if d.PState == nil || *d.PState != 15 {
		t.Fatal("valid nonnegative p-state sample discarded by an exporter-specific bound")
	}
}

func TestPartitionProfileUsesParentSysfs(t *testing.T) {
	root := t.TempDir()
	pf := smdtest.Device("rbln0", false)
	pf.NpuId = nil
	vf := smdtest.Device("rbln0-0", true)
	vf.ParentName = pf.Name
	vf.PciBusId = "0000:17:00.1"
	for _, bus := range []string{pf.PciBusId, vf.PciBusId} {
		if err := os.MkdirAll(filepath.Join(root, bus), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../"+pf.PciBusId, filepath.Join(root, vf.PciBusId, "physfn")); err != nil {
		t.Fatal(err)
	}
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{pf, vf}, nil)
	o := DefaultOptions()
	o.PCIDevicesPath = root
	c := startFake(t, s, o)
	for _, mode := range []string{"4", "1", "garbage"} {
		if err := os.WriteFile(filepath.Join(root, pf.PciBusId, "sriov_numvfs"), []byte(mode), 0o644); err != nil {
			t.Fatal(err)
		}
		got := collect(t, c)
		want := ""
		if mode != "garbage" {
			want = "vf" + mode
		}
		if got[1].PartitionMode != want {
			t.Fatalf("mode %s: %+v", mode, got[1])
		}
	}
	// A remote client must not interpret the collector host's sysfs as the target.
	remote := collect(t, startFake(t, s))
	if remote[1].PartitionMode != "" {
		t.Fatal("remote profile guessed")
	}
}

func TestTransitionLogsAreEdgeTriggered(t *testing.T) {
	s := &smdtest.Server{}
	d := smdtest.Device("rbln0", false)
	s.Set([]*pb.DeviceInfo{d}, nil)
	c := startFake(t, s)
	buf := captureLogs(t)
	collect(t, c)
	collect(t, c)
	d.State = pb.DeviceState_DEVICE_STATE_BUSY
	s.Set([]*pb.DeviceInfo{d}, nil)
	collect(t, c)
	d.State = pb.DeviceState_DEVICE_STATE_FAULT
	s.Set([]*pb.DeviceInfo{d}, nil)
	collect(t, c)
	collect(t, c)
	d.State = pb.DeviceState_DEVICE_STATE_READY
	s.Set([]*pb.DeviceInfo{d}, nil)
	collect(t, c)
	s.Set(nil, nil)
	collect(t, c)
	collect(t, c)
	counts := map[string]int{}
	for _, r := range jsonRecords(t, buf) {
		counts[r["msg"].(string)]++
	}
	for _, msg := range []string{"First collection succeeded", "Device entered degraded state", "Device recovered", "Device no longer present"} {
		if counts[msg] != 1 {
			t.Fatalf("%s count=%d: %s", msg, counts[msg], buf.String())
		}
	}
}

func TestReadOnlyClientReusesProvisionedGroup(t *testing.T) {
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{smdtest.Device("rbln0", false)}, nil)
	collect(t, startFake(t, s))
	s.Mu.Lock()
	s.Errors = map[string]error{"create": status.Error(codes.PermissionDenied, "read only")}
	s.Mu.Unlock()
	opts := DefaultOptions()
	opts.CreateMetricGroup = false
	collect(t, startFake(t, s, opts))
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.Created) != 1 {
		t.Fatal("read-only client attempted group creation")
	}
}

func TestVFIOBindingExcludesCachedNativeDevice(t *testing.T) {
	root := t.TempDir()
	d := smdtest.Device("rbln0-0", true)
	dir := filepath.Join(root, d.PciBusId)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/sys/bus/pci/drivers/vfio-pci", filepath.Join(dir, "driver")); err != nil {
		t.Fatal(err)
	}
	s := &smdtest.Server{}
	s.Set([]*pb.DeviceInfo{d}, nil)
	opts := DefaultOptions()
	opts.PCIDevicesPath = root
	if got := collect(t, startFake(t, s, opts)); len(got) != 0 {
		t.Fatalf("VFIO device exported: %+v", got)
	}
}

func TestClientRejectsUnsafeGroupAndInvalidTLS(t *testing.T) {
	for _, opts := range []Options{
		{MetricGroupID: "default"},
		{TLSCert: "cert.pem"},
		{TLSKey: "key.pem"},
		{TLSCA: filepath.Join(t.TempDir(), "missing.pem")},
	} {
		c, err := NewLazyClient("127.0.0.1:50051", opts)
		if err == nil {
			_ = c.Close()
			t.Fatalf("invalid options accepted: %+v", opts)
		}
	}
}

func TestPerDeviceMetadataFailuresAreReportedSeparately(t *testing.T) {
	for _, kind := range []string{"version", "memory", "topology"} {
		t.Run(kind, func(t *testing.T) {
			d := smdtest.Device("rbln0", false)
			s := &smdtest.Server{Errors: map[string]error{}}
			if kind == "topology" {
				s.TopologyUnreachable = []*pb.EntityStatus{{Ref: d.Name}}
			} else {
				if kind == "version" {
					d.Versions = nil
				} else {
					d.Memory = nil
				}
				s.Errors[kind] = status.Error(codes.NotFound, "temporarily unavailable")
			}
			s.Set([]*pb.DeviceInfo{d}, []*pb.MetricSample{smdtest.Int(d.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 0)})
			got := collect(t, startFake(t, s))[0]
			if got.CollectionSucceeded {
				t.Fatal("per-device failure hidden")
			}
			if got.MemoryUsedBytes == nil || *got.MemoryUsedBytes != 0 {
				t.Fatal("valid telemetry discarded")
			}
			if health, known := got.Health(); !known || health != 0 {
				t.Fatal("collection failure changed READY health")
			}
		})
	}
}

func TestEmptySuccessfulSamplesDoNotInventTelemetryOrFailures(t *testing.T) {
	s := &smdtest.Server{}
	d := smdtest.Device("rbln0", false)
	d.State = pb.DeviceState_DEVICE_STATE_UNSPECIFIED
	s.Set([]*pb.DeviceInfo{d}, nil)
	got := collect(t, startFake(t, s))[0]
	if !got.CollectionSucceeded {
		t.Fatal("unsupported readings treated as failed queries")
	}
	if got.Temperature != nil || got.Power != nil || got.Utilization != nil || got.MemoryUsedBytes != nil {
		t.Fatal("missing samples fabricated")
	}
	if _, known := got.Health(); known {
		t.Fatal("successful query fabricated device health")
	}
}

func TestTLSRejectsUntrustedDaemon(t *testing.T) {
	s := &smdtest.Server{}
	transport := smdtest.NewTLS(t)
	addr, stop := smdtest.Serve(t, s, "127.0.0.1:0", transport)
	t.Cleanup(stop)
	untrusted := smdtest.NewTLS(t)
	opts := DefaultOptions()
	opts.TLSCA = untrusted.CAFile
	client, err := NewLazyClient(addr, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.GetDeviceInfo(ctx); err == nil {
		t.Fatal("accepted an untrusted server certificate")
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.InventoryRequests) != 0 {
		t.Fatal("untrusted connection reached the daemon")
	}
}
