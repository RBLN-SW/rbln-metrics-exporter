package host

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

var pciRootPattern = regexp.MustCompile(`^pci[0-9a-fA-F]{4,8}:[0-9a-fA-F]{2}$`)

func (c *Client) PCIAncestors(ctx context.Context, address string) ([]string, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, failure("PCI topology", address, err)
	}
	if !strings.EqualFold(filepath.Base(resolved), address) {
		return nil, system.FieldError(address, "PCI topology", resolved)
	}
	ancestors := []string{}
	current := string(filepath.Separator)
	for _, part := range strings.Split(resolved, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if len(ancestors) == 0 {
			if pciRootPattern.MatchString(part) {
				ancestors = append(ancestors, current)
			}
			continue
		}
		if _, err := system.NormalizePCIAddress(part); err != nil {
			return nil, system.FieldError(address, "PCI hierarchy", part)
		}
		ancestors = append(ancestors, current)
	}
	if len(ancestors) < 2 {
		return nil, &system.Error{Kind: system.Unsupported, Target: address, Message: "PCI root hierarchy is not exposed"}
	}
	return ancestors, nil
}

func (c *Client) IOMMUGroup(ctx context.Context, address string) (uint32, []string, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return 0, nil, err
	}
	link := filepath.Join(p, "iommu_group")
	target, err := os.Readlink(link)
	if err != nil {
		return 0, nil, attributeError("iommu_group", failure("iommu_group", address, err))
	}
	group, err := strconv.ParseUint(filepath.Base(target), 10, 32)
	if err != nil {
		return 0, nil, system.FieldError(address, "iommu_group", target)
	}
	entries, err := os.ReadDir(filepath.Join(link, "devices"))
	if err != nil {
		return 0, nil, failure("IOMMU members", address, err)
	}
	members := []string{}
	found := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		a, err := system.NormalizePCIAddress(entry.Name())
		if err != nil {
			return 0, nil, system.FieldError(address, "IOMMU member", entry.Name())
		}
		if _, err := os.Stat(filepath.Join(link, "devices", entry.Name())); err != nil {
			return 0, nil, failure("IOMMU member", a, err)
		}
		if strings.EqualFold(a, address) {
			found = true
		}
		members = append(members, a)
	}
	if !found {
		return 0, nil, system.FieldError(address, "IOMMU group", "function missing from members")
	}
	sort.Strings(members)
	return uint32(group), members, nil
}

func (c *Client) ReadTopology(ctx context.Context, address string) (system.Topology, error) {
	t := system.Topology{}
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return t, err
	}
	var errs []error
	record := func(e error) {
		if e != nil && !errors.Is(e, system.ErrNotFound) && !errors.Is(e, system.ErrUnsupported) {
			errs = append(errs, e)
		}
	}
	if value, e := readText(filepath.Join(p, "numa_node")); e == nil {
		n, e := strconv.Atoi(value)
		if e != nil || n < -1 {
			errs = append(errs, system.FieldError(address, "numa_node", value))
		} else if n >= 0 {
			t.NUMANode = &n
		}
	} else {
		record(e)
	}
	t.CPUList, err = readText(filepath.Join(p, "local_cpulist"))
	record(err)
	t.PCIAncestors, err = c.PCIAncestors(ctx, address)
	record(err)
	if len(t.PCIAncestors) > 0 {
		t.PCIeRoot = filepath.Base(t.PCIAncestors[0])
	}
	if g, members, e := c.IOMMUGroup(ctx, address); e == nil {
		t.IOMMUGroup, t.IOMMUMembers = &g, members
	} else {
		record(e)
	}
	for _, f := range []struct {
		name string
		dst  **uint32
	}{{"current_link_width", &t.LinkWidth}, {"max_link_width", &t.MaxLinkWidth}} {
		if n, e := readUint(filepath.Join(p, f.name), 32); e == nil {
			*f.dst = system.Pointer(uint32(n))
		} else {
			record(e)
		}
	}
	for _, f := range []struct {
		name string
		dst  **float64
	}{{"current_link_speed", &t.LinkSpeedGTPerSecond}, {"max_link_speed", &t.MaxLinkSpeedGTPerSecond}} {
		if raw, e := readText(filepath.Join(p, f.name)); e == nil {
			fields := strings.Fields(raw)
			if len(fields) == 0 {
				continue
			}
			n, e := strconv.ParseFloat(fields[0], 64)
			if e != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
				errs = append(errs, system.FieldError(address, f.name, raw))
			} else {
				*f.dst = &n
			}
		} else {
			record(e)
		}
	}
	return t, errors.Join(errs...)
}

// PCIDistance reports hop distance only inside one observed root. Unrelated
// roots have SeparateRoots=true and Known=false; no common parent is invented.
type PCIDistance struct {
	Known, SeparateRoots bool
	Hops                 int
}

func Distance(a, b []string) PCIDistance {
	if len(a) == 0 || len(b) == 0 {
		return PCIDistance{}
	}
	if a[0] != b[0] {
		return PCIDistance{SeparateRoots: true}
	}
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return PCIDistance{Known: true, Hops: len(a) + len(b) - 2*i}
}
