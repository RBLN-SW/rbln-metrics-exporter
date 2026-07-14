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

const (
	milliCelsiusToCelsius = 1000.0
	microWattToWatt       = 1_000_000.0

	PStateUnavailable = -1
)

const (
	ClockCP0  = "cp0"
	ClockCP1  = "cp1"
	ClockDC0  = "dc0"
	ClockDC1  = "dc1"
	ClockBus  = "bus"
	ClockSHM  = "shm"
	ClockDRAM = "dram"
)

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

type TopologyInfo struct {
	NUMANode      int32
	CPUList       string
	RSDGroup      string
	PCIeLinkSpeed string
	PCIeLinkWidth string
}

type DeviceInfo struct {
	UUID            string
	Name            string
	DeviceID        string
	Card            string
	PCIBusID        string
	IsVF            bool
	ParentName      string
	NumVFs          uint32
	Temperature     float64
	Power           float64
	DRAMUsedGiB     float64
	DRAMTotalGiB    float64
	Utilization     float64
	DriverVersion   string
	FirmwareVersion string
	SMCVersion      string
	ErrStatus       int
	DevState        rblnservicespb.DeviceStatus
	PState          int32
	Clocks          map[string]float64
	Topology        *TopologyInfo
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

	topologies, err := c.getTopologyByName(ctx)
	if err != nil {
		slog.Warn("failed to list topology", "err", err)
		return nil, fmt.Errorf("failed to list topology: %v", err)
	}

	// Key by name, not UUID: getTotalInfo includes SR-IOV VFs, which share
	// the parent PF's UUID, so a UUID key could overwrite the PF's telemetry
	// with a VF's.
	totalMap := make(map[string]*rblnservicespb.DeviceInfo, len(totalInfos))
	for _, info := range totalInfos {
		totalMap[info.GetName()] = info
	}

	merged := make([]DeviceInfo, 0, len(devices))
	for _, dev := range devices {
		di := DeviceInfo{
			UUID:       dev.GetUuid(),
			Name:       dev.GetName(),
			DeviceID:   dev.GetDevId(),
			Card:       dev.GetCardName(),
			PCIBusID:   dev.GetPciBusId(),
			IsVF:       dev.GetIsVf(),
			ParentName: dev.GetParentName(),
			NumVFs:     dev.GetNumVfs(),
			DevState:   rblnservicespb.DeviceStatus_NOT_FOUND,
			PState:     PStateUnavailable,
			Topology:   topologies[dev.GetName()],
		}
		if info, ok := totalMap[dev.GetName()]; ok {
			// SMI now reports temperature in milli-Celsius and power in micro-Watts.
			di.Temperature = float64(info.GetTemperature()) / milliCelsiusToCelsius
			di.Power = float64(info.GetWatt()) / microWattToWatt
			di.DRAMTotalGiB = float64(info.GetTotalMem())
			di.DRAMUsedGiB = float64(info.GetUsedMem())
			di.Utilization = float64(info.GetUtilization())
			di.DriverVersion = info.GetDrvVersion()
			di.FirmwareVersion = info.GetFwVersion()
			di.ErrStatus = int(info.GetErrStatus())
			di.DevState = info.GetDevStatus()
			di.PState = info.GetPState()
		}

		di.SMCVersion, err = c.getSMCVersion(ctx, dev.GetName())
		if err != nil {
			slog.Warn("failed to get version", "device", dev.GetName(), "err", err)
			return nil, fmt.Errorf("failed to get version for %s: %v", dev.GetName(), err)
		}
		di.Clocks, err = c.getClocks(ctx, dev.GetName())
		if err != nil {
			slog.Warn("failed to get clock info", "device", dev.GetName(), "err", err)
			return nil, fmt.Errorf("failed to get clock info for %s: %v", dev.GetName(), err)
		}

		merged = append(merged, di)
	}

	return merged, nil
}

func (c *Client) getTopologyByName(ctx context.Context) (map[string]*TopologyInfo, error) {
	resp, err := c.client.RblnListTopology(ctx, &rblnservicespb.DeviceFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to RblnListTopology RPC: %w", err)
	}

	topologies := make(map[string]*TopologyInfo, len(resp.GetEntries()))
	for _, entry := range resp.GetEntries() {
		// A failed entry's zero values are indistinguishable from real
		// readings (e.g. numa_node 0), so skip it entirely.
		if entryErr := entry.GetError(); entryErr.GetCode() != 0 || entryErr.GetMessage() != "" {
			slog.Debug("skipping topology entry with error",
				"device", entry.GetDeviceName(), "code", entryErr.GetCode(), "message", entryErr.GetMessage())
			continue
		}
		topologies[entry.GetDeviceName()] = &TopologyInfo{
			NUMANode:      entry.GetNumaNode(),
			CPUList:       entry.GetLocalCpulist(),
			RSDGroup:      entry.GetRsdGroup(),
			PCIeLinkSpeed: entry.GetPcieLinkSpeed(),
			PCIeLinkWidth: entry.GetPcieLinkWidth(),
		}
	}
	return topologies, nil
}

func (c *Client) getSMCVersion(ctx context.Context, name string) (string, error) {
	version, err := c.client.GetVersion(ctx, &rblnservicespb.Device{Name: name})
	if err != nil {
		return "", fmt.Errorf("failed to GetVersion RPC: %w", err)
	}
	if version.GetErrStatus() != rblnservicespb.Status_SUCCEED {
		slog.Debug("daemon returned no version", "device", name)
		return "", nil
	}
	return version.GetSmcVersion(), nil
}

func (c *Client) getClocks(ctx context.Context, name string) (map[string]float64, error) {
	clock, err := c.client.GetClockInfo(ctx, &rblnservicespb.Device{Name: name})
	if err != nil {
		return nil, fmt.Errorf("failed to GetClockInfo RPC: %w", err)
	}
	if clock.GetErrStatus() != rblnservicespb.Status_SUCCEED {
		slog.Debug("daemon returned no clock info", "device", name)
		return nil, nil
	}
	return clockMap(clock), nil
}

func clockMap(clock *rblnservicespb.ClockInfo) map[string]float64 {
	clocks := make(map[string]float64, 7)
	put := func(label string, mhz int32) {
		// The daemon encodes "clock not implemented on this platform" as 0
		// (ATOM has no cp1/dram, REBEL has no shm); skip those instead of
		// exposing a fake 0 MHz reading.
		if mhz > 0 {
			clocks[label] = float64(mhz)
		}
	}
	put(ClockCP0, clock.GetCp_0Clock())
	put(ClockCP1, clock.GetCp_1Clock())
	put(ClockDC0, clock.GetDc_0Clock())
	put(ClockDC1, clock.GetDc_1Clock())
	put(ClockBus, clock.GetBusClock())
	put(ClockSHM, clock.GetShmClock())
	put(ClockDRAM, clock.GetDramClock())
	return clocks
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
