package host

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rbln-sw/rblnlib-go/internal/hostlock"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// WaitForNode validates a returned canonical /dev path and waits for a char
// device. It uses this client's DevRoot, so a host mount prefix cannot leak
// into the container's intended path.
func (c *Client) WaitForNode(ctx context.Context, node system.DeviceNode) (system.DeviceNode, error) {
	relative, err := filepath.Rel("/dev", node.Path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." || filepath.Clean(node.Path) != node.Path {
		return system.DeviceNode{}, &system.Error{Kind: system.InvalidArgument, Target: node.Path}
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	for {
		n, err := c.node(relative, node.Kind)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, system.ErrNotFound) {
			return n, err
		}
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// RDSBindings returns exactly the PCI functions bound to rblnfs. They are NVMe
// devices, not NPUs, so this discovery is separate from ListDevices.
func (c *Client) RDSBindings(ctx context.Context) ([]string, error) {
	root := filepath.Join(c.pciRoot(), "drivers", "rblnfs")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, failure("RDS bindings", root, err)
	}
	out := []string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a, err := system.NormalizePCIAddress(entry.Name())
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name())); err != nil {
			return nil, failure("RDS binding", a, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// RestoreRDS releases one explicitly selected rblnfs function and requests
// normal kernel reprobe. It does not unload rblnfs or select workload policy.
func (c *Client) RestoreRDS(ctx context.Context, address string) (err error) {
	mutated := false
	defer func() {
		if mutated && err != nil {
			err = errors.Join(system.ErrIndeterminate, err)
		}
	}()
	p, err := c.devicePath(address)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	release, err := hostlock.Acquire(ctx, c.opts.LockPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	driver, err := filepath.EvalSymlinks(filepath.Join(p, "driver"))
	if err != nil {
		return failure("RDS driver", address, err)
	}
	if filepath.Base(driver) != "rblnfs" {
		return &system.Error{Kind: system.Conflict, Target: address, Message: "function is not bound to rblnfs"}
	}
	mutated = true
	if err := c.write(ctx, filepath.Join(driver, "unbind"), filepath.Base(p)); err != nil {
		return err
	}
	if err := c.setOverride(ctx, p, ""); err != nil {
		return err
	}
	return c.write(ctx, filepath.Join(c.pciRoot(), "drivers_probe"), filepath.Base(p))
}

type Mount struct {
	ID   int
	Path string
}

// RootfsMounts describes mounts in the configured procfs mount namespace.
func (c *Client) RootfsMounts(ctx context.Context, root string) ([]Mount, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, &system.Error{Kind: system.InvalidArgument, Target: root, Message: "explicit non-root mount subtree required"}
	}
	if c.mountsFn != nil {
		return c.mountsFn(ctx, root)
	}
	f, err := os.Open(filepath.Join(c.opts.ProcRoot, "self", "mountinfo"))
	if err != nil {
		return nil, failure("mountinfo", c.opts.ProcRoot, err)
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 1024*1024)
	out := []Mount{}
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields := strings.Fields(s.Text())
		if len(fields) < 10 {
			return nil, system.FieldError("mountinfo", "entry", s.Text())
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, system.FieldError("mountinfo", "mount ID", fields[0])
		}
		p := decode.Replace(fields[4])
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			out = append(out, Mount{ID: id, Path: p})
		}
	}
	if err := s.Err(); err != nil {
		return nil, failure("mountinfo", c.opts.ProcRoot, err)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].Path) > len(out[j].Path) })
	return out, nil
}

// UnmountRootfs removes mounts deepest-first and requires observable progress.
// It never force-unmounts. ProcRoot must describe this process's mount namespace;
// inspecting a host namespace and unmounting in a container would be incorrect.
func (c *Client) UnmountRootfs(ctx context.Context, root string) error {
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	mounts, err := c.RootfsMounts(ctx, root)
	if err != nil {
		return err
	}
	for len(mounts) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		sort.SliceStable(mounts, func(i, j int) bool { return len(mounts[i].Path) > len(mounts[j].Path) })
		if err := c.unmount(mounts[0].Path); err != nil {
			return failure("unmount", mounts[0].Path, err)
		}
		after, err := c.RootfsMounts(ctx, root)
		if err != nil {
			return err
		}
		if len(after) >= len(mounts) {
			return &system.Error{Kind: system.NotReady, Target: root, Message: "unmount made no progress or concurrent remount observed"}
		}
		mounts = after
	}
	return nil
}
