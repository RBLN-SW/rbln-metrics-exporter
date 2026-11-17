package smd

import (
	"context"
	"math"
	"strconv"
	"strings"

	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

type TopologySnapshot struct {
	Devices map[string]system.Topology
	Issues  []system.Issue
}

func linkSpeed(raw string) *float64 {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return nil
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(parts[0], "GT/s"), 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}
func linkWidth(raw string) *uint32 {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(raw), "x"), 10, 32)
	if err != nil || n == 0 {
		return nil
	}
	return system.Pointer(uint32(n))
}
func topology(numa int32, cpus, group, speed, width string) system.Topology {
	t := system.Topology{CPUList: cpus, RSDGroup: group, LinkSpeedGTPerSecond: linkSpeed(speed), LinkWidth: linkWidth(width)}
	if numa >= 0 {
		t.NUMANode = system.Pointer(int(numa))
	}
	return t
}

func (c *Client) ListTopology(ctx context.Context) (TopologySnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	out := TopologySnapshot{Devices: map[string]system.Topology{}}
	token, seen := "", map[string]bool{}
	for {
		r, err := c.topology.ListTopology(ctx, &pb.ListTopologyRequest{PageSize: 1000, PageToken: token})
		if err != nil {
			return out, rpcError("ListTopology", "", err)
		}
		out.Issues = append(out.Issues, unreachable("topology", r.GetUnreachable())...)
		for _, e := range r.GetEntries() {
			if _, ok := out.Devices[e.GetDeviceName()]; ok {
				return out, system.FieldError(e.GetDeviceName(), "topology", "duplicate entry")
			}
			out.Devices[e.GetDeviceName()] = topology(e.GetNumaNode(), e.GetLocalCpulist(), e.GetRsdGroup(), e.GetPcieLinkSpeed(), e.GetPcieLinkWidth())
		}
		token = r.GetNextPageToken()
		if token == "" {
			break
		}
		if err := nextPage(seen, token); err != nil {
			return out, err
		}
	}
	for _, i := range out.Issues {
		delete(out.Devices, i.Target)
	}
	return out, nil
}
