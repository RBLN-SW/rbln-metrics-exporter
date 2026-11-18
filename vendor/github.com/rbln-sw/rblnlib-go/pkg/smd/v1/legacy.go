package smd

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rbln-sw/rblnlib-go/internal/identity"

	legacy "github.com/rbln-sw/rblnlib-go/internal/proto/smdlegacy"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func legacyState(s legacy.DeviceStatus) system.State {
	switch s {
	case legacy.DeviceStatus_READY:
		return system.StateReady
	case legacy.DeviceStatus_BUSY:
		return system.StateBusy
	case legacy.DeviceStatus_INIT:
		return system.StateInit
	case legacy.DeviceStatus_FAULT:
		return system.StateFault
	case legacy.DeviceStatus_FINISH:
		return system.StateFinish
	default:
		return system.StateUnknown
	}
}

func (c *Client) legacyInfo(ctx context.Context) (map[string]*legacy.DeviceInfo, error) {
	stream, err := c.legacy.GetTotalInfo(ctx, &legacy.Empty{})
	if err != nil {
		return nil, rpcError("legacy GetTotalInfo", "", err)
	}
	out := map[string]*legacy.DeviceInfo{}
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, rpcError("legacy GetTotalInfo stream", "", err)
		}
		if d.GetName() == "" || out[d.GetName()] != nil {
			return nil, system.FieldError(d.GetName(), "legacy telemetry", "empty/duplicate identity")
		}
		out[d.GetName()] = d
	}
}

func (c *Client) listLegacy(ctx context.Context) (system.Snapshot, error) {
	return c.listLegacyInventory(ctx, true)
}

func (c *Client) listLegacyInventory(ctx context.Context, withInfo bool) (system.Snapshot, error) {
	s := system.Snapshot{Source: "smd/v0", ObservedAt: time.Now(), Devices: []system.Device{}}
	stream, err := c.legacy.GetDeviceList(ctx, &legacy.Empty{})
	if err != nil {
		return s, rpcError("legacy GetDeviceList", "", err)
	}
	seenNames, seenAddresses := map[string]bool{}, map[string]bool{}
	for {
		raw, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return s, rpcError("legacy inventory stream", "", err)
		}
		d := system.Device{EnabledVFs: system.Pointer(raw.GetNumVfs()), Name: raw.GetName(), UUID: raw.GetUuid(), ProductName: raw.GetCardName(), ParentName: raw.GetParentName(), State: system.StateUnknown, FunctionKind: system.PhysicalFunction, VendorID: system.VendorID, Class: system.NPUClass, Compute: system.Pointer(raw.GetIsVf() || raw.GetNumVfs() == 0)}
		d.ReportedPCI = &system.ReportedPCI{Address: raw.GetPciBusId(), DeviceID: raw.GetDevId()}
		d.PCIAddress, err = system.NormalizePCIAddress(raw.GetPciBusId())
		if err != nil || system.ValidTargets([]string{d.Name}) != nil {
			s.Issues = append(s.Issues, system.Issue{Target: d.Name, Field: "identity", Err: system.FieldError(d.Name, "legacy inventory", "invalid identity")})
			continue
		}
		if seenNames[d.Name] || seenAddresses[d.PCIAddress] {
			return s, system.FieldError(d.Name, "legacy inventory", "duplicate identity")
		}
		seenNames[d.Name], seenAddresses[d.PCIAddress] = true, true
		if raw.GetIsVf() {
			d.FunctionKind = system.VirtualFunction
		} else if n, ok := identity.NativePFOrdinal(d.Name); ok {
			d.NativeIndex = &n
		}
		if n, e := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(raw.GetDevId()), "0x"), 16, 16); e == nil {
			d.DeviceID = uint16(n)
			d.DeviceIDKnown = true
		} else {
			d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "device_id", Err: system.FieldError(d.Name, "device_id", raw.GetDevId())})
		}
		s.Devices = append(s.Devices, d)
	}
	byName := map[string]int{}
	for i, d := range s.Devices {
		byName[d.Name] = i
	}
	for i := range s.Devices {
		d := &s.Devices[i]
		if parent, ok := byName[d.ParentName]; ok && d.FunctionKind == system.VirtualFunction {
			d.ParentPCIAddress = s.Devices[parent].PCIAddress
			s.Devices[parent].Compute = system.Pointer(false)
			if s.Devices[parent].EnabledVFs != nil && *s.Devices[parent].EnabledVFs == 0 {
				s.Devices[parent].EnabledVFs = nil
			}
		}
	}
	if !withInfo {
		return s, nil
	}
	infos, infoErr := c.legacyInfo(ctx)
	for i := range s.Devices {
		d := &s.Devices[i]
		info := infos[d.Name]
		if infoErr != nil || info == nil {
			e := infoErr
			if e == nil {
				e = system.ErrUnavailable
			}
			d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "state/versions/memory", Err: e})
			continue
		}
		d.State = legacyState(info.GetDevStatus())
		d.Versions = system.Versions{Driver: info.GetDrvVersion(), Firmware: info.GetFwVersion()}
		if info.GetErrStatus() == legacy.Status_SUCCEED {
			d.MemoryTotalBytes = legacyBytes(info.GetTotalMem())
		}
	}
	return s, nil
}

