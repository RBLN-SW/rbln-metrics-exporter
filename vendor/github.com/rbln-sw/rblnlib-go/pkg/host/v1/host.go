// Package host implements the system/v1 contract against the local Linux host.
// Discovery works before the native driver is loaded and while devices are
// bound to VFIO. It never starts SMI, opens NPU nodes, or contacts a daemon.
package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rbln-sw/rblnlib-go/internal/identity"
	"golang.org/x/sys/unix"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// Options roots are absolute paths in this process's mount namespace. SysRoot
// must expose the complete host sysfs (including /sys/devices symlink targets).
// A container using a remote SMD must not enrich it with this host's sysfs.
type Options struct {
	SysRoot, DevRoot, ProcRoot string
	// Overrides support containers which mount only selected host subtrees.
	PCIRoot, PCIDevicesRoot, RebellionsClassRoot, AccelClassRoot string
	VFIODeviceRoot                                               string
	HostRoot                                                     string // optional chroot for host command execution
	HostPID                                                      int    // optional nsenter PID; exclusive with HostRoot
	PCIIDsPath                                                   string // empty disables the optional pci.ids lookup
	LockPath                                                     string // shared between all cooperating host control processes
	CommandTimeout                                               time.Duration
	// Kernel overrides are execution seams for fixture kernels or alternate
	// host transports. Nil callbacks keep the native implementation.
	Kernel *KernelOps
}

type KernelOps struct {
	WriteAttribute func(context.Context, string, string) error
	LoadModule     func(context.Context, string) error
	UnloadModule   func(string) error
	Unmount        func(string) error
	Mounts         func(context.Context, string) ([]Mount, error)
}

type Client struct {
	opts         Options
	products     *PCIIDs
	write        func(context.Context, string, string) error
	unmount      func(string) error
	unload       func(string) error
	loadModuleFn func(context.Context, string) error
	mountsFn     func(context.Context, string) ([]Mount, error)
}

func New(opts Options) (*Client, error) {
	if opts.HostPID < 0 || (opts.HostPID != 0 && opts.HostRoot != "") {
		return nil, &system.Error{Kind: system.InvalidArgument, Message: "choose a chroot or a host namespace PID"}
	}
	if opts.SysRoot == "" {
		opts.SysRoot = "/sys"
	}
	if opts.DevRoot == "" {
		opts.DevRoot = "/dev"
	}
	if opts.ProcRoot == "" {
		opts.ProcRoot = "/proc"
	}
	if opts.LockPath == "" {
		opts.LockPath = "/run/lock/rbln-host-control.lock"
		if opts.HostRoot != "" {
			opts.LockPath = filepath.Join(opts.HostRoot, "run/lock/rbln-host-control.lock")
		}
	}
	if opts.CommandTimeout == 0 {
		opts.CommandTimeout = 10 * time.Second
	}
	if opts.CommandTimeout < 0 {
		return nil, &system.Error{Kind: system.InvalidArgument, Message: "negative command timeout"}
	}
	for _, p := range []string{opts.SysRoot, opts.DevRoot, opts.ProcRoot, opts.LockPath, opts.HostRoot, opts.PCIIDsPath, opts.PCIRoot, opts.PCIDevicesRoot, opts.RebellionsClassRoot, opts.AccelClassRoot, opts.VFIODeviceRoot} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return nil, &system.Error{Kind: system.InvalidArgument, Target: p, Message: "root/path must be absolute and clean"}
		}
	}
	c := &Client{opts: opts, write: writeAttribute, unmount: unmount, unload: unloadModule}
	if k := opts.Kernel; k != nil {
		if k.WriteAttribute != nil {
			c.write = k.WriteAttribute
		}
		if k.UnloadModule != nil {
			c.unload = k.UnloadModule
		}
		if k.Unmount != nil {
			c.unmount = k.Unmount
		}
		c.loadModuleFn = k.LoadModule
		c.mountsFn = k.Mounts
	}
	if opts.PCIIDsPath != "" {
		var err error
		c.products, err = LoadPCIIDs(opts.PCIIDsPath)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Client) pciRoot() string {
	if c.opts.PCIRoot != "" {
		return c.opts.PCIRoot
	}
	return filepath.Join(c.opts.SysRoot, "bus/pci")
}
func (c *Client) devicesRoot() string {
	if c.opts.PCIDevicesRoot != "" {
		return c.opts.PCIDevicesRoot
	}
	return filepath.Join(c.pciRoot(), "devices")
}
func (c *Client) classRoot(class string) string {
	if class == "rebellions" && c.opts.RebellionsClassRoot != "" {
		return c.opts.RebellionsClassRoot
	}
	if class == "accel" && c.opts.AccelClassRoot != "" {
		return c.opts.AccelClassRoot
	}
	return filepath.Join(c.opts.SysRoot, "class", class)
}
func (c *Client) devicePath(address string) (string, error) {
	a, err := system.NormalizePCIAddress(address)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.devicesRoot(), a), nil
}

