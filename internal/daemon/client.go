package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

type DeviceInfo struct {
	UUID            string
	Name            string
	DeviceID        string
	Card            string
	Temperature     float64
	Power           float64
	DRAMUsedGiB     float64
	DRAMTotalGiB    float64
	Utilization     float64
	DriverVersion   string
	FirmwareVersion string
	DeviceStatus    int
}

func (c *Client) GetDeviceInfo(ctx context.Context) ([]DeviceInfo, error) {
	devices, err := c.getServiceableDevices(ctx)
	if err != nil {
		slog.Warn("failed to get serviceable devices", "err", err)
		return nil, fmt.Errorf("failed to get serviceable devices: %v", err)
	}

	totalInfos, err := c.getTotalDeviceInfo(ctx)
	if err != nil {
		slog.Warn("failed to get total device info", "err", err)
		return nil, fmt.Errorf("failed to get total device info: %v", err)
	}

	deviceMap := make(map[string]*rblnservicespb.Device, len(devices))
	for _, dev := range devices {
		deviceMap[dev.GetUuid()] = dev
	}

	totalMap := make(map[string]*rblnservicespb.DeviceInfo, len(totalInfos))
	for _, info := range totalInfos {
		totalMap[info.GetUuid()] = info
	}

	merged := make([]DeviceInfo, 0, len(deviceMap))
	for uuid, dev := range deviceMap {
		di := DeviceInfo{
			UUID:     dev.GetUuid(),
			Name:     dev.GetName(),
			DeviceID: dev.GetDevId(),
			Card:     cardNameFromDevID(dev.GetDevId()),
		}
		if info, ok := totalMap[uuid]; ok {
			di.Temperature = float64(info.GetTemperature())
			di.Power = float64(info.GetWatt())
			di.DRAMTotalGiB = float64(info.GetTotalMem())
			di.DRAMUsedGiB = float64(info.GetUsedMem())
			di.Utilization = float64(info.GetUtilization())
			di.DriverVersion = info.GetDrvVersion()
			di.FirmwareVersion = info.GetFwVersion()
			di.DeviceStatus = int(info.GetErrStatus())
		}
		merged = append(merged, di)
	}

	return merged, nil
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

func (c *Client) getTotalDeviceInfo(ctx context.Context) ([]*rblnservicespb.DeviceInfo, error) {
	stream, err := c.client.GetTotalInfo(ctx, &rblnservicespb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("failed to GetTotalInfo RPC: %w", err)
	}

	var deviceinfos []*rblnservicespb.DeviceInfo
	for {
		d, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to receive device: %w", err)
		}
		deviceinfos = append(deviceinfos, d)
	}
	return deviceinfos, nil
}
