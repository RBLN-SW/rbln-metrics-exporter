package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	legacy "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicespb"
	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func legacyState(s legacy.DeviceStatus) pb.DeviceState {
	// The enum numbers differ between APIs; never cast them.
	switch s {
	case legacy.DeviceStatus_READY:
		return pb.DeviceState_DEVICE_STATE_READY
	case legacy.DeviceStatus_BUSY:
		return pb.DeviceState_DEVICE_STATE_BUSY
	case legacy.DeviceStatus_INIT:
		return pb.DeviceState_DEVICE_STATE_INIT
	case legacy.DeviceStatus_FAULT:
		return pb.DeviceState_DEVICE_STATE_FAULT
	case legacy.DeviceStatus_FINISH:
		return pb.DeviceState_DEVICE_STATE_FINISH
	default:
		return pb.DeviceState_DEVICE_STATE_UNSPECIFIED
	}
}

func (c *Client) legacyTotalInfo(ctx context.Context) (map[string]*legacy.DeviceInfo, error) {
	stream, err := c.legacy.GetTotalInfo(ctx, &legacy.Empty{})
	if err != nil {
		return nil, fmt.Errorf("v0 getTotalInfo: %w", err)
	}
	infos := map[string]*legacy.DeviceInfo{}
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return infos, nil
		}
		if err != nil {
			return nil, fmt.Errorf("v0 getTotalInfo stream: %w", err)
		}
		if d.GetName() != "" {
			infos[d.GetName()] = d
		}
	}
}

func (c *Client) supplementLegacyStates(ctx context.Context, devices []DeviceInfo) {
	needed := false
	for _, d := range devices {
		if d.DevState == pb.DeviceState_DEVICE_STATE_UNSPECIFIED {
			needed = true
			break
		}
	}
	if !needed {
		c.stateLookupFailed = false
		return
	}
	infos, err := c.legacyTotalInfo(ctx)
	if status.Code(err) == codes.Unimplemented {
		c.stateLookupFailed = false
		return
	}
	if err != nil {
		if !c.stateLookupFailed {
			slog.Warn("Legacy state lookup failed", "err", err, "effect", "v1 readings retained; unknown states remain unknown and per-device collection_success is 0")
		}
		c.stateLookupFailed = true
	} else {
		c.stateLookupFailed = false
	}
	for i := range devices {
		d := &devices[i]
		// A future v1 enum value is not a missing field. Preserve it so the
		// collector leaves the state unavailable rather than overriding a newer state.
		if d.DevState != pb.DeviceState_DEVICE_STATE_UNSPECIFIED {
			continue
		}
		info := infos[d.Name]
		if err != nil || info == nil {
			d.CollectionSucceeded = false
			continue
		}
		// err_status represented READY in Python and constant success in Rust.
		// Neither is a substitute for the actual dev_status field.
		d.DevState = legacyState(info.GetDevStatus())
	}
}

func (c *Client) legacyDevices(ctx context.Context) ([]*legacy.Device, error) {
	stream, err := c.legacy.GetDeviceList(ctx, &legacy.Empty{})
	if err != nil {
		return nil, fmt.Errorf("v0 getDeviceList: %w", err)
	}
	var devices []*legacy.Device
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return devices, nil
		}
		if err != nil {
			return nil, fmt.Errorf("v0 getDeviceList stream: %w", err)
		}
		if d.GetName() != "" {
			devices = append(devices, d)
		}
	}
}

func (c *Client) legacyInventory(devices []*legacy.Device) []*pb.DeviceInfo {
	inventory := make([]*pb.DeviceInfo, 0, len(devices))
	byBus := map[string]*pb.DeviceInfo{}
	for _, d := range devices {
		item := &pb.DeviceInfo{Name: d.GetName(), Uuid: d.GetUuid(), DevId: d.GetDevId(), CardName: d.GetCardName(), PciBusId: d.GetPciBusId(), IsVf: d.GetIsVf(), ParentName: d.GetParentName()}
		if d.GetIsVf() || d.GetNumVfs() == 0 {
			n := uint32(0)
			item.NpuId = &n
		}
		inventory = append(inventory, item)
		if pciAddress.MatchString(item.PciBusId) {
			byBus[strings.ToLower(item.PciBusId)] = item
		}
	}
	for _, d := range inventory {
		// Older v0 schemas lack VF metadata. Local PCI relationships can supply
		// it; a gateway must never consult its own host to classify a remote NPU.
		if c.options.PCIDevicesPath != "" && pciAddress.MatchString(d.PciBusId) {
			target, err := os.Readlink(filepath.Join(c.options.PCIDevicesPath, strings.ToLower(d.PciBusId), "physfn"))
			if err == nil {
				d.IsVf = true
				if parent := byBus[strings.ToLower(filepath.Base(target))]; parent != nil {
					d.ParentName = parent.Name
					parent.NpuId = nil
				}
			}
		}
		if d.IsVf && d.ParentName != "" {
			for _, parent := range inventory {
				if parent.Name == d.ParentName {
					parent.NpuId = nil
				}
			}
		}
	}
	return inventory
}

