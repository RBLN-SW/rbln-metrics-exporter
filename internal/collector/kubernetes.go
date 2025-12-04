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
)

const (
	PodResourceSocket  = "/var/lib/kubelet/pod-resources/kubelet.sock"
	RBLNResourcePrefix = "rebellions.ai"
	SysfsDriverPools   = "/sys/bus/pci/drivers/rebellions/%s/pools"
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
}

func NewPodResourceMapper(ctx context.Context) *PodResourceMapper {
	m := &PodResourceMapper{
		podResourcesByDevice: make(map[DeviceName]PodResourceInfo),
		syncRequests:         make(chan struct{}, 1),
	}

	if err := m.syncPodResources(); err != nil {
		slog.Warn("initial pod resource sync failed", "err", err)
	}

	go m.runSyncLoop(ctx)
	return m
}

func (p *PodResourceMapper) TriggerSync() {
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
				slog.Warn("Failed to sync pod resources", "err", err)
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
						deviceName, err := getDeviceName(deviceID)
						if err != nil {
							return err
						}
						podResourcesInfo[DeviceName(deviceName)] = PodResourceInfo{
							Name:          pod.Name,
							Namespace:     pod.Namespace,
							ContainerName: container.Name,
						}
					}
				}
			}
		}
	}

	p.Lock()
	defer p.Unlock()
	p.podResourcesByDevice = podResourcesInfo

	return nil
}

func (p *PodResourceMapper) getPodResources() (*podResourcesAPI.ListPodResourcesResponse, error) {
	client, cleanup, err := newKubeletClient()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	podResourcesClient := podResourcesAPI.NewPodResourcesListerClient(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := podResourcesClient.List(ctx, &podResourcesAPI.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod resources; err: %w", err)
	}
	return resp, nil
}

func newKubeletClient() (*grpc.ClientConn, func(), error) {
	if _, err := os.Stat(PodResourceSocket); err != nil {
		slog.Error("kubelet pod-resources socket unavailable", "socket", PodResourceSocket, "err", err)
		return nil, func() {}, fmt.Errorf("kubelet pod-resources socket unavailable, %w", err)
	}
	conn, err := grpc.NewClient("unix://"+PodResourceSocket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		slog.Error("failed to create kubelet client", "err", err)
		return nil, func() {}, fmt.Errorf("failed to create kubelet client, %w", err)
	}
	return conn, func() {
		_ = conn.Close()
	}, nil
}

func getDeviceName(pciAddress string) (string, error) {
	poolsFilePath := fmt.Sprintf(SysfsDriverPools, pciAddress)
	poolsFile, err := os.ReadFile(poolsFilePath)
	if err != nil {
		slog.Error("Failed to read", "file", poolsFilePath, "err", err)
		return "", fmt.Errorf("failed to read %s, %w", poolsFilePath, err)
	}

	deviceName := strings.Split(strings.Split(string(poolsFile), "\n")[1], " ")[0]
	return deviceName, nil
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
