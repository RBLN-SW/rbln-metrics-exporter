package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	legacy "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

const reconnectMaxDelay = 5 * time.Second

type Client struct {
	conn      *grpc.ClientConn
	inventory pb.InventoryServiceClient
	metrics   pb.MetricServiceClient
	topology  pb.TopologyServiceClient
	legacy    legacy.RBLNServicesClient
	endpoint  string
	options   Options
	// Gateway scrapes share the client. Serialize snapshots and transition logs.
	mu                sync.Mutex
	collectedOnce     bool
	prevMissing       map[string]bool
	prevState         map[string]pb.DeviceState
	activeAPI         string
	stateLookupFailed bool
}

func NewLazyClient(endpoint string, options ...Options) (*Client, error) {
	opts := DefaultOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.MetricGroupID == "" {
		opts.MetricGroupID = DefaultMetricGroupID
	}
	if opts.MetricGroupID == "default" {
		return nil, fmt.Errorf("the SMD default metric group includes clocks; use a dedicated clock-free group")
	}
	creds, err := transportCredentials(opts)
	if err != nil {
		return nil, err
	}
	bo := backoff.DefaultConfig
	bo.MaxDelay = reconnectMaxDelay
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds), grpc.WithConnectParams(grpc.ConnectParams{Backoff: bo, MinConnectTimeout: 20 * time.Second}))
	if err != nil {
		return nil, fmt.Errorf("create rbln-smd client: %w", err)
	}
	return &Client{conn: conn, inventory: pb.NewInventoryServiceClient(conn), metrics: pb.NewMetricServiceClient(conn), topology: pb.NewTopologyServiceClient(conn), legacy: legacy.NewRBLNServicesClient(conn), endpoint: endpoint, options: opts}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) GetDeviceInfo(ctx context.Context) ([]DeviceInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	api := "v1"
	devices, missing, err := c.collectV1(ctx)
	if status.Code(err) == codes.Unimplemented {
		api = "v0"
		devices, missing, err = c.collectV0(ctx)
	}
	if err != nil {
		return nil, err
	}
	if api == "v1" {
		c.supplementLegacyStates(ctx, devices)
	}
	if c.activeAPI != api {
		slog.Info("Selected SMD collection API", "endpoint", c.endpoint, "api", api)
		c.activeAPI = api
	}
	c.recordDeviceTransitions(devices, missing)
	return devices, nil
}

func (c *Client) collectV1(ctx context.Context) ([]DeviceInfo, map[string]bool, error) {
	devices, err := c.listDevices(ctx)
	if err != nil {
		return nil, nil, err
	}
	topologies, topologyFailures, err := c.listTopology(ctx)
	if err != nil {
		return nil, nil, err
	}
	readings := map[string][]*pb.MetricSample{}
	missing := map[string]bool{}
	if len(devices) > 0 {
		if err := c.ensureMetricGroup(ctx); err != nil {
			return nil, nil, err
		}
		resp, err := c.metrics.SampleMetrics(ctx, &pb.SampleMetricsRequest{Group: &pb.MetricGroupRef{Id: c.options.MetricGroupID}})
		if err != nil {
			return nil, nil, fmt.Errorf("SampleMetrics: %w", err)
		}
		for _, e := range resp.GetUnreachable() {
			missing[e.GetRef()] = true
		}
		for _, sample := range resp.GetSamples() {
			name := sample.GetDevice().GetName()
			if name != "" && !missing[name] {
				readings[name] = append(readings[name], sample)
			}
		}
	}
	merged := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		if c.foreignDriver(d.GetPciBusId()) {
			continue
		}
		di := DeviceInfo{UUID: d.GetUuid(), Name: d.GetName(), DeviceID: d.GetDevId(), Card: d.GetCardName(), PCIBusID: d.GetPciBusId(), IsVF: d.GetIsVf(), ParentName: d.GetParentName(), Compute: d.NpuId != nil, DevState: d.GetState(), Topology: topologies[d.GetName()]}
		di.CollectionSucceeded = !missing[di.Name] && !topologyFailures[di.Name]
		versions := d.GetVersions()
		if versions == nil {
			// Some SMD responses omit inline static specs even with FULL detail.
			v, err := c.inventory.GetVersion(ctx, &pb.GetVersionRequest{Device: &pb.DeviceRef{Name: di.Name}})
			if err == nil {
				versions = &pb.VersionInfo{FwVersion: v.GetFwVersion(), DrvVersion: v.GetDrvVersion(), SmcVersion: v.GetSmcVersion()}
			} else if !deviceUnavailable(err) {
				return nil, nil, fmt.Errorf("GetVersion(%s): %w", di.Name, err)
			} else {
				di.CollectionSucceeded = false
			}
		}
		di.DriverVersion, di.FirmwareVersion, di.SMCVersion = versions.GetDrvVersion(), versions.GetFwVersion(), versions.GetSmcVersion()
		memory := d.GetMemory()
		if memory == nil && di.Compute {
			m, err := c.inventory.GetMemorySpec(ctx, &pb.GetMemorySpecRequest{Device: &pb.DeviceRef{Name: di.Name}})
			if err == nil {
				memory = m.GetMemory()
			} else if !deviceUnavailable(err) {
				return nil, nil, fmt.Errorf("GetMemorySpec(%s): %w", di.Name, err)
			} else {
				di.CollectionSucceeded = false
			}
		}
		if memory != nil && di.Compute {
			v := memory.GetTotalBytes()
			di.MemoryTotalBytes = &v
		}
		c.partitionMetadata(&di, devices)
		if !di.IsVF && di.Topology != nil {
			di.PCIeLinkSpeed, di.PCIeLinkWidth = di.Topology.LinkSpeed, di.Topology.LinkWidth
		}
		if !missing[di.Name] {
			for _, sample := range readings[di.Name] {
				applySample(&di, sample)
			}
		}
		merged = append(merged, di)
	}
	return merged, missing, nil
}