func (c *Client) legacyTopology(ctx context.Context) (map[string]*TopologyInfo, map[string]bool, error) {
	r, err := c.legacy.RblnListTopology(ctx, &legacy.DeviceFilter{})
	if status.Code(err) == codes.Unimplemented {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("v0 RblnListTopology: %w", err)
	}
	out := map[string]*TopologyInfo{}
	failed := map[string]bool{}
	for _, e := range r.GetEntries() {
		if e.GetError().GetCode() != 0 || e.GetError().GetMessage() != "" {
			failed[e.GetDeviceName()] = true
			continue
		}
		out[e.GetDeviceName()] = &TopologyInfo{NUMANode: e.GetNumaNode(), CPUList: e.GetLocalCpulist(), RSDGroup: e.GetRsdGroup(), LinkSpeed: parseLinkSpeed(e.GetPcieLinkSpeed()), LinkWidth: parseLinkWidth(e.GetPcieLinkWidth())}
	}
	return out, failed, nil
}

func (c *Client) collectV0(ctx context.Context) ([]DeviceInfo, map[string]bool, error) {
	devices, err := c.legacyDevices(ctx)
	if err != nil {
		return nil, nil, err
	}
	infos, err := c.legacyTotalInfo(ctx)
	if err != nil {
		return nil, nil, err
	}
	topology, failedTopology, err := c.legacyTopology(ctx)
	if err != nil {
		return nil, nil, err
	}
	inventory := c.legacyInventory(devices)
	out := make([]DeviceInfo, 0, len(devices))
	missing := map[string]bool{}
	for i, raw := range devices {
		identity := inventory[i]
		if c.foreignDriver(identity.PciBusId) {
			continue
		}
		count := raw.GetNumVfs()
		d := DeviceInfo{Name: identity.Name, UUID: identity.Uuid, DeviceID: identity.DevId, Card: identity.CardName, PCIBusID: identity.PciBusId, IsVF: identity.IsVf, ParentName: identity.ParentName, NumVFs: &count, Compute: identity.NpuId != nil, CollectionSucceeded: !failedTopology[identity.Name], Topology: topology[identity.Name]}
		if !d.IsVF && !d.Compute && count == 0 {
			// A child proves this is a hosting PF, but older v0 schemas do not
			// report the enabled VF count. Do not publish a misleading zero.
			d.NumVFs = nil
		}
		c.partitionMetadata(&d, inventory)
		if !d.IsVF && d.Topology != nil {
			d.PCIeLinkSpeed, d.PCIeLinkWidth = d.Topology.LinkSpeed, d.Topology.LinkWidth
		}
		if info := infos[d.Name]; info != nil {
			d.DevState = legacyState(info.GetDevStatus())
			d.DriverVersion, d.FirmwareVersion = info.GetDrvVersion(), info.GetFwVersion()
			if info.GetErrStatus() == legacy.Status_SUCCEED {
				// v0 scalar values have no presence information. Preserve the readings
				// the daemon supplied, including zero; do not invent per-field validity.
				if !d.IsVF {
					d.Temperature = legacyReading(float64(info.GetTemperature())/1000, false)
					d.Power = legacyReading(float64(info.GetWatt())/1e6, true)
				}
				if d.Compute {
					d.MemoryTotalBytes = legacyBytes(info.GetTotalMem())
					d.MemoryUsedBytes = legacyBytes(info.GetUsedMem())
					d.Utilization = legacyReading(float64(info.GetUtilization()), true)
				}
				if n := int64(info.GetPState()); n >= 0 {
					d.PState = &n
				}
			} else {
				d.CollectionSucceeded = false
			}
		} else {
			missing[d.Name] = true
			d.CollectionSucceeded = false
		}
		version, err := c.legacy.GetVersion(ctx, &legacy.Device{Name: d.Name})
		switch {
		case status.Code(err) == codes.Unimplemented:
		case err != nil && !deviceUnavailable(err):
			return nil, nil, fmt.Errorf("v0 getVersion(%s): %w", d.Name, err)
		case err != nil || version.GetErrStatus() != legacy.Status_SUCCEED:
			d.CollectionSucceeded = false
		default:
			d.SMCVersion = version.GetSmcVersion()
		}
		out = append(out, d)
	}
	return out, missing, nil
}

func legacyReading(v float64, nonnegative bool) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || (nonnegative && v < 0) {
		return nil
	}
	return &v
}

func legacyBytes(gib float32) *uint64 {
	n := math.Round(float64(gib) * (1 << 30))
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.Exp2(64) {
		return nil
	}
	value := uint64(n)
	return &value
}
