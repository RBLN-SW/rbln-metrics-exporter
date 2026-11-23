package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"

	host "github.com/rbln-sw/rblnlib-go/pkg/host/v1"
	smd "github.com/rbln-sw/rblnlib-go/pkg/smd/v1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

type Client struct {
	backend       *smd.Client
	local         *host.Client
	endpoint      string
	options       Options
	mu            sync.Mutex
	collectedOnce bool
	prevMissing   map[string]bool
	prevState     map[string]system.State
	activeAPI     string
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
		return nil, fmt.Errorf("use a dedicated clock-free metric group")
	}
	var tlsConfig *tls.Config
	var err error
	if opts.TLSCA != "" || opts.TLSCert != "" || opts.TLSKey != "" {
		tlsConfig, err = smd.LoadTLSConfig(smd.TLSFiles{CAFile: opts.TLSCA, CertFile: opts.TLSCert, KeyFile: opts.TLSKey})
		if err != nil {
			return nil, err
		}
	}
	backend, err := smd.New(smd.Options{Endpoint: endpoint, TLS: tlsConfig})
	if err != nil {
		return nil, err
	}
	c := &Client{backend: backend, endpoint: endpoint, options: opts}
	if opts.PCIDevicesPath != "" {
		root, e := filepath.Abs(opts.PCIDevicesPath)
		if e == nil {
			c.local, e = host.New(host.Options{PCIDevicesRoot: root})
		}
		if e != nil {
			_ = backend.Close()
			return nil, e
		}
	}
	return c, nil
}
func (c *Client) Close() error { return c.backend.Close() }

func (c *Client) GetDeviceInfo(ctx context.Context) ([]DeviceInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, err := c.backend.CollectTelemetry(ctx, smd.TelemetryOptions{GroupID: c.options.MetricGroupID, CreateGroup: c.options.CreateMetricGroup})
	if err != nil {
		return nil, err
	}
	failures := map[string]bool{}
	for _, issue := range snapshot.Issues {
		if issue.Target == "" {
			return nil, issue.Err
		}
		failures[issue.Target] = true
	}
	byAddress := map[string]system.Device{}
	for _, reading := range snapshot.Devices {
		byAddress[reading.Device.PCIAddress] = reading.Device
	}
	out := make([]DeviceInfo, 0, len(snapshot.Devices))
	missing := map[string]bool{}
	for _, reading := range snapshot.Devices {
		raw := reading.Device
		if c.local != nil {
			driver, e := c.local.CurrentDriver(ctx, raw.PCIAddress)
			if e == nil && driver != "" && driver != system.NativeDriver {
				continue
			}
		}
		deviceID := ""
		if raw.ReportedPCI != nil {
			deviceID = raw.ReportedPCI.DeviceID
		} else if raw.DeviceIDKnown {
			deviceID = fmt.Sprintf("%04x", raw.DeviceID)
		}
		d := DeviceInfo{UUID: raw.UUID, SID: raw.Serial, Name: raw.Name, DeviceID: deviceID, Card: raw.ProductName, PCIBusID: raw.PCIAddress, IsVF: raw.FunctionKind == system.VirtualFunction, ParentName: raw.ParentName, NumVFs: raw.EnabledVFs, Compute: raw.Compute != nil && *raw.Compute, DriverVersion: raw.Versions.Driver, FirmwareVersion: raw.Versions.Firmware, SMCVersion: raw.Versions.SMC, DevState: raw.State, CollectionSucceeded: !failures[raw.Name]}
		c.partitionMetadata(ctx, &d, byAddress)
		for _, issue := range append(append([]system.Issue(nil), raw.Issues...), reading.Issues...) {
			// Unsupported/malformed individual values are absent readings. Collection
			// status tracks actual query/availability failures, independently of health.
			if errors.Is(issue.Err, system.ErrUnsupported) || errors.Is(issue.Err, system.ErrInvalidData) || errors.Is(issue.Err, system.ErrNotReported) {
				continue
			}
			d.CollectionSucceeded = false
			if issue.Field == "telemetry" {
				missing[d.Name] = true
			}
		}
		if failures[d.Name] {
			missing[d.Name] = true
		}
		if d.Compute {
			d.MemoryTotalBytes = raw.MemoryTotalBytes
		}
		if !d.IsVF {
			d.Temperature = reading.Values[system.TemperatureCelsius].Float
			d.Power = reading.Values[system.PowerWatts].Float
		}
		if d.Compute {
			d.Utilization = reading.Values[system.UtilizationPercent].Float
			d.MemoryUsedBytes = reading.Values[system.MemoryUsedBytes].Uint
		}
		if value := reading.Values[system.PerformanceState].Uint; value != nil && *value <= math.MaxInt64 {
			n := int64(*value)
			d.PState = &n
		}
		t := raw.Topology
		if t.NUMANode != nil || t.CPUList != "" || t.RSDGroup != "" || t.LinkWidth != nil || t.LinkSpeedGTPerSecond != nil {
			d.Topology = &TopologyInfo{NUMANode: -1, CPUList: t.CPUList, RSDGroup: t.RSDGroup, LinkSpeed: t.LinkSpeedGTPerSecond}
			if t.NUMANode != nil {
				d.Topology.NUMANode = int32(*t.NUMANode)
			}
			if t.LinkWidth != nil {
				n := int64(*t.LinkWidth)
				d.Topology.LinkWidth = &n
			}
			if !d.IsVF {
				d.PCIeLinkSpeed = d.Topology.LinkSpeed
				d.PCIeLinkWidth = d.Topology.LinkWidth
			}
		}
		out = append(out, d)
	}
	api := strings.TrimPrefix(snapshot.Source, "smd/")
	if c.activeAPI != api {
		slog.Info("Selected SMD collection API", "endpoint", c.endpoint, "api", api)
		c.activeAPI = api
	}
	c.recordDeviceTransitions(out, missing)
	return out, nil
}

func (c *Client) recordDeviceTransitions(devices []DeviceInfo, missing map[string]bool) {
	if !c.collectedOnce {
		c.collectedOnce = true
		slog.Info("First collection succeeded", "endpoint", c.endpoint, "devices", len(devices))
	}
	states := map[string]system.State{}
	for _, d := range devices {
		states[d.Name] = d.DevState
		if missing[d.Name] && !c.prevMissing[d.Name] {
			slog.Warn("Device telemetry unavailable", "device", d.Name, "effect", "dynamic metrics omitted until collection recovers")
		}
		if !missing[d.Name] && c.prevMissing[d.Name] {
			slog.Info("Device telemetry recovered", "device", d.Name)
		}
		wasFault := c.prevState[d.Name] == system.StateFault
		fault := d.DevState == system.StateFault
		if fault && !wasFault {
			slog.Warn("Device entered degraded state", "device", d.Name, "state", string(d.DevState))
		}
		if wasFault && !fault && d.HasKnownState() {
			slog.Info("Device recovered", "device", d.Name, "state", string(d.DevState))
		}
	}
	for name := range c.prevState {
		if _, ok := states[name]; !ok {
			slog.Warn("Device no longer present", "device", name, "effect", "metrics for this device are no longer exported")
		}
	}
	c.prevState, c.prevMissing = states, missing
}
