package smd

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// This set intentionally excludes clock reads, which can wake an idle device.
// EnsureTelemetryGroup is an explicit command; SampleTelemetry never creates
// or changes daemon configuration, even on the first read.
var telemetryIDs = []pb.MetricId{
	pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS, pb.MetricId_METRIC_ID_POWER_MICRO_WATT,
	pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI, pb.MetricId_METRIC_ID_MEMORY_USED_BYTES,
	pb.MetricId_METRIC_ID_PSTATE,
}

func metric(id pb.MetricId) system.Metric {
	switch id {
	case pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS:
		return system.TemperatureCelsius
	case pb.MetricId_METRIC_ID_POWER_MICRO_WATT:
		return system.PowerWatts
	case pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI:
		return system.UtilizationPercent
	case pb.MetricId_METRIC_ID_MEMORY_USED_BYTES:
		return system.MemoryUsedBytes
	case pb.MetricId_METRIC_ID_PSTATE:
		return system.PerformanceState
	default:
		return ""
	}
}

func (c *Client) SupportedMetrics(ctx context.Context) ([]system.Metric, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	out := []system.Metric{}
	token, seen := "", map[string]bool{}
	for {
		r, err := c.metrics.ListSupportedMetrics(ctx, &pb.ListSupportedMetricsRequest{PageSize: 1000, PageToken: token})
		if err != nil {
			return nil, rpcError("SupportedMetrics", "", err)
		}
		if len(r.GetUnreachable()) != 0 {
			return nil, system.IssuesError(unreachable("metric catalog", r.GetUnreachable()))
		}
		for _, d := range r.GetMetrics() {
			if m := metric(d.GetMetricId()); m != "" && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
		token = r.GetNextPageToken()
		if token == "" {
			return out, nil
		}
		if err := nextPage(seen, token); err != nil {
			return nil, err
		}
	}
}

func (c *Client) findTelemetryGroup(ctx context.Context, id string) (*pb.MetricGroup, error) {
	token, seen := "", map[string]bool{}
	var found *pb.MetricGroup
	for {
		r, err := c.metrics.ListMetricGroups(ctx, &pb.ListMetricGroupsRequest{PageSize: 1000, PageToken: token})
		if err != nil {
			return nil, rpcError("ListMetricGroups", id, err)
		}
		for _, issue := range unreachable("metric group", r.GetUnreachable()) {
			if issue.Target == id {
				return nil, issue.Err
			}
		}
		for _, g := range r.GetItems() {
			if g.GetId() == id {
				if found != nil {
					return nil, system.FieldError(id, "metric group", "duplicate ID")
				}
				found = g
			}
		}
		token = r.GetNextPageToken()
		if token == "" {
			return found, nil
		}
		if err := nextPage(seen, token); err != nil {
			return nil, err
		}
	}
}

func validateTelemetryGroup(g *pb.MetricGroup, id string) error {
	if id == "" || id == "default" {
		return &system.Error{Kind: system.InvalidArgument, Target: id, Message: "a dedicated telemetry group is required"}
	}
	if g == nil {
		return &system.Error{Kind: system.NotFound, Target: id, Message: "telemetry group is not provisioned"}
	}
	got, want := slices.Clone(g.GetMetricIds()), slices.Clone(telemetryIDs)
	slices.Sort(got)
	slices.Sort(want)
	if g.GetId() != id || !slices.Equal(got, want) || g.GetDeviceScope().GetAll() == nil || g.GetSamplingPolicy().GetOnDemand() == nil {
		return &system.Error{Kind: system.Conflict, Target: id, Message: "telemetry group must contain exactly temperature, power, utilization, memory used and p-state, all devices, on-demand"}
	}
	return nil
}

// EnsureTelemetryGroup creates the documented, clock-free group if absent.
// groupID is a caller-chosen UUIDv4, as required by SMD. Existing groups are
// validated, never changed. Permission to create is deployment policy.
func (c *Client) EnsureTelemetryGroup(ctx context.Context, groupID string) error {
	_, err := c.ensureTelemetryGroup(ctx, groupID)
	return err
}
func (c *Client) ensureTelemetryGroup(ctx context.Context, groupID string) (*pb.MetricGroup, error) {
	if groupID == "" || groupID == "default" {
		return nil, &system.Error{Kind: system.InvalidArgument, Target: groupID}
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	g, err := c.findTelemetryGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g != nil {
		return g, validateTelemetryGroup(g, groupID)
	}
	r, err := c.metrics.CreateMetricGroup(ctx, &pb.CreateMetricGroupRequest{Name: "rblnlib-go-telemetry-v1", IdempotencyKey: groupID, MetricIds: slices.Clone(telemetryIDs), DeviceScope: &pb.DeviceScope{Selector: &pb.DeviceScope_All{All: &emptypb.Empty{}}}, SamplingPolicy: &pb.SamplingPolicy{Mode: &pb.SamplingPolicy_OnDemand{OnDemand: &emptypb.Empty{}}}})
	if status.Code(err) == codes.AlreadyExists {
		g, err = c.findTelemetryGroup(ctx, groupID)
	} else if err == nil {
		g = r.GetGroup()
	} else {
		_, err = commandError("CreateMetricGroup", err)
	}
	if err != nil {
		return nil, err
	}
	return g, validateTelemetryGroup(g, groupID)
}

// TelemetryOptions makes the caller's group-provisioning policy explicit.
// CreateGroup permits creating an absent group, never changing an existing one.
type TelemetryOptions struct {
	GroupID     string
	CreateGroup bool
}

// CollectTelemetry optionally provisions the dedicated group after selecting
// the supported query API, then collects one inventory/sample view. The plain
// SampleTelemetry method remains read-only.
func (c *Client) CollectTelemetry(ctx context.Context, o TelemetryOptions) (system.TelemetrySnapshot, error) {
	return c.collectTelemetry(ctx, o.GroupID, o.CreateGroup)
}

// SampleTelemetry returns normalized readings: Celsius, watts, percent, bytes,
// and p-state ordinal. It never samples SMD's default clock-bearing group.
// A legacy server uses its established getTotalInfo query (no metric groups).
func (c *Client) SampleTelemetry(ctx context.Context, groupID string) (system.TelemetrySnapshot, error) {
	return c.collectTelemetry(ctx, groupID, false)
}

func (c *Client) collectTelemetry(ctx context.Context, groupID string, createGroup bool) (system.TelemetrySnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	out := system.TelemetrySnapshot{Source: "smd/v1", ObservedAt: time.Now()}
	inv, err := c.listV1(ctx)
	var unsupported *unsupportedInventory
	if errors.As(err, &unsupported) && !c.opts.DisableLegacy {
		return c.allLegacyTelemetry(ctx)
	}
	if err != nil {
		return out, rpcError("ListDevices", c.opts.Endpoint, err)
	}
	if !c.opts.DisableLegacy {
		c.supplementStates(ctx, &inv)
	}
	if len(inv.Devices) == 0 {
		out.Issues = inv.Issues
		return out, nil
	}
	var g *pb.MetricGroup
	if createGroup {
		g, err = c.ensureTelemetryGroup(ctx, groupID)
	} else {
		g, err = c.findTelemetryGroup(ctx, groupID)
	}
	if err != nil {
		if errors.Is(err, system.ErrUnsupported) && !c.opts.DisableLegacy {
			return c.allLegacyTelemetry(ctx)
		}
		return out, err
	}
	if err := validateTelemetryGroup(g, groupID); err != nil {
		return out, err
	}
	r, err := c.metrics.SampleMetrics(ctx, &pb.SampleMetricsRequest{Group: &pb.MetricGroupRef{Id: groupID}})
	if err != nil {
		if status.Code(err) == codes.Unimplemented && !c.opts.DisableLegacy {
			return c.allLegacyTelemetry(ctx)
		}
		return out, rpcError("SampleMetrics", groupID, err)
	}
	topos, topologyErr := c.ListTopology(ctx)
	if topologyErr != nil {
		out.Issues = append(out.Issues, system.Issue{Field: "topology", Err: topologyErr})
	}
	out.Issues = append(out.Issues, inv.Issues...)
	out.Issues = append(out.Issues, topos.Issues...)
	out.Issues = append(out.Issues, unreachable("telemetry", r.GetUnreachable())...)
	blocked := map[string]bool{}
	for _, e := range r.GetUnreachable() {
		blocked[e.GetRef()] = true
	}
	byName := map[string]int{}
	for _, d := range inv.Devices {
		if t, ok := topos.Devices[d.Name]; ok {
			d.Topology = t
		}
		byName[d.Name] = len(out.Devices)
		t := system.Telemetry{Device: d, Values: map[system.Metric]system.MetricValue{}}
		if blocked[d.Name] {
			t.Issues = append(t.Issues, system.Issue{Target: d.Name, Field: "telemetry", Err: system.ErrUnavailable})
		}
		out.Devices = append(out.Devices, t)
	}
	seenSamples := map[string]map[system.Metric]bool{}
	for _, sample := range r.GetSamples() {
		name := sample.GetDevice().GetName()
		i, ok := byName[name]
		if !ok {
			out.Issues = append(out.Issues, system.Issue{Target: name, Field: "telemetry", Err: system.FieldError(name, "sample", "unknown inventory identity")})
			continue
		}
		if blocked[name] {
			continue
		}
		m, value, err := normalizedSample(sample)
		if m == "" {
			continue
		}
		d := out.Devices[i].Device
		if (d.FunctionKind == system.VirtualFunction && (m == system.TemperatureCelsius || m == system.PowerWatts)) ||
			((d.Compute == nil || !*d.Compute) && (m == system.MemoryUsedBytes || m == system.UtilizationPercent)) {
			out.Devices[i].Issues = append(out.Devices[i].Issues, system.Issue{Target: name, Field: string(m), Err: &system.Error{Kind: system.Unsupported, Message: "metric is not scoped to this function"}})
			continue
		}
		if seenSamples[name] == nil {
			seenSamples[name] = map[system.Metric]bool{}
		}
		if seenSamples[name][m] {
			err = system.FieldError(name, string(m), "duplicate reading")
			delete(out.Devices[i].Values, m)
		}
		seenSamples[name][m] = true
		if err != nil {
			out.Devices[i].Issues = append(out.Devices[i].Issues, system.Issue{Target: name, Field: string(m), Err: err})
			continue
		}
		out.Devices[i].Values[m] = value
	}
	return out, nil
}

func normalizedSample(s *pb.MetricSample) (system.Metric, system.MetricValue, error) {
	m := metric(s.GetMetricId())
	v := system.MetricValue{}
	if m == "" {
		return m, v, nil
	}
	bad := func() (system.Metric, system.MetricValue, error) {
		return m, v, system.FieldError(s.GetDevice().GetName(), string(m), "invalid type/range")
	}
	if m == system.MemoryUsedBytes || m == system.PerformanceState {
		raw, ok := s.GetTypedValue().(*pb.MetricSample_IntValue)
		if !ok || raw.IntValue < 0 {
			return bad()
		}
		v.Uint = system.Pointer(uint64(raw.IntValue))
		return m, v, nil
	}
	raw, ok := s.GetTypedValue().(*pb.MetricSample_FloatValue)
	if !ok || math.IsNaN(raw.FloatValue) || math.IsInf(raw.FloatValue, 0) {
		return bad()
	}
	n := raw.FloatValue
	switch m {
	case system.TemperatureCelsius:
		n /= 1000
	case system.PowerWatts:
		if n < 0 {
			return bad()
		}
		n /= 1e6
	case system.UtilizationPercent:
		if n < 0 || n > 100000 {
			return bad()
		}
		n /= 1000
	}
	v.Float = &n
	return m, v, nil
}
