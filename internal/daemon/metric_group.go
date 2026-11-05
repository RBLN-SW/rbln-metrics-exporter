package daemon

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

var exporterMetricIDs = []pb.MetricId{
	pb.MetricId_METRIC_ID_TEMPERATURE_MILLI_CELSIUS,
	pb.MetricId_METRIC_ID_POWER_MICRO_WATT,
	pb.MetricId_METRIC_ID_UTILIZATION_PERCENT_MILLI,
	pb.MetricId_METRIC_ID_MEMORY_USED_BYTES,
	pb.MetricId_METRIC_ID_PSTATE,
}

func (c *Client) ensureMetricGroup(ctx context.Context) error {
	group, err := c.findMetricGroup(ctx)
	if err != nil {
		return err
	}
	if group == nil {
		if !c.options.CreateMetricGroup {
			return fmt.Errorf("SMD metric group %q does not exist; provision the documented clock-free group or enable --create-metric-group", c.options.MetricGroupID)
		}
		r, err := c.metrics.CreateMetricGroup(ctx, &pb.CreateMetricGroupRequest{
			Name: "rbln-metrics-exporter", IdempotencyKey: c.options.MetricGroupID,
			MetricIds:      exporterMetricIDs,
			DeviceScope:    &pb.DeviceScope{Selector: &pb.DeviceScope_All{All: &emptypb.Empty{}}},
			SamplingPolicy: &pb.SamplingPolicy{Mode: &pb.SamplingPolicy_OnDemand{OnDemand: &emptypb.Empty{}}},
		})
		if status.Code(err) == codes.AlreadyExists {
			group, err = c.findMetricGroup(ctx)
		} else if err == nil {
			group = r.GetGroup()
		}
		if err != nil {
			return fmt.Errorf("create clock-free SMD metric group (requires CreateMetricGroup permission; alternatively pre-provision it): %w", err)
		}
	}
	return validateMetricGroup(group, c.options.MetricGroupID)
}

func validateMetricGroup(g *pb.MetricGroup, id string) error {
	if g == nil || g.GetId() != id {
		return fmt.Errorf("SMD metric group %q was not returned", id)
	}
	// Never sample the default group: dropping clock output does not prevent
	// the daemon from querying the clocks and waking an idle device.
	got := slices.Clone(g.GetMetricIds())
	want := slices.Clone(exporterMetricIDs)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) || g.GetDeviceScope().GetAll() == nil || g.GetSamplingPolicy().GetOnDemand() == nil {
		return fmt.Errorf("SMD metric group %q must contain only temperature, power, utilization, memory used and p-state, with all devices and on-demand sampling; refusing to sample or modify it", id)
	}
	return nil
}

func (c *Client) findMetricGroup(ctx context.Context) (*pb.MetricGroup, error) {
	token := ""
	seen := map[string]bool{}
	for {
		r, err := c.metrics.ListMetricGroups(ctx, &pb.ListMetricGroupsRequest{PageSize: 1000, PageToken: token})
		if err != nil {
			return nil, fmt.Errorf("ListMetricGroups: %w", err)
		}
		for _, unavailable := range r.GetUnreachable() {
			if unavailable.GetRef() == c.options.MetricGroupID {
				return nil, fmt.Errorf("SMD metric group %q is unreachable; cannot verify clock-free sampling", c.options.MetricGroupID)
			}
		}
		for _, g := range r.GetItems() {
			if g.GetId() == c.options.MetricGroupID {
				return g, nil
			}
		}
		token = r.GetNextPageToken()
		if token == "" {
			return nil, nil
		}
		if seen[token] {
			return nil, fmt.Errorf("ListMetricGroups repeated page token")
		}
		seen[token] = true
	}
}
