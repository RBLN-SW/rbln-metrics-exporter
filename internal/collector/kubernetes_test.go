package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc"
	podResourcesAPI "k8s.io/kubelet/pkg/apis/podresources/v1"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

const (
	dpResource = "rebellions.ai/ATOM"
	draDriver  = "npu.rebellions.ai"
)

func devicePluginContainer(containerName, resourceName string, deviceIDs ...string) *podResourcesAPI.ContainerResources {
	return &podResourcesAPI.ContainerResources{
		Name:    containerName,
		Devices: []*podResourcesAPI.ContainerDevices{{ResourceName: resourceName, DeviceIds: deviceIDs}},
	}
}

// The shape kubelet builds from a driver's Prepare response.
func draContainer(containerName, claimName, driverName string, deviceNames ...string) *podResourcesAPI.ContainerResources {
	return &podResourcesAPI.ContainerResources{
		Name:             containerName,
		DynamicResources: []*podResourcesAPI.DynamicResource{draClaim(claimName, driverName, deviceNames...)},
	}
}

func draClaim(claimName, driverName string, deviceNames ...string) *podResourcesAPI.DynamicResource {
	claimResources := make([]*podResourcesAPI.ClaimResource, 0, len(deviceNames))
	for _, deviceName := range deviceNames {
		claimResources = append(claimResources, &podResourcesAPI.ClaimResource{
			DriverName: driverName,
			DeviceName: deviceName,
		})
	}
	return &podResourcesAPI.DynamicResource{ClaimName: claimName, ClaimResources: claimResources}
}

func podResources(podNamespace, podName string, containers ...*podResourcesAPI.ContainerResources) *podResourcesAPI.PodResources {
	return &podResourcesAPI.PodResources{Name: podName, Namespace: podNamespace, Containers: containers}
}