func failure(op, target string, err error) error {
	if err == nil {
		return nil
	}
	var normalized *system.Error
	if errors.As(err, &normalized) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	kind := system.Unavailable
	switch {
	case errors.Is(err, os.ErrNotExist):
		kind = system.NotFound
	case errors.Is(err, os.ErrPermission):
		kind = system.PermissionDenied
	case errors.Is(err, syscall.EAGAIN):
		kind = system.NotReady
	case errors.Is(err, syscall.EBUSY):
		kind = system.Busy
	}
	return &system.Error{Kind: kind, Op: op, Target: target, Cause: err}
}

// readText must not hand a transient sysfs EAGAIN to Go's netpoller. After
// PCI rebind some attributes become readable without sending a poll event,
// so os.ReadFile can wait forever on the original descriptor. Direct reads
// return NotReady to the caller's reconciliation policy.
func readText(path string) (text string, err error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", failure("read", path, err)
	}
	defer func() { err = errors.Join(err, failure("close", path, unix.Close(fd))) }()
	const maxAttributeBytes = 1 << 20
	data := make([]byte, 0, 4096)
	var chunk [4096]byte
	for {
		n, readErr := unix.Read(fd, chunk[:])
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		if readErr != nil {
			return "", failure("read", path, readErr)
		}
		if n == 0 {
			return strings.TrimSpace(string(data)), nil
		}
		if len(data)+n > maxAttributeBytes {
			return "", system.FieldError(path, "attribute size", "exceeds 1 MiB")
		}
		data = append(data, chunk[:n]...)
	}
}

func readUint(path string, bits int) (uint64, error) {
	s, err := readText(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, system.FieldError(path, "unsigned integer", s)
	}
	return n, nil
}

func readHex(path string, bits int) (uint64, error) {
	s, err := readText(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, bits)
	if err != nil {
		return 0, system.FieldError(path, "hexadecimal integer", s)
	}
	return n, nil
}

func (c *Client) requireDevice(ctx context.Context, address string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := c.devicePath(address)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "", failure("device", address, err)
	}
	vendor, err := readHex(filepath.Join(p, "vendor"), 16)
	if err != nil {
		return "", err
	}
	if vendor != uint64(system.VendorID) {
		return "", &system.Error{Kind: system.Unsupported, Target: address, Message: "not a Rebellions device"}
	}
	class, err := readHex(filepath.Join(p, "class"), 24)
	if err != nil {
		return "", err
	}
	if vendor != uint64(system.VendorID) || class != uint64(system.NPUClass) {
		return "", &system.Error{Kind: system.Unsupported, Target: address, Message: "not a Rebellions NPU"}
	}
	return p, nil
}

