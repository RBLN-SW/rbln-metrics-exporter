package collector

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podResourcesAPI "k8s.io/kubelet/pkg/apis/podresources/v1"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

const (
	PodResourceSocket  = "/var/lib/kubelet/pod-resources/kubelet.sock"
	RBLNResourcePrefix = "rebellions.ai"
)

// isRBLNDRADriver reports whether a DRA driver name belongs to Rebellions. The
// driver calls itself "npu.rebellions.ai" by default — a subdomain of the vendor
// domain, and so never a match for RBLNResourcePrefix — but its DRIVER_NAME env
// var can override that, which is why the vendor domain is matched as a domain
// suffix rather than compared. An override that abandons the vendor domain
// cannot be recognised here and its devices go unattributed.
func isRBLNDRADriver(driverName string) bool {
	return driverName == RBLNResourcePrefix ||
		strings.HasSuffix(driverName, "."+RBLNResourcePrefix)
}

type DeviceName string

type PodResourceInfo struct {
	Name          string
	Namespace     string
	ContainerName string
}

// before reports which of two claimants of one device wins the labels. The
// order is arbitrary but total, which is the point: kubelet does not promise a
// stable pod order, so picking whichever came first in the response would make
// the labels flap between scrapes and split the device's series.
func (p PodResourceInfo) before(other PodResourceInfo) bool {
	if p.Namespace != other.Namespace {
		return p.Namespace < other.Namespace
	}
	if p.Name != other.Name {
		return p.Name < other.Name
	}
	return p.ContainerName < other.ContainerName
}

type PodResourceMapper struct {
	sync.RWMutex
	podResourcesByDevice map[DeviceName]PodResourceInfo
	// The previous sync's value, kept so steady-state sharing is reported
	// once instead of every cycle.
	sharedDevices []DeviceName
	syncRequests  chan struct{}

	client podResourcesAPI.PodResourcesListerClient
}

func NewPodResourceMapper(ctx context.Context) (*PodResourceMapper, error) {
	conn, cleanup, err := newKubeletClient()
	if err != nil {
		return nil, err
	}

	m := &PodResourceMapper{
		podResourcesByDevice: make(map[DeviceName]PodResourceInfo),
		syncRequests:         make(chan struct{}, 1),
		client:               podResourcesAPI.NewPodResourcesListerClient(conn),
	}

	if err := m.syncPodResources(); err != nil {
		slog.Warn("Initial pod resource sync failed", "err", err,
			"effect", "pod attribution missing until first successful sync")
	}

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	go m.runSyncLoop(ctx)
	return m, nil
}

func (p *PodResourceMapper) TriggerSync() {
	if p == nil || p.syncRequests == nil {
		return
	}
	select {
	case p.syncRequests <- struct{}{}:
	default:
	}
}

func (p *PodResourceMapper) Snapshot() map[DeviceName]PodResourceInfo {
	devices, _ := p.SharedSnapshot()
	return devices
}

// SharedSnapshot returns the last sync's device map along with the devices that
// sync found claimed by more than one pod. Both come from one critical section
// so a caller cannot label a device from one sync and read its sharing state
// from the next.
func (p *PodResourceMapper) SharedSnapshot() (map[DeviceName]PodResourceInfo, []DeviceName) {
	p.RLock()
	defer p.RUnlock()

	snapshot := make(map[DeviceName]PodResourceInfo, len(p.podResourcesByDevice))
	maps.Copy(snapshot, p.podResourcesByDevice)
	return snapshot, slices.Clone(p.sharedDevices)
}

