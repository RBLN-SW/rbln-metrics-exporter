package host

import (
	"context"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// Enrich joins metadata to freshly observed local PCI facts by PCI address.
// Call only for metadata from this same host. Metadata never overrides local
// binding, lifecycle, topology, node paths or PF/VF relationships. Local-only
// devices (including VFIO/unbound functions) stay in the result. For a matched
// function ReportedPCI retains the backend spelling for legacy projections.
// Local read diagnostics remain visible; metadata issues follow the fields
// supplied by the backend, including unsuccessful fallback reads.
func (c *Client) Enrich(ctx context.Context, metadata system.Snapshot) (system.Snapshot, error) {
	local, err := c.ListDevices(ctx)
	if err != nil {
		return local, err
	}
	local.Source = "sysfs+" + metadata.Source
	local.Issues = append(local.Issues, metadata.Issues...)
	byPCI := map[string]system.Device{}
	for _, m := range metadata.Devices {
		a, err := system.NormalizePCIAddress(m.PCIAddress)
		if err != nil {
			return local, err
		}
		if _, exists := byPCI[a]; exists {
			return local, system.FieldError(a, "metadata", "duplicate PCI address")
		}
		byPCI[a] = m
	}
	for i := range local.Devices {
		d := &local.Devices[i]
		m, exists := byPCI[d.PCIAddress]
		if !exists {
			continue
		}
		delete(byPCI, d.PCIAddress)
		if !d.DriverKnown || d.Driver != system.NativeDriver {
			continue
		}
		if d.Name == "" || d.Name != m.Name || (d.UUID != "" && m.UUID != "" && d.UUID != m.UUID) {
			d.Issues = append(d.Issues, system.Issue{Target: d.PCIAddress, Field: "metadata", Err: &system.Error{Kind: system.NotReady, Message: "metadata identity differs from current native function"}})
			continue
		}
		d.ReportedPCI = m.ReportedPCI
		for _, issue := range m.Issues {
			if usesMetadataField(d, issue.Field) {
				d.Issues = append(d.Issues, issue)
			}
		}
		if d.UUID == "" {
			d.UUID = m.UUID
		}
		if d.Serial == "" {
			d.Serial = m.Serial
		}
		if d.ProductName == "" {
			d.ProductName = m.ProductName
		}
		if d.MemoryTotalBytes == nil {
			d.MemoryTotalBytes = m.MemoryTotalBytes
		}
		if d.Versions.Driver == "" {
			d.Versions.Driver = m.Versions.Driver
		}
		if d.Versions.Firmware == "" {
			d.Versions.Firmware = m.Versions.Firmware
		}
		if d.Versions.SMC == "" {
			d.Versions.SMC = m.Versions.SMC
		}
		d.Index, d.Compute = m.Index, m.Compute
	}
	for a := range byPCI {
		local.Issues = append(local.Issues, system.Issue{Target: a, Field: "metadata", Err: &system.Error{Kind: system.NotReady, Message: "metadata function absent from local PCI snapshot"}})
	}
	return local, nil
}

func usesMetadataField(local *system.Device, field string) bool {
	switch field {
	case "index", "compute":
		return true
	case "device_id", "numa_node", "link_speed", "link_width":
		return local.ReportedPCI != nil
	case "uuid":
		return local.UUID == ""
	case "serial":
		return local.Serial == ""
	case "product_name":
		return local.ProductName == ""
	case "memory_total_bytes":
		return local.MemoryTotalBytes == nil
	case "versions":
		return local.Versions.Driver == "" || local.Versions.Firmware == "" || local.Versions.SMC == ""
	case "state/versions/memory": // combined legacy SMD observation
		return local.Versions.Driver == "" || local.Versions.Firmware == "" || local.MemoryTotalBytes == nil
	default:
		return false
	}
}
