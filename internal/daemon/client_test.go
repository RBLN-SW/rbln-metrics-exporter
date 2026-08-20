package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
	"github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
)

// fakeDaemon serves the RPC surface GetDeviceInfo touches, with a mutable
// device/total-info view so tests can drive per-cycle transitions.
type fakeDaemon struct {
	rblnservicespb.UnimplementedRBLNServicesServer
	mu          sync.Mutex
	serviceable []*rblnservicespb.Device
	totalInfos  []*rblnservicespb.DeviceInfo
}

func (f *fakeDaemon) set(devices []*rblnservicespb.Device, infos []*rblnservicespb.DeviceInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serviceable, f.totalInfos = devices, infos
}

func (f *fakeDaemon) GetServiceableDeviceList(_ *rblnservicespb.Empty, stream grpc.ServerStreamingServer[rblnservicespb.Device]) error {
	f.mu.Lock()
	devices := append([]*rblnservicespb.Device(nil), f.serviceable...)
	f.mu.Unlock()
	for _, d := range devices {
		if err := stream.Send(d); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeDaemon) GetTotalInfo(_ *rblnservicespb.Empty, stream grpc.ServerStreamingServer[rblnservicespb.DeviceInfo]) error {
	f.mu.Lock()
	infos := append([]*rblnservicespb.DeviceInfo(nil), f.totalInfos...)
	f.mu.Unlock()
	for _, i := range infos {
		if err := stream.Send(i); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeDaemon) GetVersion(context.Context, *rblnservicespb.Device) (*rblnservicespb.VersionInfo, error) {
	return &rblnservicespb.VersionInfo{ErrStatus: rblnservicespb.Status_SUCCEED, SmcVersion: "1.0.0"}, nil
}

func (f *fakeDaemon) RblnListTopology(context.Context, *rblnservicespb.DeviceFilter) (*rblnservicespb.RblnListTopologyResponse, error) {
	return &rblnservicespb.RblnListTopologyResponse{}, nil
}

func startFake(t *testing.T, fake *fakeDaemon) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := grpc.NewServer()
	rblnservicespb.RegisterRBLNServicesServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := NewLazyClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
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

func recordsWithMsg(recs []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, m := range recs {
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

func dev(name string) *rblnservicespb.Device {
	return &rblnservicespb.Device{Name: name, Uuid: "uuid-" + name}
}

func info(name string, status rblnservicespb.DeviceStatus, errStatus rblnservicespb.Status) *rblnservicespb.DeviceInfo {
	return &rblnservicespb.DeviceInfo{Name: name, DevStatus: status, ErrStatus: errStatus}
}

func collect(t *testing.T, c *Client) {
	t.Helper()
	if _, err := c.GetDeviceInfo(context.Background()); err != nil {
		t.Fatalf("GetDeviceInfo: %v", err)
	}
}

func TestGetDeviceInfoLogsFirstSuccessOnce(t *testing.T) {
	fake := &fakeDaemon{}
	fake.set(
		[]*rblnservicespb.Device{dev("rbln0"), dev("rbln1")},
		[]*rblnservicespb.DeviceInfo{
			info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
			info("rbln1", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
		},
	)
	client := startFake(t, fake)
	buf := captureLogs(t)

	collect(t, client)
	collect(t, client)

	recs := recordsWithMsg(jsonRecords(t, buf), "First collection succeeded")
	if len(recs) != 1 {
		t.Fatalf("got %d first-success records, want 1: %s", len(recs), buf.String())
	}
	m := recs[0]
	if m["level"] != "info" || m["devices"] != float64(2) {
		t.Fatalf("record = %v, want info with devices=2", m)
	}
	if ep, ok := m["endpoint"].(string); !ok || ep == "" {
		t.Fatalf("endpoint = %v, want non-empty daemon endpoint", m["endpoint"])
	}
}

func TestGetDeviceInfoMissingDeviceEdges(t *testing.T) {
	present := []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
	}
	fake := &fakeDaemon{}
	fake.set([]*rblnservicespb.Device{dev("rbln0")}, present)
	client := startFake(t, fake)
	buf := captureLogs(t)

	collect(t, client) // healthy baseline
	fake.set([]*rblnservicespb.Device{dev("rbln0")}, nil)
	collect(t, client) // goes missing -> one warn
	collect(t, client) // still missing -> no repeat
	fake.set([]*rblnservicespb.Device{dev("rbln0")}, present)
	collect(t, client) // returns -> one info

	recs := jsonRecords(t, buf)
	missing := recordsWithMsg(recs, "Device missing from total info")
	if len(missing) != 1 {
		t.Fatalf("got %d missing warns, want 1 (edge-triggered): %s", len(missing), buf.String())
	}
	m := missing[0]
	if m["level"] != "warn" || m["device"] != "rbln0" {
		t.Fatalf("record = %v, want warn for rbln0", m)
	}
	if _, ok := m["effect"].(string); !ok {
		t.Fatalf("effect = %v, want degradation effect string", m["effect"])
	}
	returned := recordsWithMsg(recs, "Device returned to total info")
	if len(returned) != 1 || returned[0]["device"] != "rbln0" {
		t.Fatalf("got %d returned records, want 1 for rbln0: %s", len(returned), buf.String())
	}
}

func TestGetDeviceInfoDegradedEdges(t *testing.T) {
	devices := []*rblnservicespb.Device{dev("rbln0")}
	fake := &fakeDaemon{}
	fake.set(devices, []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
	})
	client := startFake(t, fake)
	buf := captureLogs(t)

	collect(t, client) // healthy baseline
	fake.set(devices, []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_BUSY, rblnservicespb.Status_SUCCEED),
	})
	collect(t, client) // READY->BUSY churn must stay silent
	fake.set(devices, []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_FAULT, rblnservicespb.Status_FAILED),
	})
	collect(t, client) // degrades -> one warn
	collect(t, client) // still degraded -> no repeat
	fake.set(devices, []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
	})
	collect(t, client) // recovers -> one info

	recs := jsonRecords(t, buf)
	degraded := recordsWithMsg(recs, "Device entered degraded state")
	if len(degraded) != 1 {
		t.Fatalf("got %d degraded warns, want 1 (edge-triggered, silent on READY->BUSY): %s",
			len(degraded), buf.String())
	}
	m := degraded[0]
	if m["level"] != "warn" || m["device"] != "rbln0" {
		t.Fatalf("record = %v, want warn for rbln0", m)
	}
	if m["state"] != "FAULT" || m["errStatus"] != float64(rblnservicespb.Status_FAILED) {
		t.Fatalf("state/errStatus = %v/%v, want FAULT/1", m["state"], m["errStatus"])
	}
	recovered := recordsWithMsg(recs, "Device recovered")
	if len(recovered) != 1 || recovered[0]["device"] != "rbln0" {
		t.Fatalf("got %d recovered records, want 1 for rbln0: %s", len(recovered), buf.String())
	}
}

