package daemon

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/smdtest"
	legacy "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

func TestLegacyStateNumbersAndReadiness(t *testing.T) {
	for _, tc := range []struct {
		old  legacy.DeviceStatus
		want pb.DeviceState
	}{
		{legacy.DeviceStatus_READY, pb.DeviceState_DEVICE_STATE_READY},
		{legacy.DeviceStatus_BUSY, pb.DeviceState_DEVICE_STATE_BUSY},
		{legacy.DeviceStatus_INIT, pb.DeviceState_DEVICE_STATE_INIT},
		{legacy.DeviceStatus_FAULT, pb.DeviceState_DEVICE_STATE_FAULT},
		{legacy.DeviceStatus_FINISH, pb.DeviceState_DEVICE_STATE_FINISH},
		{legacy.DeviceStatus_NOT_FOUND, pb.DeviceState_DEVICE_STATE_UNSPECIFIED},
		{legacy.DeviceStatus(99), pb.DeviceState_DEVICE_STATE_UNSPECIFIED},
	} {
		if got := legacyState(tc.old); got != tc.want {
			t.Fatalf("%v mapped to %v instead of %v", tc.old, got, tc.want)
		}
	}
}

func TestV1UnknownStateUsesLegacyStateWithoutReplacingReadings(t *testing.T) {
	a, b := smdtest.Device("rbln0", false), smdtest.Device("rbln1", false)
	a.State = pb.DeviceState_DEVICE_STATE_UNSPECIFIED
	b.State = pb.DeviceState_DEVICE_STATE_FAULT
	old := &smdtest.LegacyServer{Infos: []*legacy.DeviceInfo{
		{Name: a.Name, Uuid: a.Uuid, DevStatus: legacy.DeviceStatus_BUSY, ErrStatus: legacy.Status_SUCCEED, UsedMem: 99},
		{Name: b.Name, Uuid: a.Uuid, DevStatus: legacy.DeviceStatus_READY, ErrStatus: legacy.Status_SUCCEED},
	}}
	s := &smdtest.Server{Legacy: old}
	s.Set([]*pb.DeviceInfo{a, b}, []*pb.MetricSample{smdtest.Int(a.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 123), smdtest.Float(a.Name, pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, 12500)})
	got := collect(t, startFake(t, s))
	if got[0].DevState != pb.DeviceState_DEVICE_STATE_BUSY || got[1].DevState != pb.DeviceState_DEVICE_STATE_FAULT {
		t.Fatalf("state selection: %+v", got)
	}
	if *got[0].MemoryUsedBytes != 123 || *got[0].Utilization != 12.5 {
		t.Fatal("v0 overwrote v1 telemetry")
	}
	if h, ok := got[0].Health(); !ok || h != 1 {
		t.Fatal("err_status substituted for actual BUSY state")
	}
	old.Mu.Lock()
	defer old.Mu.Unlock()
	if old.Calls["total"] != 1 || old.Calls["devices"] != 0 || old.Calls["version"] != 0 {
		t.Fatalf("unnecessary legacy calls: %v", old.Calls)
	}
}

func TestKnownV1StateNeverQueriesLegacy(t *testing.T) {
	old := &smdtest.LegacyServer{Errors: map[string]error{"total": status.Error(codes.PermissionDenied, "must not call")}}
	s := &smdtest.Server{Legacy: old}
	s.Set([]*pb.DeviceInfo{smdtest.Device("rbln0", false)}, nil)
	collect(t, startFake(t, s))
	old.Mu.Lock()
	defer old.Mu.Unlock()
	if len(old.Calls) != 0 {
		t.Fatalf("legacy queried: %v", old.Calls)
	}
}

