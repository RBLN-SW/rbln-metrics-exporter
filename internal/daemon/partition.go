package daemon

import (
	"context"
	"strconv"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// Resource aliases and compute-accounting policy remain exporter concerns;
// the library owns interpretation and validation of the live PCI topology.
func (c *Client) partitionMetadata(ctx context.Context, d *DeviceInfo, inventory map[string]system.Device) {
	if d.IsVF || d.Compute {
		d.NumVFs = system.Pointer(uint32(0))
		if !d.IsVF {
			d.PartitionMode = "none"
		}
	} else {
		d.PartitionMode = "hosting"
	}
	if c.local == nil {
		return
	}
	facts, err := c.local.ReadSRIOV(ctx, d.PCIBusID)
	if err != nil {
		return
	}
	if facts.Kind == system.VirtualFunction {
		parent, ok := inventory[facts.ParentPCIAddress]
		if !ok || (d.ParentName != "" && d.ParentName != parent.Name) {
			return
		}
		d.IsVF = true
		d.ParentName = parent.Name
		d.NumVFs = system.Pointer(uint32(0))
		if facts.ParentEnabledVFs != nil {
			n := *facts.ParentEnabledVFs
			if n == 1 || n == 4 {
				d.PartitionMode = "vf" + strconv.FormatUint(uint64(n), 10)
			}
		}
	} else {
		d.NumVFs = system.Pointer(facts.EnabledVFs)
		if facts.EnabledVFs > 0 {
			d.Compute = false
			d.PartitionMode = "hosting"
		}
	}
}