// ListDevices preserves successful entries and reports every failed read. It
// never turns an unreadable device into proof that the device disappeared.
func (c *Client) ListDevices(ctx context.Context) (system.Snapshot, error) {
	s := system.Snapshot{Source: "sysfs", ObservedAt: time.Now(), Devices: []system.Device{}}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	entries, err := os.ReadDir(c.devicesRoot())
	if err != nil {
		return s, failure("list PCI devices", c.pciRoot(), err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		if _, err := system.NormalizePCIAddress(entry.Name()); err != nil {
			continue
		}
		d, err := c.GetDevice(ctx, entry.Name())
		if errors.Is(err, system.ErrUnsupported) {
			continue
		}
		if err != nil {
			s.Issues = append(s.Issues, system.Issue{Target: entry.Name(), Field: "device", Err: err})
			continue
		}
		s.Devices = append(s.Devices, d)
	}
	return s, nil
}

func (c *Client) GetDevice(ctx context.Context, address string) (system.Device, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return system.Device{}, err
	}
	d := system.Device{PCIAddress: filepath.Base(p), VendorID: system.VendorID, Class: system.NPUClass, State: system.StateUnknown}
	issue := func(field string, err error) {
		if err != nil {
			d.Issues = append(d.Issues, system.Issue{Target: d.PCIAddress, Field: field, Err: err})
		}
	}
	id, err := readHex(filepath.Join(p, "device"), 16)
	if err != nil {
		return d, err
	}
	d.DeviceID = uint16(id)
	d.DeviceIDKnown = true
	d.ProductName = c.products.Lookup(d.DeviceID)
	d.Driver, err = c.CurrentDriver(ctx, address)
	d.DriverKnown = err == nil
	issue("driver", err)
	if override, e := readText(filepath.Join(p, "driver_override")); e == nil {
		if override == "(null)" {
			override = ""
		}
		d.DriverOverride = &override
	} else {
		issue("driver_override", e)
	}
	sriov, err := c.ReadSRIOV(ctx, address)
	if err == nil {
		d.SRIOV = &sriov
		d.EnabledVFs = system.Pointer(sriov.EnabledVFs)
		d.FunctionKind = sriov.Kind
		d.ParentPCIAddress = sriov.ParentPCIAddress
	}
	issue("sriov", err)
	d.Topology, err = c.ReadTopology(ctx, address)
	issue("topology", err)
	if d.DriverKnown && d.Driver == system.NativeDriver {
		entry, e := c.function(ctx, address)
		issue("function", e)
		if e == nil {
			d.Name = entry.name
			if d.FunctionKind == system.PhysicalFunction {
				if index, ok := identity.NativePFOrdinal(d.Name); ok {
					d.NativeIndex = &index
				}
			}
			for _, f := range []struct {
				attr, field string
				dst         *string
			}{
				{"card_name", "product_name", &d.ProductName}, {"unique_id", "uuid", &d.UUID}, {"serial_id", "serial", &d.Serial},
				{"kernel_version", "driver_version", &d.Versions.Driver}, {"fw_ver", "firmware_version", &d.Versions.Firmware}, {"smc_ver", "smc_version", &d.Versions.SMC},
			} {
				v, e := readText(filepath.Join(entry.path, f.attr))
				if e == nil && v != "" {
					*f.dst = v
				} else {
					issue(f.field, attributeError(f.field, e))
				}
			}
			if v, e := readUint(filepath.Join(entry.path, "dram_total"), 64); e == nil {
				d.MemoryTotalBytes = &v
			} else {
				issue("memory_total_bytes", attributeError("dram_total", e))
			}
		}
		d.State, err = c.ReadState(ctx, address)
		issue("state", err)
	}
	if d.DriverKnown && d.Driver != "" {
		d.Nodes, err = c.DeviceNodes(ctx, address)
		issue("device_nodes", err)
	}
	return d, nil
}

func attributeError(attr string, err error) error {
	if errors.Is(err, system.ErrNotFound) {
		return &system.Error{Kind: system.Unsupported, Op: attr, Cause: err}
	}
	return err
}

// CurrentDriver distinguishes a missing driver link (unbound) from a missing
// device or a dangling link (failed observation).
func (c *Client) CurrentDriver(ctx context.Context, address string) (string, error) {
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return "", err
	}
	link := filepath.Join(p, "driver")
	target, err := os.Readlink(link)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Stat(p); e != nil {
			return "", failure("device", address, e)
		}
		return "", nil
	}
	if err != nil {
		return "", failure("driver", address, err)
	}
	if _, err := os.Stat(link); err != nil {
		return "", failure("driver", address, err)
	}
	return filepath.Base(target), nil
}

func writeAttribute(ctx context.Context, path, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0) // never create/truncate sysfs
	if err != nil {
		return failure("write", path, err)
	}
	_, writeErr := f.WriteString(value + "\n")
	closeErr := f.Close()
	return failure("write", path, errors.Join(writeErr, closeErr))
}

var _ system.Inventory = (*Client)(nil)
var _ system.DeviceReader = (*Client)(nil)
var _ system.StateReader = (*Client)(nil)
var _ system.NodeResolver = (*Client)(nil)