func TestLegacyStateErrorsKeepV1ReadingsAndReportPartialFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.PermissionDenied, codes.Unimplemented} {
		t.Run(code.String(), func(t *testing.T) {
			d := smdtest.Device("rbln0", false)
			d.State = pb.DeviceState_DEVICE_STATE_UNSPECIFIED
			old := &smdtest.LegacyServer{Infos: []*legacy.DeviceInfo{{Name: d.Name, DevStatus: legacy.DeviceStatus_READY}}, Errors: map[string]error{"total": status.Error(code, "state read failed")}, TotalErrorAfterSend: true}
			s := &smdtest.Server{Legacy: old}
			s.Set([]*pb.DeviceInfo{d}, []*pb.MetricSample{smdtest.Int(d.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 321)})
			got := collect(t, startFake(t, s))[0]
			if got.DevState != pb.DeviceState_DEVICE_STATE_UNSPECIFIED || *got.MemoryUsedBytes != 321 {
				t.Fatal("partial legacy stream applied or v1 data discarded")
			}
			if got.CollectionSucceeded != (code == codes.Unimplemented) {
				t.Fatal("incorrect per-device query result")
			}
		})
	}
}

func TestLegacyOnlyDaemonCollectsPFAndVF(t *testing.T) {
	old := &smdtest.LegacyServer{
		Devices: []*legacy.Device{{Name: "rbln0", Uuid: "shared", NumVfs: 4}, {Name: "rbln0-0", Uuid: "shared", IsVf: true, ParentName: "rbln0"}},
		Infos: []*legacy.DeviceInfo{
			{Name: "rbln0", Uuid: "shared", Temperature: 35000, Watt: 50000000, TotalMem: 140, Utilization: 10, DevStatus: legacy.DeviceStatus_READY},
			{Name: "rbln0-0", Uuid: "shared", Temperature: 0, Watt: 0, TotalMem: 35, UsedMem: 0.5, Utilization: 12.5, DevStatus: legacy.DeviceStatus_BUSY, PState: 2},
		},
		Entries: []*legacy.TopologyEntry{{DeviceName: "rbln0", PcieLinkSpeed: "32 GT/s", PcieLinkWidth: "x16"}},
	}
	s := &smdtest.Server{Legacy: old, LegacyOnly: true}
	got := collect(t, startFake(t, s))
	if len(got) != 2 || got[0].Compute || !got[1].Compute {
		t.Fatalf("legacy inventory: %+v", got)
	}
	for _, d := range got {
		if d.SID != "" {
			t.Fatalf("legacy API has no board serial: %+v", d)
		}
	}
	if got[0].MemoryTotalBytes != nil || got[0].Utilization != nil || *got[0].Power != 50 || *got[0].Temperature != 35 || *got[0].PCIeLinkWidth != 16 {
		t.Fatalf("PF scope/units: %+v", got[0])
	}
	if got[1].Power != nil || got[1].Temperature != nil || *got[1].MemoryTotalBytes != 35<<30 || *got[1].MemoryUsedBytes != 1<<29 || *got[1].Utilization != 12.5 {
		t.Fatalf("VF scope/units: %+v", got[1])
	}
	if h, ok := got[1].Health(); !ok || h != 1 {
		t.Fatal("legacy BUSY state lost")
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.SampleCalls != 0 || len(s.Created) != 0 {
		t.Fatal("legacy collection created a metric group")
	}
}

func TestLegacyFailedReadingsDoNotReportSuccessfulCollection(t *testing.T) {
	for _, state := range []legacy.DeviceStatus{legacy.DeviceStatus_READY, legacy.DeviceStatus_BUSY, legacy.DeviceStatus_FAULT} {
		t.Run(state.String(), func(t *testing.T) {
			old := &smdtest.LegacyServer{
				Devices: []*legacy.Device{{Name: "rbln0"}, {Name: "rbln1"}},
				Infos: []*legacy.DeviceInfo{
					{Name: "rbln0", DevStatus: state, ErrStatus: legacy.Status_FAILED, UsedMem: 1, Temperature: 35000},
					{Name: "rbln1", DevStatus: legacy.DeviceStatus_READY, UsedMem: 2},
				},
			}
			c := startFake(t, &smdtest.Server{Legacy: old, LegacyOnly: true})
			got := collect(t, c)
			if got[0].CollectionSucceeded || got[0].Temperature != nil || got[0].MemoryUsedBytes != nil || got[0].PState != nil {
				t.Fatalf("FAILED response treated as valid telemetry: %+v", got[0])
			}
			if got[0].DevState != legacyState(state) {
				t.Fatal("query failure replaced the device's lifecycle state")
			}
			if !got[1].CollectionSucceeded || got[1].MemoryUsedBytes == nil || *got[1].MemoryUsedBytes != 2<<30 {
				t.Fatalf("failure affected another device: %+v", got[1])
			}
			old.Mu.Lock()
			old.Infos[0].ErrStatus = legacy.Status_SUCCEED
			old.Mu.Unlock()
			recovered := collect(t, c)[0]
			if !recovered.CollectionSucceeded || recovered.MemoryUsedBytes == nil || *recovered.MemoryUsedBytes != 1<<30 || recovered.DevState != legacyState(state) {
				t.Fatalf("collection did not recover independently of readiness: %+v", recovered)
			}
		})
	}
}

func TestV1FailuresAreNotDowngradedToLegacy(t *testing.T) {
	for _, method := range []string{"inventory", "topology", "groups", "create", "samples"} {
		for _, code := range []codes.Code{codes.PermissionDenied, codes.Unavailable} {
			t.Run(method+code.String(), func(t *testing.T) {
				old := &smdtest.LegacyServer{}
				s := &smdtest.Server{Legacy: old, Errors: map[string]error{method: status.Error(code, "failed")}}
				s.Set([]*pb.DeviceInfo{smdtest.Device("rbln0", false)}, nil)
				_, err := startFake(t, s).GetDeviceInfo(context.Background())
				if status.Code(err) != code {
					t.Fatalf("error=%v", err)
				}
				old.Mu.Lock()
				defer old.Mu.Unlock()
				if len(old.Calls) != 0 {
					t.Fatalf("downgraded after %v: %v", err, old.Calls)
				}
			})
		}
	}
}

func TestLegacyHostingPFWithoutVFCountDoesNotReportZero(t *testing.T) {
	old := &smdtest.LegacyServer{
		Devices: []*legacy.Device{{Name: "rbln0"}, {Name: "rbln0-0", IsVf: true, ParentName: "rbln0"}},
		Infos:   []*legacy.DeviceInfo{{Name: "rbln0"}, {Name: "rbln0-0"}},
	}
	got := collect(t, startFake(t, &smdtest.Server{Legacy: old, LegacyOnly: true}))
	if got[0].Compute || got[0].NumVFs != nil || got[0].PartitionMode != "hosting" {
		t.Fatalf("misclassified hosting PF: %+v", got[0])
	}
}

func TestAPIAvailabilityIsRecheckedAfterDaemonUpgrade(t *testing.T) {
	old := &smdtest.LegacyServer{Devices: []*legacy.Device{{Name: "rbln0"}}, Infos: []*legacy.DeviceInfo{{Name: "rbln0", UsedMem: 1, DevStatus: legacy.DeviceStatus_READY}}}
	s := &smdtest.Server{Legacy: old, Errors: map[string]error{"inventory": status.Error(codes.Unimplemented, "stub")}}
	d := smdtest.Device("rbln0", false)
	s.Set([]*pb.DeviceInfo{d}, []*pb.MetricSample{smdtest.Int(d.Name, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES, 123)})
	c := startFake(t, s)
	if got := collect(t, c); *got[0].MemoryUsedBytes != 1<<30 {
		t.Fatal("v0 not used")
	}
	s.Mu.Lock()
	s.Errors = nil
	s.Mu.Unlock()
	if got := collect(t, c); *got[0].MemoryUsedBytes != 123 {
		t.Fatal("v1 was not retried after upgrade")
	}
	old.Mu.Lock()
	defer old.Mu.Unlock()
	if old.Calls["total"] != 1 {
		t.Fatal("continued reading v0 with complete v1 support")
	}
}
