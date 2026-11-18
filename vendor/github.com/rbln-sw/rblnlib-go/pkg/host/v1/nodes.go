package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rbln-sw/rblnlib-go/internal/identity"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"golang.org/x/sys/unix"
)

type functionInfo struct{ name, path, node string }

func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, "\\\x00\n\r")
}

// function resolves identity through the PCI relationship, never by assuming
// that rblnN and accelN use the same N. accel class attributes take precedence
// when both classes exist, matching the v4 SSW attribute lookup.
func (c *Client) function(ctx context.Context, address string) (functionInfo, error) {
	a, err := system.NormalizePCIAddress(address)
	if err != nil {
		return functionInfo{}, err
	}
	for _, class := range []string{"accel", "rebellions"} {
		root := c.classRoot(class)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return functionInfo{}, failure("function class", root, err)
		}
		var matches []functionInfo
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return functionInfo{}, err
			}
			if class == "rebellions" && !identity.NativeFunction(entry.Name()) {
				continue
			}
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			p := filepath.Join(root, entry.Name())
			owns, err := ownsPCIAddress(p, a)
			if err != nil {
				return functionInfo{}, err
			}
			if !owns {
				continue
			}
			name := entry.Name()
			node := entry.Name()
			if class == "accel" {
				name, err = readText(filepath.Join(p, "name"))
				// KMD 3.4 exposes generic accel minors alongside the legacy
				// class, without the RBLN name/attributes added by v4. Their
				// presence alone is not evidence of the v4 device interface.
				if errors.Is(err, system.ErrNotFound) {
					continue
				}
				if err != nil {
					return functionInfo{}, err
				}
				node = filepath.Join("accel", entry.Name())
			}
			if !safeName(name) {
				return functionInfo{}, system.FieldError(p, "name", name)
			}
			if raw, e := readText(filepath.Join(p, "uevent")); e == nil {
				for _, line := range strings.Split(raw, "\n") {
					if v, ok := strings.CutPrefix(line, "DEVNAME="); ok {
						node = v
					}
				}
			} else if !errors.Is(e, system.ErrNotFound) {
				return functionInfo{}, e
			}
			if !filepath.IsLocal(node) || filepath.Clean(node) != node || strings.ContainsAny(node, "\\\x00\n\r") {
				return functionInfo{}, system.FieldError(p, "DEVNAME", node)
			}
			matches = append(matches, functionInfo{name: name, path: p, node: node})
		}
		if len(matches) > 1 {
			return functionInfo{}, &system.Error{Kind: system.Conflict, Target: a, Message: "multiple native function identities"}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
	}
	// PCI-only mounts expose the live identity independently of SMD caches.
	pciPath, _ := c.devicePath(a)
	entries, err := os.ReadDir(filepath.Join(pciPath, "rebellions"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return functionInfo{}, failure("PCI function identity", a, err)
	}
	var found *functionInfo
	for _, entry := range entries {
		if !identity.NativeFunction(entry.Name()) {
			continue
		}
		p := filepath.Join(pciPath, "rebellions", entry.Name())
		raw, err := readText(filepath.Join(p, "pci_addr"))
		if errors.Is(err, system.ErrNotFound) {
			continue
		}
		if err != nil {
			return functionInfo{}, err
		}
		if !strings.EqualFold(raw, a) || !safeName(entry.Name()) {
			return functionInfo{}, system.FieldError(a, "native identity", entry.Name())
		}
		if found != nil {
			return functionInfo{}, &system.Error{Kind: system.Conflict, Target: a, Message: "ambiguous native identity"}
		}
		f := functionInfo{name: entry.Name(), path: p, node: entry.Name()}
		found = &f
	}
	if found != nil {
		return *found, nil
	}
	return functionInfo{}, &system.Error{Kind: system.NotReady, Target: a, Message: "native function not present in sysfs"}
}

// Both class directories are shared with other devices. Inspect ownership
// before following a link so a platform device or disappearing peer cannot
// invalidate the requested function. Reject contradictory claims on the target.
func ownsPCIAddress(entry, address string) (bool, error) {
	link := filepath.Join(entry, "device")
	target, linkErr := os.Readlink(link)
	if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
		return false, failure("function device link", link, linkErr)
	}
	hasLink := linkErr == nil
	owns := hasLink && strings.EqualFold(filepath.Base(target), address)
	raw, err := readText(filepath.Join(entry, "pci_addr"))
	if hasLink && !owns {
		if err == nil && strings.EqualFold(raw, address) {
			return false, system.FieldError(entry, "pci_addr", "disagrees with device link")
		}
		return false, nil
	}
	if err != nil && !errors.Is(err, system.ErrNotFound) {
		return false, err
	}
	if !hasLink {
		if err != nil {
			return false, nil
		}
		owner, err := system.NormalizePCIAddress(raw)
		if err != nil {
			return false, system.FieldError(entry, "pci_addr", raw)
		}
		return owner == address, nil
	}
	if _, err := os.Stat(link); err != nil {
		return false, failure("function device link", link, err)
	}
	if err == nil && !strings.EqualFold(raw, address) {
		return false, system.FieldError(entry, "pci_addr", "disagrees with device link")
	}
	return true, nil
}

// NativeName resolves the currently attached driver's command name. It is not
// an enumeration index and may change after a bind/reset.
func (c *Client) NativeName(ctx context.Context, address string) (string, error) {
	driver, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return "", err
	}
	if driver != system.NativeDriver {
		return "", &system.Error{Kind: system.NotReady, Target: address, Message: "native driver is not bound"}
	}
	f, err := c.function(ctx, address)
	return f.name, err
}

// NativePFIdentity is the native PF ordinal used by existing per-NPU config
// labels. It is distinct from SMD/SMI's usable-device Index and from an accel
// node minor number. Re-resolve after every reset/rebind.
type NativePFIdentity struct {
	Name, PCIAddress string
	Index            uint32
}