func response(pods ...*podResourcesAPI.PodResources) *podResourcesAPI.ListPodResourcesResponse {
	return &podResourcesAPI.ListPodResourcesResponse{PodResources: pods}
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

func TestDeviceMapFromPodResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		resp       *podResourcesAPI.ListPodResourcesResponse
		want       map[DeviceName]PodResourceInfo
		wantShared []DeviceName
	}{
		{
			name: "no pods on the node",
			resp: response(),
			want: map[DeviceName]PodResourceInfo{},
		},
		{
			name: "pod holding no rbln devices",
			resp: response(podResources("team-a", "cpu-pod", &podResourcesAPI.ContainerResources{Name: "worker"})),
			want: map[DeviceName]PodResourceInfo{},
		},
		{
			name: "device plugin resource maps every device id",
			resp: response(podResources("team-a", "dp-pod", devicePluginContainer("worker", dpResource, "rbln0", "rbln1"))),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "dp-pod", Namespace: "team-a", ContainerName: "worker"},
				"rbln1": {Name: "dp-pod", Namespace: "team-a", ContainerName: "worker"},
			},
		},
		{
			name: "container requesting two device plugin resources",
			resp: response(podResources("team-a", "mixed-pod", &podResourcesAPI.ContainerResources{
				Name: "worker",
				Devices: []*podResourcesAPI.ContainerDevices{
					{ResourceName: "rebellions.ai/ATOM", DeviceIds: []string{"rbln0"}},
					{ResourceName: "rebellions.ai/REBEL", DeviceIds: []string{"rbln1"}},
				},
			})),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "mixed-pod", Namespace: "team-a", ContainerName: "worker"},
				"rbln1": {Name: "mixed-pod", Namespace: "team-a", ContainerName: "worker"},
			},
		},
		{
			name: "dra claim maps every allocated device",
			resp: response(podResources("team-b", "dra-pod", draContainer("worker", "dra-pod-claim", draDriver, "rbln2", "rbln3"))),
			want: map[DeviceName]PodResourceInfo{
				"rbln2": {Name: "dra-pod", Namespace: "team-b", ContainerName: "worker"},
				"rbln3": {Name: "dra-pod", Namespace: "team-b", ContainerName: "worker"},
			},
		},
		{
			name: "container holding two dra claims",
			resp: response(podResources("team-b", "two-claim-pod", &podResourcesAPI.ContainerResources{
				Name: "worker",
				DynamicResources: []*podResourcesAPI.DynamicResource{
					draClaim("claim-a", draDriver, "rbln0"),
					draClaim("claim-b", draDriver, "rbln1"),
				},
			})),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "two-claim-pod", Namespace: "team-b", ContainerName: "worker"},
				"rbln1": {Name: "two-claim-pod", Namespace: "team-b", ContainerName: "worker"},
			},
		},
		{
			name: "claim allocating from two drivers keeps only rbln results",
			resp: response(podResources("team-b", "multi-driver-pod", &podResourcesAPI.ContainerResources{
				Name: "worker",
				DynamicResources: []*podResourcesAPI.DynamicResource{{
					ClaimName: "mixed-claim",
					ClaimResources: []*podResourcesAPI.ClaimResource{
						{DriverName: draDriver, DeviceName: "rbln0"},
						{DriverName: "gpu.nvidia.com", DeviceName: "gpu-0"},
					},
				}},
			})),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "multi-driver-pod", Namespace: "team-b", ContainerName: "worker"},
			},
		},
		{
			name: "driver named as the bare vendor domain still matches",
			resp: response(podResources("team-b", "bare-pod", draContainer("worker", "bare-claim", "rebellions.ai", "rbln0"))),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "bare-pod", Namespace: "team-b", ContainerName: "worker"},
			},
		},
		{
			name: "driver whose name merely ends in the vendor domain is ignored",
			resp: response(podResources("team-c", "lookalike-pod",
				draContainer("worker", "lookalike-claim", "npu.notrebellions.ai", "rbln0"))),
			want: map[DeviceName]PodResourceInfo{},
		},
		{
			name: "device plugin and dra pods coexist on one node",
			resp: response(
				podResources("team-a", "dp-pod", devicePluginContainer("worker", dpResource, "rbln0")),
				podResources("team-b", "dra-pod", draContainer("worker", "dra-pod-claim", draDriver, "rbln1")),
			),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "dp-pod", Namespace: "team-a", ContainerName: "worker"},
				"rbln1": {Name: "dra-pod", Namespace: "team-b", ContainerName: "worker"},
			},
		},
		{
			name: "other vendors are ignored on both paths",
			resp: response(podResources("team-c", "other-pod",
				devicePluginContainer("worker", "nvidia.com/gpu", "GPU-0"),
				draContainer("worker", "gpu-claim", "gpu.nvidia.com", "gpu-0"),
			)),
			want: map[DeviceName]PodResourceInfo{},
		},
		{
			name: "dra claim results without a device name are ignored",
			resp: response(podResources("team-d", "future-pod", draContainer("worker", "future-claim", draDriver, ""))),
			want: map[DeviceName]PodResourceInfo{},
		},
		{
			name: "sidecar sharing its pod's claim does not count as shared",
			resp: response(podResources("team-b", "sidecar-pod",
				draContainer("worker", "pod-claim", draDriver, "rbln0"),
				draContainer("logger", "pod-claim", draDriver, "rbln0"),
			)),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "sidecar-pod", Namespace: "team-b", ContainerName: "logger"},
			},
		},
		{
			name: "containers of one pod holding separate devices",
			resp: response(podResources("team-b", "two-container-pod",
				draContainer("worker", "claim-a", draDriver, "rbln0"),
				draContainer("logger", "claim-b", draDriver, "rbln1"),
			)),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "two-container-pod", Namespace: "team-b", ContainerName: "worker"},
				"rbln1": {Name: "two-container-pod", Namespace: "team-b", ContainerName: "logger"},
			},
		},
		{
			name: "two pods sharing one claim",
			resp: response(
				podResources("team-a", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln0")),
				podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")),
			),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "consumer-a", Namespace: "team-a", ContainerName: "worker"},
			},
			wantShared: []DeviceName{"rbln0"},
		},
		{
			name: "one claim sharing several devices across pods",
			resp: response(
				podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln2", "rbln0", "rbln1")),
				podResources("team-b", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln2", "rbln0", "rbln1")),
			),
			want: map[DeviceName]PodResourceInfo{
				"rbln0": {Name: "consumer-a", Namespace: "team-a", ContainerName: "worker"},
				"rbln1": {Name: "consumer-a", Namespace: "team-a", ContainerName: "worker"},
				"rbln2": {Name: "consumer-a", Namespace: "team-a", ContainerName: "worker"},
			},
			wantShared: []DeviceName{"rbln0", "rbln1", "rbln2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, shared := deviceMapFromPodResources(tt.resp)
			if !maps.Equal(tt.want, got) {
				t.Errorf("device map: want %v, got %v", tt.want, got)
			}
			if !slices.Equal(tt.wantShared, shared) {
				t.Errorf("shared devices: want %v, got %v", tt.wantShared, shared)
			}
		})
	}
}

// Guards the tie-break in PodResourceInfo.before: the same allocations arriving
// in a different order must produce the same labels.
func TestDeviceMapFromPodResourcesIsOrderIndependent(t *testing.T) {
	t.Parallel()

	first := podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0"))
	second := podResources("team-a", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln0"))
	want := map[DeviceName]PodResourceInfo{
		"rbln0": {Name: "consumer-a", Namespace: "team-a", ContainerName: "worker"},
	}

	for _, tt := range []struct {
		name string
		resp *podResourcesAPI.ListPodResourcesResponse
	}{
		{name: "already ordered", resp: response(first, second)},
		{name: "reverse ordered", resp: response(second, first)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, shared := deviceMapFromPodResources(tt.resp)
			if !maps.Equal(want, got) {
				t.Errorf("device map: want %v, got %v", want, got)
			}
			if !slices.Equal([]DeviceName{"rbln0"}, shared) {
				t.Errorf("shared devices: want [rbln0], got %v", shared)
			}
		})
	}
}

