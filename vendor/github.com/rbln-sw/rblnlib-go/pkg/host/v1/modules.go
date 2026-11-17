package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rbln-sw/rblnlib-go/internal/command"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

var moduleName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

type Module struct {
	Name           string
	Loaded         bool
	State, Version string
	RefCount       *uint64
}

func (c *Client) Module(ctx context.Context, name string) (Module, error) {
	m := Module{Name: name}
	if err := ctx.Err(); err != nil {
		return m, err
	}
	if !moduleName.MatchString(name) {
		return m, &system.Error{Kind: system.InvalidArgument, Target: name}
	}
	p := filepath.Join(c.opts.SysRoot, "module", strings.ReplaceAll(name, "-", "_"))
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return m, nil
	} else if err != nil {
		return m, failure("module", name, err)
	}
	m.Loaded = true
	var errs []error
	for _, attr := range []struct {
		name string
		dst  *string
	}{{"version", &m.Version}, {"initstate", &m.State}} {
		value, err := readText(filepath.Join(p, attr.name))
		if err == nil {
			*attr.dst = value
		} else if !errors.Is(err, system.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if n, err := readUint(filepath.Join(p, "refcnt"), 64); err == nil {
		m.RefCount = &n
	} else if !errors.Is(err, system.ErrNotFound) {
		errs = append(errs, err)
	}
	return m, errors.Join(errs...)
}

func (c *Client) run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	if c.opts.HostRoot != "" {
		args = append([]string{c.opts.HostRoot, binary}, args...)
		binary = "chroot"
	} else if c.opts.HostPID != 0 {
		args = append([]string{"--target", strconv.Itoa(c.opts.HostPID), "--mount", "--uts", "--ipc", "--", "env", "TMPDIR=/tmp", binary}, args...)
		binary = "nsenter"
	}
	return command.Run(ctx, binary, args...)
}

func (c *Client) LoadModule(ctx context.Context, name string) error {
	if !moduleName.MatchString(name) {
		return &system.Error{Kind: system.InvalidArgument, Target: name}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.loadModuleFn != nil {
		return c.loadModuleFn(ctx, name)
	}
	_, err := c.run(ctx, "modprobe", name)
	return err
}

// UnloadModule never forces removal. Busy holders are reported immediately.
func (c *Client) UnloadModule(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !moduleName.MatchString(name) {
		return &system.Error{Kind: system.InvalidArgument, Target: name}
	}
	err := c.unload(strings.ReplaceAll(name, "-", "_"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return failure("unload module", name, err)
}

// ProbeHostDriver executes the host SMI version probe with a bounded deadline.
// A successful empty response is invalid, not evidence of an installed driver.
func (c *Client) ProbeHostDriver(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rbln-smi", "--version")
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", system.FieldError("rbln-smi", "version", "empty")
	}
	return version, nil
}

// FindManagementLibrary locates the ML library in an installed driver root.
// It makes no promise that the discovered library satisfies a given C ABI.
func FindManagementLibrary(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", &system.Error{Kind: system.InvalidArgument, Target: root}
	}
	for _, dir := range []string{"", "usr/lib", "usr/lib64", "usr/lib/x86_64-linux-gnu", "usr/lib/aarch64-linux-gnu", "usr/local/lib", "lib", "lib64", "lib/x86_64-linux-gnu", "lib/aarch64-linux-gnu"} {
		p := filepath.Join(root, dir, "librbln-ml.so")
		resolved, err := filepath.EvalSymlinks(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", failure("management library", p, err)
		}
		if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() {
			return resolved, nil
		}
	}
	return "", &system.Error{Kind: system.NotFound, Target: root, Message: "librbln-ml.so not found"}
}
