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

	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
	"github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
)

const (
	milliCelsiusToCelsius = 1000.0
	microWattToWatt       = 1_000_000.0

	PStateUnavailable = -1
)

type Client struct {
	conn     *grpc.ClientConn
	client   rblnservicespb.RBLNServicesClient
	endpoint string

	// Edge-tracking state for the device-visibility records; guarded by mu
	// because gateway mode may serve concurrent scrapes on one cached client.
	mu            sync.Mutex
	collectedOnce bool
	prevMissing   map[string]bool
	prevHealth    map[string]deviceHealth
}

// deviceHealth is the health-relevant slice of a device's state. READY/BUSY
// activity churn is deliberately excluded so steady state stays silent in
// the logs; the full state is always available via the device-state metric.
type deviceHealth struct {
	state     rblnservicespb.DeviceStatus
	errStatus int
}

func (h deviceHealth) degraded() bool {
	return h.state == rblnservicespb.DeviceStatus_FAULT || h.errStatus != 0
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
		conn:     conn,
		client:   c,
		endpoint: endpoint,
	}, nil
}

func NewLazyClient(endpoint string) (*Client, error) {
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create rbln-daemon client for %s: %w", endpoint, err)
	}

	return &Client{
		conn:     conn,
		client:   rblnservicespb.NewRBLNServicesClient(conn),
		endpoint: endpoint,
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
	Topology        *TopologyInfo
}

func (c *Client) GetDeviceInfo(ctx context.Context) ([]DeviceInfo, error) {
	devices, err := c.getServiceableDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get serviceable devices: %w", err)
	}

	totalInfos, err := c.getTotalDeviceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get total device info: %w", err)
	}

	topologies, err := c.getTopologyByName(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list topology: %w", err)
	}

	// Key by name, not UUID: getTotalInfo includes SR-IOV VFs, which share
	// the parent PF's UUID, so a UUID key could overwrite the PF's telemetry
	// with a VF's.
	totalMap := make(map[string]*rblnservicespb.DeviceInfo, len(totalInfos))
	for _, info := range totalInfos {
		totalMap[info.GetName()] = info
	}

	merged := make([]DeviceInfo, 0, len(devices))
	missing := make(map[string]bool)
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
		} else {
			missing[dev.GetName()] = true
			slog.Debug("Device missing from total info; telemetry zeroed", "device", dev.GetName())
		}

		di.SMCVersion, err = c.getSMCVersion(ctx, dev.GetName())
		if err != nil {
			return nil, fmt.Errorf("failed to get version for %s: %w", dev.GetName(), err)
		}

		merged = append(merged, di)
		slog.Log(ctx, logging.LevelTrace, "Merged device",
			"device", di.Name, "uuid", di.UUID, "state", di.DevState.String(),
			"pstate", di.PState, "errStatus", di.ErrStatus)
	}

	slog.Debug("Merged device info", "devices", len(devices),
		"totalInfos", len(totalInfos), "topologies", len(topologies), "merged", len(merged))
	c.recordDeviceTransitions(merged, missing)
	return merged, nil
}

// recordDeviceTransitions makes device-level failures diagnosable from
// default-level logs while keeping steady state silent: every record here is
// edge-triggered (fires once on a transition, never per cycle).
func (c *Client) recordDeviceTransitions(merged []DeviceInfo, missing map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.collectedOnce {
		c.collectedOnce = true
		slog.Info("First collection succeeded", "endpoint", c.endpoint, "devices", len(merged))
	}

	serviceable := make(map[string]bool, len(merged))
	for _, di := range merged {
		serviceable[di.Name] = true
	}

	for name := range missing {
		if !c.prevMissing[name] {
			slog.Warn("Device missing from total info", "device", name,
				"effect", "telemetry zeroed until device returns")
		}
	}
	for name := range c.prevMissing {
		if !missing[name] && serviceable[name] {
			slog.Info("Device returned to total info", "device", name)
		}
	}

	health := make(map[string]deviceHealth, len(merged))
	for _, di := range merged {
		if missing[di.Name] {
			continue // the missing warn already covers this device
		}
		now := deviceHealth{state: di.DevState, errStatus: di.ErrStatus}
		health[di.Name] = now
		// The zero value of prev reads as healthy, so a device that is
		// already sick on the first collection still gets its warn.
		prev := c.prevHealth[di.Name]
		if prev.degraded() == now.degraded() {
			continue
		}
		if now.degraded() {
			slog.Warn("Device entered degraded state", "device", di.Name,
				"state", now.state.String(), "errStatus", now.errStatus)
		} else {
			slog.Info("Device recovered", "device", di.Name, "state", now.state.String())
		}
	}

	// prevHealth and prevMissing are disjoint (a missing device is never in
	// health), so a vanished device warns exactly once.
	for name := range c.prevHealth {
		if !serviceable[name] {
			slog.Warn("Device no longer serviceable", "device", name,
				"effect", "metrics for this device are no longer exported")
		}
	}
	for name := range c.prevMissing {
		if !serviceable[name] {
			slog.Warn("Device no longer serviceable", "device", name,
				"effect", "metrics for this device are no longer exported")
		}
	}

	c.prevMissing = missing
	c.prevHealth = health
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
			slog.Debug("Skipping topology entry with error",
				"device", entry.GetDeviceName(), "code", entryErr.GetCode(), "errMessage", entryErr.GetMessage())
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
		slog.Debug("Daemon returned no version", "device", name)
		return "", nil
	}
	return version.GetSmcVersion(), nil
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