func (c *Client) NativePF(ctx context.Context, address string) (NativePFIdentity, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return NativePFIdentity{}, err
	}
	if _, err := os.Lstat(filepath.Join(p, "physfn")); err == nil {
		return NativePFIdentity{}, &system.Error{Kind: system.InvalidArgument, Target: address, Message: "function is a VF"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return NativePFIdentity{}, failure("physfn", address, err)
	}
	name, err := c.NativeName(ctx, address)
	if err != nil {
		return NativePFIdentity{}, err
	}
	raw, ok := strings.CutPrefix(name, "rbln")
	n, e := strconv.ParseUint(raw, 10, 32)
	if !ok || e != nil || strconv.FormatUint(n, 10) != raw {
		return NativePFIdentity{}, &system.Error{Kind: system.Unsupported, Target: name, Message: "driver exposes no supported native PF ordinal"}
	}
	return NativePFIdentity{Name: name, PCIAddress: filepath.Base(p), Index: uint32(n)}, nil
}

func (c *Client) ReadState(ctx context.Context, address string) (system.State, error) {
	driver, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return system.StateUnknown, err
	}
	if driver != system.NativeDriver {
		return system.StateUnknown, &system.Error{Kind: system.Unsupported, Target: address, Message: "no native lifecycle status"}
	}
	f, err := c.function(ctx, address)
	if err != nil {
		return system.StateUnknown, err
	}
	n, err := readUint(filepath.Join(f.path, "status"), 32)
	if err != nil {
		return system.StateUnknown, attributeError("status", err)
	}
	switch n {
	case 0:
		return system.StateReady, nil
	case 1:
		return system.StateBusy, nil
	case 2:
		return system.StateProbing, nil
	case 3:
		return system.StateInit, nil
	case 4:
		return system.StateFault, nil
	case 5:
		return system.StateFinish, nil
	default:
		return system.StateUnknown, &system.Error{Kind: system.Unsupported, Target: address, Op: "status", Message: strconv.FormatUint(n, 10)}
	}
}

func (c *Client) node(relative, kind string) (system.DeviceNode, error) {
	if !filepath.IsLocal(relative) {
		return system.DeviceNode{}, system.FieldError("device node", "relative path", relative)
	}
	n := system.DeviceNode{Path: filepath.Join("/dev", relative), HostPath: filepath.Join(c.opts.DevRoot, relative), Kind: kind}
	if c.opts.VFIODeviceRoot != "" && strings.HasPrefix(relative, "vfio/") {
		n.HostPath = filepath.Join(c.opts.VFIODeviceRoot, strings.TrimPrefix(relative, "vfio/"))
	}
	var st unix.Stat_t
	if err := unix.Stat(n.HostPath, &st); err != nil {
		return n, failure("device node", n.HostPath, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return n, system.FieldError(n.HostPath, "device node", "not a character device")
	}
	n.Major, n.Minor = system.Pointer(unix.Major(uint64(st.Rdev))), system.Pointer(unix.Minor(uint64(st.Rdev)))
	return n, nil
}

func (c *Client) DeviceNodes(ctx context.Context, address string) ([]system.DeviceNode, error) {
	driver, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return nil, err
	}
	switch driver {
	case system.NativeDriver:
		f, err := c.function(ctx, address)
		if err != nil {
			return nil, err
		}
		n, err := c.node(f.node, "native")
		if err != nil {
			return nil, err
		}
		// Detect a stale udev node whose name has been reused for another dev_t.
		dev, err := readText(filepath.Join(f.path, "dev"))
		if err != nil {
			return nil, err
		}
		want := strconv.FormatUint(uint64(*n.Major), 10) + ":" + strconv.FormatUint(uint64(*n.Minor), 10)
		if dev != want {
			return nil, &system.Error{Kind: system.NotReady, Target: address, Message: "sysfs dev and device node disagree"}
		}
		return []system.DeviceNode{n}, nil
	case system.VFIODriver:
		group, _, err := c.IOMMUGroup(ctx, address)
		if err != nil {
			return nil, err
		}
		return c.VFIONodes(ctx, group)
	default:
		return nil, &system.Error{Kind: system.Unsupported, Target: address, Message: "no supported device nodes for binding " + driver}
	}
}

const DefaultVFIODevicePath = "/dev/vfio"

// VFIONodes resolves and verifies the control and group nodes for an observed
// IOMMU group. Callers separately revalidate the group's current PCI members.
func (c *Client) VFIONodes(ctx context.Context, group uint32) ([]system.DeviceNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	control, err := c.node("vfio/vfio", "vfio-control")
	if err != nil {
		return nil, err
	}
	n, err := c.node(filepath.Join("vfio", strconv.FormatUint(uint64(group), 10)), "vfio-group")
	if err != nil {
		return nil, err
	}
	return []system.DeviceNode{control, n}, nil
}

// WaitForDeviceNodes is bounded even if the caller supplied no deadline. It
// re-resolves the live PCI/class association after every missing-node result.
func (c *Client) WaitForDeviceNodes(ctx context.Context, address string) ([]system.DeviceNode, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	for {
		nodes, err := c.DeviceNodes(ctx, address)
		if err == nil {
			return nodes, nil
		}
		if !errors.Is(err, system.ErrNotFound) && !errors.Is(err, system.ErrNotReady) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Client) RDSNodes(ctx context.Context) ([]system.DeviceNode, error) {
	paths, err := filepath.Glob(filepath.Join(c.opts.DevRoot, "rblnfs*"))
	if err != nil {
		return nil, err
	}
	nodes := []system.DeviceNode{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := c.node(filepath.Base(p), "rds")
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