func TestGetDeviceInfoDegradedVisibleOnFirstCollection(t *testing.T) {
	fake := &fakeDaemon{}
	fake.set([]*rblnservicespb.Device{dev("rbln0")}, []*rblnservicespb.DeviceInfo{
		info("rbln0", rblnservicespb.DeviceStatus_FAULT, rblnservicespb.Status_FAILED),
	})
	client := startFake(t, fake)
	buf := captureLogs(t)

	collect(t, client) // a device that is already sick at startup must be visible

	degraded := recordsWithMsg(jsonRecords(t, buf), "Device entered degraded state")
	if len(degraded) != 1 {
		t.Fatalf("got %d degraded warns, want 1 on first collection: %s", len(degraded), buf.String())
	}
}

func TestGetDeviceInfoVanishedDeviceWarnsOnce(t *testing.T) {
	fake := &fakeDaemon{}
	fake.set(
		[]*rblnservicespb.Device{dev("rbln0"), dev("rbln1")},
		[]*rblnservicespb.DeviceInfo{
			info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
			info("rbln1", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
		},
	)
	client := startFake(t, fake)
	buf := captureLogs(t)

	collect(t, client) // baseline: two devices
	fake.set(
		[]*rblnservicespb.Device{dev("rbln0")},
		[]*rblnservicespb.DeviceInfo{
			info("rbln0", rblnservicespb.DeviceStatus_READY, rblnservicespb.Status_SUCCEED),
		},
	)
	collect(t, client) // rbln1 falls off the serviceable list -> one warn
	collect(t, client) // still gone -> no repeat

	recs := jsonRecords(t, buf)
	vanished := recordsWithMsg(recs, "Device no longer serviceable")
	if len(vanished) != 1 {
		t.Fatalf("got %d vanished warns, want 1: %s", len(vanished), buf.String())
	}
	m := vanished[0]
	if m["level"] != "warn" || m["device"] != "rbln1" {
		t.Fatalf("record = %v, want warn for rbln1", m)
	}
	if returned := recordsWithMsg(recs, "Device returned to total info"); len(returned) != 0 {
		t.Fatalf("vanished device must not be reported as returned: %v", returned)
	}
}
