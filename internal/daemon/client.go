package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
)

var cardNameMap = map[string]string{
	"1020": "RBLN-CA02",
	"1021": "RBLN-CA02",
	"1120": "RBLN-CA12",
	"1121": "RBLN-CA12",
	"1150": "RBLN-CA15",
	"1220": "RBLN-CA22",
	"1221": "RBLN-CA22",
	"1250": "RBLN-CA25",
}

func cardNameFromDevID(devID string) string {
	if n, ok := cardNameMap[devID]; ok {
		return n
	}
	return devID
}

type Client struct {
	conn   *grpc.ClientConn
	client rblnservicespb.RBLNServicesClient
}

func NewClient(ctx context.Context, endpoint string) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	//nolint:staticcheck // keep DialContext until gRPC client migration is finished
	conn, err := grpc.DialContext(
		dialCtx,
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck // keep WithBlock until gRPC client migration is finished
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rbln-daemon %s: %w", endpoint, err)
	}

	c := rblnservicespb.NewRBLNServicesClient(conn)
	return &Client{
		conn:   conn,
		client: c,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type DeviceStatus struct {
	UUID         string
	Card         string
	DeviceNode   string
	Temperature  float64
	Power        float64
	DRAMUsedGiB  float64
	DRAMTotalGiB float64
	Utilization  float64
}

func (c *Client) GetDeviceStatus(ctx context.Context) ([]DeviceStatus, error) {
	devices, err := c.getServiceableDevices(ctx)
	if err != nil {
		slog.Warn("failed to get serviceable devices", "err", err)
		return nil, fmt.Errorf("failed to get serviceable devices: %v", err)
	}

	var statuses []DeviceStatus

	for _, d := range devices {
		if status, ok := c.buildDeviceStatus(ctx, d); ok {
			statuses = append(statuses, status)
		}
	}

	return statuses, nil
}

func (c *Client) getServiceableDevices(ctx context.Context) ([]*rblnservicespb.Device, error) {
	stream, err := c.client.GetServiceableDeviceList(ctx, &rblnservicespb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("failed to GetServiceableDeviceList RPC: %w", err)
	}

	var devices []*rblnservicespb.Device
	for {
		d, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to receive device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (c *Client) buildDeviceStatus(ctx context.Context, device *rblnservicespb.Device) (DeviceStatus, bool) {
	metrics := c.fetchDeviceMetrics(ctx, device)

	if metrics.hw == nil && metrics.mem == nil && metrics.util == nil {
		return DeviceStatus{}, false
	}

	status := DeviceStatus{
		UUID:       device.GetUuid(),
		Card:       cardNameFromDevID(device.GetDevId()),
		DeviceNode: device.GetName(),
	}

	if metrics.hw != nil {
		status.Temperature = float64(metrics.hw.GetTemperature())
		status.Power = float64(metrics.hw.GetWatt())
	}
	if metrics.mem != nil {
		status.DRAMTotalGiB = float64(metrics.mem.GetTotalMem())
		status.DRAMUsedGiB = float64(metrics.mem.GetUsedMem())
	}
	if metrics.util != nil {
		status.Utilization = float64(metrics.util.GetUtilization())
	}

	return status, true
}

func (c *Client) fetchDeviceMetrics(ctx context.Context, device *rblnservicespb.Device) deviceMetrics {
	var (
		hw   *rblnservicespb.HWInfo
		mem  *rblnservicespb.MemoryInfo
		util *rblnservicespb.UtilInfo
	)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		result, err := c.client.GetHWInfo(ctx, device)
		if err != nil {
			slog.Error("failed to get hw info", "device", device.GetName(), "error", err)
			return
		}
		hw = result
	}()

	go func() {
		defer wg.Done()
		result, err := c.client.GetMemoryInfo(ctx, device)
		if err != nil {
			slog.Error("failed to get memory info", "device", device.GetName(), "error", err)
			return
		}
		mem = result
	}()

	go func() {
		defer wg.Done()
		result, err := c.client.GetUtilization(ctx, device)
		if err != nil {
			slog.Error("failed to get utilization", "device", device.GetName(), "error", err)
			return
		}
		util = result
	}()

	wg.Wait()

	return deviceMetrics{
		hw:   hw,
		mem:  mem,
		util: util,
	}
}

type deviceMetrics struct {
	hw   *rblnservicespb.HWInfo
	mem  *rblnservicespb.MemoryInfo
	util *rblnservicespb.UtilInfo
}
