package smd

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/rbln-sw/rblnlib-go/internal/identity"

	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type unsupportedQuery struct{ error }

func (e *unsupportedQuery) Unwrap() error { return e.error }

// ListDevices reads cached daemon inventory. It does not assert live PCI
// topology, binding, node existence, or atomicity across pages. Use host/v1
// to revalidate before changing a local device.
func (c *Client) ListDevices(ctx context.Context) (system.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	snapshot, err := c.listV1(ctx)
	var unsupported *unsupportedQuery
	if errors.As(err, &unsupported) && !c.opts.DisableLegacy {
		return c.listLegacy(ctx)
	}
	if err == nil && !c.opts.DisableLegacy {
		c.supplementStates(ctx, &snapshot)
	}
	return snapshot, rpcError("ListDevices", c.opts.Endpoint, err)
}

func (c *Client) supplementStates(ctx context.Context, s *system.Snapshot) {
	needs := func(d system.Device) bool {
		for _, issue := range d.Issues {
			var e *system.Error
			if issue.Field == "state" && errors.As(issue.Err, &e) && e.Code == 0 {
				return true
			}
		}
		return false
	}
	needed := false
	for _, d := range s.Devices {
		needed = needed || needs(d)
	}
	if !needed {
		return
	}
	infos, err := c.legacyInfo(ctx)
	if err != nil {
		if !errors.Is(err, system.ErrUnsupported) {
			for i := range s.Devices {
				if needs(s.Devices[i]) {
					s.Devices[i].Issues = append(s.Devices[i].Issues, system.Issue{Target: s.Devices[i].Name, Field: "state_lookup", Err: err})
				}
			}
		}
		return
	} // original unknown-state issue remains visible
	for i := range s.Devices {
		d := &s.Devices[i]
		if !needs(*d) || infos[d.Name] == nil {
			continue
		}
		value := legacyState(infos[d.Name].GetDevStatus())
		if value == system.StateUnknown {
			continue
		}
		d.State = value
		issues := d.Issues[:0]
		for _, issue := range d.Issues {
			if issue.Field != "state" {
				issues = append(issues, issue)
			}
		}
		d.Issues = issues
	}
}

func (c *Client) listV1(ctx context.Context) (system.Snapshot, error) {
	s := system.Snapshot{Source: "smd/v1", ObservedAt: time.Now(), Devices: []system.Device{}}
	token := ""
	seenPages, seenNames, seenAddresses := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for {
		r, err := c.inventory.ListDevices(ctx, &pb.ListDevicesRequest{Filter: pb.DeviceListFilter_DEVICE_LIST_FILTER_ALL, Detail: pb.DeviceListDetail_DEVICE_LIST_DETAIL_FULL, PageSize: 1000, PageToken: token})
		if err != nil {
			// Only an initial unsupported method is eligible for protocol fallback.
			if token != "" {
				return s, rpcError("ListDevices page", token, err)
			}
			if status.Code(err) == codes.Unimplemented {
				return s, &unsupportedQuery{rpcError("ListDevices", c.opts.Endpoint, err)}
			}
			return s, err
		}
		s.Issues = append(s.Issues, unreachable("inventory", r.GetUnreachable())...)
		blocked := map[string]bool{}
		for _, issue := range s.Issues {
			blocked[issue.Target] = true
		}
		for _, raw := range r.GetItems() {
			if blocked[raw.GetName()] {
				continue
			}
			d, err := deviceFromV1(raw)
			if err != nil {
				s.Issues = append(s.Issues, system.Issue{Target: raw.GetName(), Field: "identity", Err: err})
				continue
			}
			if seenNames[d.Name] || seenAddresses[d.PCIAddress] {
				return s, system.FieldError(d.Name, "inventory", "duplicate identity")
			}
			seenNames[d.Name], seenAddresses[d.PCIAddress] = true, true
			if raw.Versions == nil {
				v, e := c.inventory.GetVersion(ctx, &pb.GetVersionRequest{Device: &pb.DeviceRef{Name: d.Name}})
				if e == nil {
					d.Versions = system.Versions{Driver: v.GetDrvVersion(), Firmware: v.GetFwVersion(), SMC: v.GetSmcVersion()}
				} else {
					d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "versions", Err: rpcError("GetVersion", d.Name, e)})
				}
			}
			if raw.Memory == nil && d.Compute != nil && *d.Compute {
				m, e := c.inventory.GetMemorySpec(ctx, &pb.GetMemorySpecRequest{Device: &pb.DeviceRef{Name: d.Name}})
				if e == nil && m.GetMemory() != nil {
					d.MemoryTotalBytes = system.Pointer(m.GetMemory().GetTotalBytes())
				} else {
					if e == nil {
						e = system.FieldError(d.Name, "memory", "missing memory spec")
					}
					d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "memory_total_bytes", Err: rpcError("GetMemorySpec", d.Name, e)})
				}
			}
			s.Devices = append(s.Devices, d)
		}
		token = r.GetNextPageToken()
		if token == "" {
			break
		}
		if err := nextPage(seenPages, token); err != nil {
			return s, err
		}
	}
	byName := map[string]string{}
	for _, d := range s.Devices {
		byName[d.Name] = d.PCIAddress
	}
	for i := range s.Devices {
		s.Devices[i].ParentPCIAddress = byName[s.Devices[i].ParentName]
	}
	return s, nil
}

func deviceFromV1(raw *pb.DeviceInfo) (system.Device, error) {
	d := system.Device{Name: raw.GetName(), UUID: raw.GetUuid(), Serial: raw.GetSerialId(), ProductName: raw.GetCardName(), State: state(raw.GetState()), FunctionKind: system.PhysicalFunction, ParentName: raw.GetParentName(), Compute: system.Pointer(raw.NpuId != nil), VendorID: system.VendorID, Class: system.NPUClass}
	if err := system.ValidTargets([]string{d.Name}); err != nil {
		return d, system.FieldError(d.Name, "name", "invalid function name")
	}
	var err error
	d.ReportedPCI = &system.ReportedPCI{Address: raw.GetPciBusId(), DeviceID: raw.GetDevId()}
	d.PCIAddress, err = system.NormalizePCIAddress(raw.GetPciBusId())
	if err != nil {
		return d, system.FieldError(d.Name, "pci_bus_id", raw.GetPciBusId())
	}
	if raw.GetIsVf() {
		d.FunctionKind = system.VirtualFunction
	} else if n, ok := identity.NativePFOrdinal(d.Name); ok {
		d.NativeIndex = &n
	}
	if raw.NpuId != nil {
		d.Index = system.Pointer(*raw.NpuId)
	}
	if id, e := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(raw.GetDevId()), "0x"), 16, 16); e == nil {
		d.DeviceID = uint16(id)
		d.DeviceIDKnown = true
	} else {
		d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "device_id", Err: system.FieldError(d.Name, "device_id", raw.GetDevId())})
	}
	if v := raw.GetVersions(); v != nil {
		d.Versions = system.Versions{Driver: v.GetDrvVersion(), Firmware: v.GetFwVersion(), SMC: v.GetSmcVersion()}
	}
	if m := raw.GetMemory(); m != nil {
		d.MemoryTotalBytes = system.Pointer(m.GetTotalBytes())
	}
	if d.State == system.StateUnknown {
		d.Issues = append(d.Issues, system.Issue{Target: d.Name, Field: "state", Err: &system.Error{Kind: system.Unsupported, Message: "unrecognized or unspecified SMD lifecycle state", Code: int32(raw.GetState())}})
	}
	return d, nil
}