// fakePodResourcesClient serves one canned List response, swappable between
// syncs so a test can drive the mapper across state transitions.
type fakePodResourcesClient struct {
	resp *podResourcesAPI.ListPodResourcesResponse
	err  error
}

func (f *fakePodResourcesClient) List(context.Context, *podResourcesAPI.ListPodResourcesRequest, ...grpc.CallOption) (*podResourcesAPI.ListPodResourcesResponse, error) {
	return f.resp, f.err
}

// The mapper only calls List; the remaining methods exist to satisfy the
// interface and panic so that a new call site cannot go unnoticed.
func (f *fakePodResourcesClient) GetAllocatableResources(context.Context, *podResourcesAPI.AllocatableResourcesRequest, ...grpc.CallOption) (*podResourcesAPI.AllocatableResourcesResponse, error) {
	panic("GetAllocatableResources: unexpected call")
}

func (f *fakePodResourcesClient) Get(context.Context, *podResourcesAPI.GetPodResourcesRequest, ...grpc.CallOption) (*podResourcesAPI.GetPodResourcesResponse, error) {
	panic("Get: unexpected call")
}

func newTestMapper(client *fakePodResourcesClient) *PodResourceMapper {
	return &PodResourceMapper{
		podResourcesByDevice: make(map[DeviceName]PodResourceInfo),
		client:               client,
	}
}

func mustSync(t *testing.T, m *PodResourceMapper) {
	t.Helper()
	if err := m.syncPodResources(); err != nil {
		t.Fatalf("syncPodResources: %v", err)
	}
}

func TestSnapshotReturnsAnIndependentCopyOfTheLastSync(t *testing.T) {
	m := newTestMapper(&fakePodResourcesClient{
		resp: response(podResources("team-a", "dp-pod", devicePluginContainer("worker", dpResource, "rbln0"))),
	})
	mustSync(t, m)

	snapshot := m.Snapshot()
	want := map[DeviceName]PodResourceInfo{
		"rbln0": {Name: "dp-pod", Namespace: "team-a", ContainerName: "worker"},
	}
	if !maps.Equal(want, snapshot) {
		t.Fatalf("snapshot: want %v, got %v", want, snapshot)
	}

	delete(snapshot, "rbln0")
	if !maps.Equal(want, m.Snapshot()) {
		t.Errorf("mutating a snapshot changed the mapper: %v", m.Snapshot())
	}
}

// A failed sync must not blank the mapping: stale attribution beats none until
// the next sync succeeds, which is what the failure record promises.
func TestFailedSyncKeepsThePreviousMapping(t *testing.T) {
	client := &fakePodResourcesClient{
		resp: response(podResources("team-a", "dp-pod", devicePluginContainer("worker", dpResource, "rbln0"))),
	}
	m := newTestMapper(client)
	mustSync(t, m)

	client.err = errors.New("kubelet down")
	if err := m.syncPodResources(); err == nil {
		t.Fatal("syncPodResources: want error, got nil")
	}

	want := map[DeviceName]PodResourceInfo{
		"rbln0": {Name: "dp-pod", Namespace: "team-a", ContainerName: "worker"},
	}
	if !maps.Equal(want, m.Snapshot()) {
		t.Errorf("mapping after failed sync: want %v, got %v", want, m.Snapshot())
	}
}

func TestSharedDevicesAreReportedOnceAndOnResolution(t *testing.T) {
	sharedResp := response(
		podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")),
		podResources("team-b", "consumer-b", draContainer("worker", "shared-claim", draDriver, "rbln0")),
	)
	soleResp := response(podResources("team-a", "consumer-a", draContainer("worker", "shared-claim", draDriver, "rbln0")))

	buf := captureLogs(t)
	client := &fakePodResourcesClient{resp: sharedResp}
	m := newTestMapper(client)

	mustSync(t, m)
	mustSync(t, m)

	recs := jsonRecords(t, buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records while sharing stayed steady, want 1: %s", len(recs), buf.String())
	}
	if recs[0]["level"] != "warn" || recs[0]["msg"] != "Devices claimed by more than one pod" {
		t.Fatalf("record 0 = %v, want the sharing warning", recs[0])
	}

	client.resp = soleResp
	mustSync(t, m)

	recs = jsonRecords(t, buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records after sharing ended, want 2: %s", len(recs), buf.String())
	}
	if recs[1]["level"] != "info" || recs[1]["msg"] != "Device sharing resolved" {
		t.Fatalf("record 1 = %v, want the resolution record", recs[1])
	}
}

func TestSteadyExclusiveAllocationStaysQuiet(t *testing.T) {
	buf := captureLogs(t)
	m := newTestMapper(&fakePodResourcesClient{
		resp: response(podResources("team-a", "dp-pod", devicePluginContainer("worker", dpResource, "rbln0"))),
	})

	mustSync(t, m)
	mustSync(t, m)

	if recs := jsonRecords(t, buf); len(recs) != 0 {
		t.Fatalf("got %d records on exclusive allocation, want 0: %s", len(recs), buf.String())
	}
}
