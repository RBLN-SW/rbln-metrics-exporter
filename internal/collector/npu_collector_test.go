package collector

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/daemon"
	"github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
)

// fakeDaemon serves the minimal RPC surface a collection cycle touches and
// counts getClockInfo calls. On ATOM, getClockInfo is a message-ring device
// job that wakes an idle device out of LPM, so the collection cycle must
// never issue it (CA25 idle-power regression, exporter 0.3.x).
type fakeDaemon struct {
	rblnservicespb.UnimplementedRBLNServicesServer
	clockInfoCalls atomic.Int32
}

func (f *fakeDaemon) GetServiceableDeviceList(_ *rblnservicespb.Empty, stream grpc.ServerStreamingServer[rblnservicespb.Device]) error {
	return stream.Send(&rblnservicespb.Device{
		Name:     "rbln0",
		DevId:    "1120",
		Uuid:     "00000000-0000-0000-0000-000000000000",
		CardName: "RBLN-CA25",
	})
}

func (f *fakeDaemon) GetTotalInfo(_ *rblnservicespb.Empty, stream grpc.ServerStreamingServer[rblnservicespb.DeviceInfo]) error {
	return stream.Send(&rblnservicespb.DeviceInfo{
		Name:        "rbln0",
		Temperature: 35000,
		Watt:        40_000_000,
		PState:      2,
	})
}

func (f *fakeDaemon) GetVersion(context.Context, *rblnservicespb.Device) (*rblnservicespb.VersionInfo, error) {
	return &rblnservicespb.VersionInfo{ErrStatus: rblnservicespb.Status_SUCCEED, SmcVersion: "1.0.0"}, nil
}

func (f *fakeDaemon) RblnListTopology(context.Context, *rblnservicespb.DeviceFilter) (*rblnservicespb.RblnListTopologyResponse, error) {
	return &rblnservicespb.RblnListTopologyResponse{}, nil
}

func (f *fakeDaemon) GetClockInfo(context.Context, *rblnservicespb.Device) (*rblnservicespb.ClockInfo, error) {
	f.clockInfoCalls.Add(1)
	return &rblnservicespb.ClockInfo{ErrStatus: rblnservicespb.Status_SUCCEED, Dc_0Clock: 1500}, nil
}

func TestCollectionCycleDoesNotQueryClocks(t *testing.T) {
	fake := &fakeDaemon{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := grpc.NewServer()
	rblnservicespb.RegisterRBLNServicesServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := daemon.NewLazyClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("failed to create daemon client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	registry := prometheus.NewRegistry()
	npc := NewNPUCollector(client, registry, false, NewNoopPodResourceMapper(), "test-node")
	npc.Register(registry)

	if err := npc.GetMetrics(context.Background()); err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}

	if n := fake.clockInfoCalls.Load(); n != 0 {
		t.Errorf("collection cycle issued %d getClockInfo call(s); it must never issue any (wakes idle ATOM devices out of LPM)", n)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, mf := range families {
		names[mf.GetName()] = true
	}
	if names["rbln_npu_clock_frequency_mhz"] {
		t.Error("rbln_npu_clock_frequency_mhz is exposed; the clock metric was removed and must stay removed")
	}
	if !names["rbln_npu_power_state"] {
		t.Error("rbln_npu_power_state missing; throttling visibility must be preserved via the p-state metric")
	}
}