func (p *PodResourceMapper) runSyncLoop(ctx context.Context) {
	for {
		select {
		case <-p.syncRequests:
			if err := p.syncPodResources(); err != nil {
				slog.Warn("Failed to sync pod resources", "err", err,
					"effect", "pod attribution stale until next successful sync")
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *PodResourceMapper) syncPodResources() error {
	podResources, err := p.getPodResources()
	if err != nil {
		return err
	}

	podResourcesInfo, shared := deviceMapFromPodResources(podResources)

	slog.Debug("Synced pod resources",
		"devices", len(podResourcesInfo), "pods", len(podResources.GetPodResources()))

	p.Lock()
	previouslyShared := p.sharedDevices
	p.podResourcesByDevice = podResourcesInfo
	p.sharedDevices = shared
	p.Unlock()

	if !slices.Equal(previouslyShared, shared) {
		if len(shared) > 0 {
			slog.Warn("Devices claimed by more than one pod",
				"devices", shared,
				"effect", "each device's pod labels name a single claimant; the others go unattributed")
		} else {
			slog.Info("Device sharing resolved", "devices", previouslyShared)
		}
	}

	return nil
}

// deviceMapFromPodResources joins the two shapes kubelet reports device
// assignments in: ContainerDevices for the device plugin, and DynamicResources
// for DRA. Both identify a device by its node name ("rbln0"), which is also
// what the daemon reports telemetry under, so one map serves both. The sources
// are unioned rather than switched between because a cluster can run the device
// plugin and the DRA driver at the same time.
//
// The second return value lists, sorted, the devices claimed by more than one
// pod. Only DRA produces those, through a ResourceClaim several pods reference;
// the device plugin allocates a device to one pod. Containers of a single pod
// may share a claim too, but that leaves the pod and namespace labels correct,
// so it is not reported.
func deviceMapFromPodResources(podResources *podResourcesAPI.ListPodResourcesResponse) (map[DeviceName]PodResourceInfo, []DeviceName) {
	devices := make(map[DeviceName]PodResourceInfo)
	shared := make(map[DeviceName]struct{})

	assign := func(device DeviceName, info PodResourceInfo, logAttrs ...any) {
		if existing, claimed := devices[device]; claimed {
			if existing.Namespace != info.Namespace || existing.Name != info.Name {
				shared[device] = struct{}{}
			}
			if !info.before(existing) {
				return
			}
		}
		devices[device] = info
		slog.Log(context.Background(), logging.LevelTrace, "Mapped device to pod",
			append([]any{
				"deviceId", device, "pod", info.Name,
				"namespace", info.Namespace, "container", info.ContainerName,
			}, logAttrs...)...)
	}

	for _, pod := range podResources.GetPodResources() {
		for _, container := range pod.GetContainers() {
			info := PodResourceInfo{
				Name:          pod.GetName(),
				Namespace:     pod.GetNamespace(),
				ContainerName: container.GetName(),
			}

			for _, containerDevice := range container.GetDevices() {
				if !strings.HasPrefix(containerDevice.GetResourceName(), RBLNResourcePrefix) {
					continue
				}
				for _, deviceID := range containerDevice.GetDeviceIds() {
					assign(DeviceName(deviceID), info, "resource", containerDevice.GetResourceName())
				}
			}

			for _, dynamicResource := range container.GetDynamicResources() {
				for _, claimResource := range dynamicResource.GetClaimResources() {
					// A DRA claim result carries no device name when it
					// allocated something other than a device, which this
					// exporter has no telemetry for.
					if !isRBLNDRADriver(claimResource.GetDriverName()) ||
						claimResource.GetDeviceName() == "" {
						continue
					}
					assign(DeviceName(claimResource.GetDeviceName()), info,
						"driver", claimResource.GetDriverName(),
						"claim", dynamicResource.GetClaimName())
				}
			}
		}
	}

	// Sorted so syncPodResources can compare successive syncs with slices.Equal;
	// unsorted map order would report every cycle as a change.
	return devices, slices.Sorted(maps.Keys(shared))
}

func (p *PodResourceMapper) getPodResources() (*podResourcesAPI.ListPodResourcesResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := p.client.List(ctx, &podResourcesAPI.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod resources; err: %w", err)
	}
	return resp, nil
}

func newKubeletClient() (*grpc.ClientConn, func(), error) {
	if _, err := os.Stat(PodResourceSocket); err != nil {
		return nil, func() {}, fmt.Errorf("kubelet pod-resources socket unavailable, %w", err)
	}
	conn, err := grpc.NewClient("unix://"+PodResourceSocket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to create kubelet client, %w", err)
	}
	return conn, func() {
		_ = conn.Close()
	}, nil
}

func IsKubernetes() bool {
	if s := os.Getenv("KUBERNETES_SERVICE_HOST"); s != "" {
		return true
	}
	if _, err := os.Stat(PodResourceSocket); err == nil {
		return true
	}
	return false
}

func NewNoopPodResourceMapper() *PodResourceMapper {
	return &PodResourceMapper{
		podResourcesByDevice: make(map[DeviceName]PodResourceInfo),
	}
}
