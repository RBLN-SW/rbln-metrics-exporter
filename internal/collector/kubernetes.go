package collector

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podResourcesAPI "k8s.io/kubelet/pkg/apis/podresources/v1alpha1"

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

const (
	PodResourceSocket  = "/var/lib/kubelet/pod-resources/kubelet.sock"
	RBLNResourcePrefix = "rebellions.ai"
)

type DeviceName string

type PodResourceInfo struct {
	Name          string
	Namespace     string
	ContainerName string
}

type PodResourceMapper struct {
	sync.RWMutex
	podResourcesByDevice map[DeviceName]PodResourceInfo
	syncRequests         chan struct{}

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
	p.RLock()
	defer p.RUnlock()

	snapshot := make(map[DeviceName]PodResourceInfo, len(p.podResourcesByDevice))
	maps.Copy(snapshot, p.podResourcesByDevice)
	return snapshot
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
	podResourcesInfo := make(map[DeviceName]PodResourceInfo)

	podResources, err := p.getPodResources()
	if err != nil {
		return err
	}

	for _, pod := range podResources.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, containerDevice := range container.GetDevices() {
				if strings.HasPrefix(containerDevice.GetResourceName(), RBLNResourcePrefix) {
					for _, deviceID := range containerDevice.GetDeviceIds() {
						podResourcesInfo[DeviceName(deviceID)] = PodResourceInfo{
							Name:          pod.Name,
							Namespace:     pod.Namespace,
							ContainerName: container.Name,
						}
						slog.Log(context.Background(), logging.LevelTrace, "Mapped device to pod",
							"deviceId", deviceID, "pod", pod.Name, "namespace", pod.Namespace,
							"container", container.Name, "resource", containerDevice.GetResourceName())
					}
				}
			}
		}
	}

	slog.Debug("Synced pod resources",
		"devices", len(podResourcesInfo), "pods", len(podResources.GetPodResources()))

	p.Lock()
	defer p.Unlock()
	p.podResourcesByDevice = podResourcesInfo

	return nil
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