func deviceUnavailable(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

func (c *Client) listDevices(ctx context.Context) ([]*pb.DeviceInfo, error) {
	var out []*pb.DeviceInfo
	token := ""
	seen := map[string]bool{}
	for {
		r, err := c.inventory.ListDevices(ctx, &pb.ListDevicesRequest{Filter: pb.DeviceListFilter_DEVICE_LIST_FILTER_ALL, Detail: pb.DeviceListDetail_DEVICE_LIST_DETAIL_FULL, PageSize: 1000, PageToken: token})
		if err != nil {
			return nil, fmt.Errorf("InventoryService.ListDevices: %w", err)
		}
		unavailable := map[string]bool{}
		for _, e := range r.GetUnreachable() {
			unavailable[e.GetRef()] = true
		}
		for _, d := range r.GetItems() {
			if d.GetName() != "" && !unavailable[d.GetName()] {
				out = append(out, d)
			}
		}
		token = r.GetNextPageToken()
		if token == "" {
			return out, nil
		}
		if seen[token] {
			return nil, fmt.Errorf("ListDevices repeated page token")
		}
		seen[token] = true
	}
}

func (c *Client) listTopology(ctx context.Context) (map[string]*TopologyInfo, map[string]bool, error) {
	out := map[string]*TopologyInfo{}
	failures := map[string]bool{}
	token := ""
	seen := map[string]bool{}
	for {
		r, err := c.topology.ListTopology(ctx, &pb.ListTopologyRequest{PageSize: 1000, PageToken: token})
		if err != nil {
			return nil, nil, fmt.Errorf("ListTopology: %w", err)
		}
		unavailable := map[string]bool{}
		for _, e := range r.GetUnreachable() {
			unavailable[e.GetRef()] = true
			failures[e.GetRef()] = true
		}
		for _, e := range r.GetEntries() {
			if !unavailable[e.GetDeviceName()] {
				out[e.GetDeviceName()] = &TopologyInfo{NUMANode: e.GetNumaNode(), CPUList: e.GetLocalCpulist(), RSDGroup: e.GetRsdGroup(), LinkSpeed: parseLinkSpeed(e.GetPcieLinkSpeed()), LinkWidth: parseLinkWidth(e.GetPcieLinkWidth())}
			}
		}
		token = r.GetNextPageToken()
		if token == "" {
			return out, failures, nil
		}
		if seen[token] {
			return nil, nil, fmt.Errorf("ListTopology repeated page token")
		}
		seen[token] = true
	}
}

func applySample(d *DeviceInfo, s *pb.MetricSample) {
	if value, ok := s.GetTypedValue().(*pb.MetricSample_FloatValue); ok {
		v := value.FloatValue
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		switch s.GetMetricId() {
		case pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS:
			if !d.IsVF {
				v /= 1000
				d.Temperature = &v
			}
		case pb.MetricId_METRIC_ID_POWER_MICRO_WATT:
			if !d.IsVF && v >= 0 {
				v /= 1e6
				d.Power = &v
			}
		case pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI:
			if d.Compute && v >= 0 && v <= 100000 {
				v /= 1000
				d.Utilization = &v
			}
		}
	}
	if value, ok := s.GetTypedValue().(*pb.MetricSample_IntValue); ok {
		v := value.IntValue
		if v < 0 {
			return
		}
		switch s.GetMetricId() {
		case pb.MetricId_METRIC_ID_MEMORY_USED_BYTES:
			if d.Compute {
				n := uint64(v)
				d.MemoryUsedBytes = &n
			}
		case pb.MetricId_METRIC_ID_PSTATE:
			d.PState = &v
		}
	}
}

func (c *Client) recordDeviceTransitions(devices []DeviceInfo, missing map[string]bool) {
	if !c.collectedOnce {
		c.collectedOnce = true
		slog.Info("First collection succeeded", "endpoint", c.endpoint, "devices", len(devices))
	}
	states := map[string]pb.DeviceState{}
	for _, d := range devices {
		states[d.Name] = d.DevState
		if missing[d.Name] && !c.prevMissing[d.Name] {
			slog.Warn("Device telemetry unavailable", "device", d.Name, "effect", "dynamic metrics omitted until collection recovers")
		}
		if !missing[d.Name] && c.prevMissing[d.Name] {
			slog.Info("Device telemetry recovered", "device", d.Name)
		}
		wasFault := c.prevState[d.Name] == pb.DeviceState_DEVICE_STATE_FAULT
		fault := d.DevState == pb.DeviceState_DEVICE_STATE_FAULT
		if fault && !wasFault {
			slog.Warn("Device entered degraded state", "device", d.Name, "state", d.DevState.String())
		}
		if wasFault && !fault && d.HasKnownState() {
			slog.Info("Device recovered", "device", d.Name, "state", d.DevState.String())
		}
	}
	for name := range c.prevState {
		if _, ok := states[name]; !ok {
			slog.Warn("Device no longer present", "device", name, "effect", "metrics for this device are no longer exported")
		}
	}
	c.prevState, c.prevMissing = states, missing
}

func parseLinkSpeed(s string) *float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func parseLinkWidth(s string) *int64 {
	v, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "x"), 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}