func legacyBytes(gib float32) *uint64 {
	n := math.Round(float64(gib) * (1 << 30))
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.Exp2(64) {
		return nil
	}
	return system.Pointer(uint64(n))
}

func (c *Client) allLegacyTelemetry(ctx context.Context) (system.TelemetrySnapshot, error) {
	inv, err := c.listLegacyInventory(ctx, false)
	if err != nil {
		return system.TelemetrySnapshot{}, err
	}
	return c.telemetryLegacy(ctx, inv)
}

func (c *Client) telemetryLegacy(ctx context.Context, inv system.Snapshot) (system.TelemetrySnapshot, error) {
	out := system.TelemetrySnapshot{Source: "smd/v0", ObservedAt: time.Now(), Issues: inv.Issues}
	infos, err := c.legacyInfo(ctx)
	if err != nil {
		return out, err
	}
	topos, topoErr := c.legacy.RblnListTopology(ctx, &legacy.DeviceFilter{})
	if topoErr != nil && status.Code(topoErr) != codes.Unimplemented {
		out.Issues = append(out.Issues, system.Issue{Field: "topology", Err: rpcError("legacy topology", "", topoErr)})
	}
	byName := map[string]system.Topology{}
	for _, t := range topos.GetEntries() {
		if t.GetError().GetCode() != 0 || t.GetError().GetMessage() != "" {
			out.Issues = append(out.Issues, system.Issue{Target: t.GetDeviceName(), Field: "topology", Err: &system.Error{Kind: system.Unavailable, Message: t.GetError().GetMessage()}})
			continue
		}
		byName[t.GetDeviceName()] = topology(t.GetNumaNode(), t.GetLocalCpulist(), t.GetRsdGroup(), t.GetPcieLinkSpeed(), t.GetPcieLinkWidth())
	}
	for _, d := range inv.Devices {
		if t, ok := byName[d.Name]; ok {
			d.Topology = t
		}
		reading := system.Telemetry{Device: d, Values: map[system.Metric]system.MetricValue{}}
		info := infos[d.Name]
		if info == nil {
			reading.Issues = append(reading.Issues, system.Issue{Target: d.Name, Field: "telemetry", Err: system.ErrUnavailable})
			out.Devices = append(out.Devices, reading)
			continue
		}
		reading.Device.State = legacyState(info.GetDevStatus())
		reading.Device.Versions = system.Versions{Driver: info.GetDrvVersion(), Firmware: info.GetFwVersion()}
		if info.GetErrStatus() == legacy.Status_SUCCEED {
			reading.Device.MemoryTotalBytes = legacyBytes(info.GetTotalMem())
		}

		if info.GetErrStatus() != legacy.Status_SUCCEED {
			reading.Issues = append(reading.Issues, system.Issue{Target: d.Name, Field: "telemetry", Err: system.ErrUnavailable})
		} else {
			floats := []struct {
				metric system.Metric
				value  float64
				valid  bool
			}{
				{system.TemperatureCelsius, float64(info.GetTemperature()) / 1000, d.FunctionKind != system.VirtualFunction},
				{system.PowerWatts, float64(info.GetWatt()) / 1e6, d.FunctionKind != system.VirtualFunction && info.GetWatt() >= 0},
				{system.UtilizationPercent, float64(info.GetUtilization()), d.Compute != nil && *d.Compute && info.GetUtilization() >= 0 && info.GetUtilization() <= 100},
			}
			for _, v := range floats {
				if v.valid && !math.IsNaN(v.value) && !math.IsInf(v.value, 0) {
					reading.Values[v.metric] = system.MetricValue{Float: system.Pointer(v.value)}
				}
			}
			if d.Compute != nil && *d.Compute {
				if n := legacyBytes(info.GetUsedMem()); n != nil {
					reading.Values[system.MemoryUsedBytes] = system.MetricValue{Uint: n}
				}
			}
			if n := info.GetPState(); n >= 0 {
				reading.Values[system.PerformanceState] = system.MetricValue{Uint: system.Pointer(uint64(n))}
			}
			// Successful v0 messages use scalar fields: zero is a reading.
			// An absent in-scope value here has already failed validation.
			for _, id := range telemetryIDs {
				m := metric(id)
				if _, ok := reading.Values[m]; !ok && metricApplies(d, m) {
					reading.Issues = append(reading.Issues, system.Issue{Target: d.Name, Field: string(m), Err: system.FieldError(d.Name, string(m), "invalid legacy reading")})
				}
			}
		}
		v, err := c.legacy.GetVersion(ctx, &legacy.Device{Name: d.Name})
		if err == nil && v.GetErrStatus() == legacy.Status_SUCCEED {
			reading.Device.Versions.SMC = v.GetSmcVersion()
		} else if status.Code(err) != codes.Unimplemented {
			if err == nil {
				err = system.ErrUnavailable
			}
			reading.Issues = append(reading.Issues, system.Issue{Target: d.Name, Field: "smc_version", Err: rpcError("legacy GetVersion", d.Name, err)})
		}
		out.Devices = append(out.Devices, reading)
	}
	return out, nil
}
