package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

func pciLink(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", failure("PCI link", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		return "", failure("PCI link target", path, err)
	}
	address, err := system.NormalizePCIAddress(filepath.Base(target))
	if err != nil {
		return "", &system.Error{Kind: system.InvalidData, Op: "PCI link", Target: path, Cause: err}
	}
	return address, nil
}

func (c *Client) ReadSRIOV(ctx context.Context, address string) (system.SRIOV, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return system.SRIOV{}, err
	}
	address = filepath.Base(p)
	physfn := filepath.Join(p, "physfn")
	if _, e := os.Lstat(physfn); e == nil {
		parent, err := pciLink(physfn)
		if err != nil {
			return system.SRIOV{}, err
		}
		if parent == address {
			return system.SRIOV{}, system.FieldError(address, "physfn", "self reference")
		}
		parentPath, err := c.requireDevice(ctx, parent)
		if err != nil {
			return system.SRIOV{}, err
		}
		info, err := c.readPF(ctx, parentPath)
		if err != nil {
			return system.SRIOV{}, err
		}
		found := false
		for _, vf := range info.VirtualFunctions {
			if vf == address {
				found = true
			}
		}
		if !found {
			return system.SRIOV{}, &system.Error{Kind: system.NotReady, Target: address, Message: "parent has no matching virtfn link"}
		}
		return system.SRIOV{Kind: system.VirtualFunction, Supported: true, ParentPCIAddress: parent, ParentEnabledVFs: system.Pointer(info.EnabledVFs)}, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return system.SRIOV{}, failure("physfn", address, e)
	}
	return c.readPF(ctx, p)
}

func (c *Client) readPF(ctx context.Context, p string) (system.SRIOV, error) {
	info := system.SRIOV{Kind: system.PhysicalFunction, VirtualFunctions: []string{}}
	count, err := readUint(filepath.Join(p, "sriov_numvfs"), 32)
	if err != nil && !errors.Is(err, system.ErrNotFound) {
		return info, err
	}
	if err == nil {
		info.Supported, info.EnabledVFs = true, uint32(count)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return info, failure("PF", filepath.Base(p), err)
	}
	links := map[uint64]string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return info, err
		}
		if !strings.HasPrefix(entry.Name(), "virtfn") {
			continue
		}
		i, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "virtfn"), 10, 32)
		if err != nil || entry.Name() != "virtfn"+strconv.FormatUint(i, 10) {
			return info, system.FieldError(p, "virtfn", entry.Name())
		}
		vf, err := pciLink(filepath.Join(p, entry.Name()))
		if err != nil {
			return info, err
		}
		vfPath, err := c.requireDevice(ctx, vf)
		if err != nil {
			return info, err
		}
		parent, err := pciLink(filepath.Join(vfPath, "physfn"))
		if err != nil {
			return info, err
		}
		if parent != filepath.Base(p) || seen[vf] {
			return info, system.FieldError(p, "virtfn", "duplicate or mismatched parent")
		}
		seen[vf], links[i] = true, vf
	}
	if uint64(len(links)) != count {
		return info, &system.Error{Kind: system.NotReady, Target: filepath.Base(p), Message: "sriov_numvfs and virtfn links disagree"}
	}
	for i := uint64(0); i < count; i++ {
		vf, ok := links[i]
		if !ok {
			return info, system.FieldError(p, "virtfn", "non-contiguous indexes")
		}
		info.VirtualFunctions = append(info.VirtualFunctions, vf)
	}
	if total, e := readUint(filepath.Join(p, "sriov_totalvfs"), 32); e == nil {
		info.TotalVFs = system.Pointer(uint32(total))
		if count > total {
			return info, system.FieldError(p, "sriov_numvfs", "exceeds totalvfs")
		}
	} else if !errors.Is(e, system.ErrNotFound) {
		return info, e
	}
	return info, nil
}

// ObservePartitions requires a complete, consistent PF/VF and binding view.
// It applies no supported-profile or workload-mode policy (including VF count).
func (c *Client) ObservePartitions(ctx context.Context) ([]system.Device, error) {
	snapshot, err := c.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	if err := system.IssuesError(snapshot.Issues); err != nil {
		return nil, err
	}
	for _, d := range snapshot.Devices {
		for _, issue := range d.Issues {
			if issue.Field == "sriov" || issue.Field == "driver" || issue.Field == "driver_override" {
				return nil, issue.Err
			}
		}
	}
	return snapshot.Devices, nil
}
