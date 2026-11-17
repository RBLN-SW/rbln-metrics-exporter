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

// Native metadata can be read with just the driver class mounted, without a
// complete PCI inventory. No function number (including rbln0) is required.
func (c *Client) nativeAttribute(ctx context.Context, attribute string, skipUnreadable bool) (string, error) {
	for _, class := range []string{"accel", "rebellions"} {
		root := c.classRoot(class)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", failure("native metadata", root, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if entry.Name() == "rsd" || strings.HasPrefix(entry.Name(), "rblnfs") {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if class == "accel" {
				vendor, err := readHex(filepath.Join(dir, "device/vendor"), 16)
				if err != nil || vendor != uint64(system.VendorID) {
					continue
				}
			}
			value, err := readText(filepath.Join(dir, attribute))
			if errors.Is(err, system.ErrNotFound) || (skipUnreadable && err != nil) {
				continue
			}
			if err != nil {
				return "", err
			}
			if value != "" {
				return value, nil
			}
		}
	}
	return "", nil
}

func (c *Client) ReadCardName(ctx context.Context) (string, error) {
	return c.nativeAttribute(ctx, "card_name", true)
}
func (c *Client) ReadDriverVersion(ctx context.Context) (string, error) {
	return c.nativeAttribute(ctx, "kernel_version", false)
}

// ReadNamedState observes a driver function name obtained from an inventory.
// A recognized accel entry takes precedence over the legacy class. Unknown
// numeric states are observations; malformed/missing attributes are errors.
func (c *Client) ReadNamedState(ctx context.Context, name string) (system.StateObservation, error) {
	unknown := system.StateObservation{State: system.StateUnknown}
	if !safeName(name) {
		return unknown, &system.Error{Kind: system.InvalidArgument, Target: name}
	}
	if err := ctx.Err(); err != nil {
		return unknown, err
	}
	root := c.classRoot("accel")
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return unknown, failure("accel class", root, err)
	}
	path := filepath.Join(c.classRoot("rebellions"), name)
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		n, err := readText(filepath.Join(dir, "name"))
		if err != nil || n != name {
			continue
		}
		vendor, err := readHex(filepath.Join(dir, "device/vendor"), 16)
		if err != nil {
			return unknown, err
		}
		if vendor != uint64(system.VendorID) {
			continue
		}
		path = dir
		break
	}
	raw, err := readText(filepath.Join(path, "status"))
	if err != nil {
		return unknown, attributeError("state", err)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return unknown, system.FieldError(name, "state", raw)
	}
	states := map[int64]system.State{0: system.StateReady, 1: system.StateBusy, 2: system.StateProbing, 3: system.StateInit, 4: system.StateFault, 5: system.StateFinish}
	s := states[n]
	if s == "" {
		s = system.StateUnknown
	}
	return system.StateObservation{State: s, RawCode: &n}, nil
}
