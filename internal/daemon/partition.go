package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/rebellions-sw/rbln-metrics-exporter/pkg/rblnservicesv1pb"
)

var pciAddress = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$`)

func (c *Client) partitionMetadata(d *DeviceInfo, inventory []*pb.DeviceInfo) {
	if d.IsVF || d.Compute {
		zero := uint32(0)
		d.NumVFs = &zero
		if !d.IsVF {
			d.PartitionMode = "none"
		}
	}
	if !d.IsVF && !d.Compute {
		d.PartitionMode = "hosting"
	}
	if c.options.PCIDevicesPath == "" || !pciAddress.MatchString(d.PCIBusID) {
		return
	}
	bus := strings.ToLower(d.PCIBusID)
	if d.IsVF {
		target, err := os.Readlink(filepath.Join(c.options.PCIDevicesPath, bus, "physfn"))
		if err != nil {
			return
		}
		bus = strings.ToLower(filepath.Base(target))
		if !pciAddress.MatchString(bus) {
			return
		}
		// Bind the sysfs profile to the same parent the daemon inventoried. A
		// rebind/repartition racing collection must not resolve an old DRA alias.
		parentMatches := false
		for _, p := range inventory {
			if p.GetName() == d.ParentName && !p.GetIsVf() && strings.EqualFold(p.GetPciBusId(), bus) {
				parentMatches = true
				break
			}
		}
		if !parentMatches {
			return
		}
	}
	raw, err := os.ReadFile(filepath.Join(c.options.PCIDevicesPath, bus, "sriov_numvfs"))
	if err != nil {
		return
	}
	count, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		return
	}
	n := uint32(count)
	if d.IsVF {
		// num_vfs retains its original meaning: enabled children of this function,
		// not the parent count. The parent's count supplies partition_mode instead.
		zero := uint32(0)
		d.NumVFs = &zero
		if n == 1 || n == 4 {
			d.PartitionMode = "vf" + strconv.FormatUint(count, 10)
		}
	} else {
		d.NumVFs = &n
		if n > 0 {
			d.Compute = false
			d.PartitionMode = "hosting"
		}
	}
}

// The daemon inventory can briefly outlive a bind to vfio-pci. Never attach
// native VF telemetry or workload labels to a function with a foreign driver.
func (c *Client) foreignDriver(bus string) bool {
	if c.options.PCIDevicesPath == "" || !pciAddress.MatchString(bus) {
		return false
	}
	target, err := os.Readlink(filepath.Join(c.options.PCIDevicesPath, strings.ToLower(bus), "driver"))
	return err == nil && filepath.Base(target) != "rebellions"
}
