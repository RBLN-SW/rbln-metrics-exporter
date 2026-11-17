package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/rbln-sw/rblnlib-go/internal/hostlock"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// Bind changes only the explicitly named NPU. Callers must first quiesce all
// workloads and coordinate with SMD/other writers; this advisory lock cannot
// serialize daemon RPCs or non-cooperating processes. Every write is followed
// by a fresh observation. No automatic rollback or replay is attempted.
func (c *Client) Bind(ctx context.Context, request system.BindingRequest) (err error) {
	mutated := false
	defer func() {
		if mutated && err != nil {
			err = errors.Join(system.ErrIndeterminate, err)
		}
	}()
	if request.Driver != system.NativeDriver && request.Driver != system.VFIODriver {
		return &system.Error{Kind: system.InvalidArgument, Target: request.Driver, Message: "unsupported NPU binding target"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	release, err := hostlock.Acquire(ctx, c.opts.LockPath)
	if err != nil {
		return failure("host lock", request.PCIAddress, err)
	}
	defer func() { err = errors.Join(err, release()) }()
	p, err := c.requireDevice(ctx, request.PCIAddress)
	if err != nil {
		return err
	}
	address := filepath.Base(p)
	driver, err := c.checkBinding(ctx, address, request.ExpectedDriver)
	if err != nil {
		return err
	}
	if request.Driver == system.VFIODriver {
		info, err := c.ReadSRIOV(ctx, address)
		if err != nil {
			return err
		}
		if info.Kind == system.PhysicalFunction && info.EnabledVFs > 0 {
			return &system.Error{Kind: system.Conflict, Target: address, Message: "VF-hosting PF must remain native-bound"}
		}
	}
	if driver == request.Driver {
		mutated = driver == system.NativeDriver
		return c.finishBinding(ctx, p, driver)
	}
	targetPath := filepath.Join(c.pciRoot(), "drivers", request.Driver)
	if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
		mutated = true
		if err := c.LoadModule(ctx, request.Driver); err != nil {
			return err
		}
	} else if err != nil {
		return failure("target driver", request.Driver, err)
	}
	if _, err := os.Stat(filepath.Join(targetPath, "bind")); err != nil {
		return failure("bind attribute", request.Driver, err)
	}
	// Loading a module may bind an unbound function through autoprobe.
	driver, err = c.CurrentDriver(ctx, address)
	if err != nil {
		return err
	}
	if driver == request.Driver {
		mutated = true
		return c.finishBinding(ctx, p, driver)
	}
	if request.ExpectedDriver != nil && driver != *request.ExpectedDriver {
		return &system.Error{Kind: system.Conflict, Target: address, Message: "binding changed while preparing"}
	}
	mutated = true
	if err := c.setOverride(ctx, p, request.Driver); err != nil {
		return err
	}
	if driver != "" {
		if err := c.write(ctx, filepath.Join(p, "driver", "unbind"), address); err != nil {
			return err
		}
		if actual, err := c.CurrentDriver(ctx, address); err != nil {
			return err
		} else if actual != "" {
			return &system.Error{Kind: system.NotReady, Target: address, Message: "driver remains bound after unbind"}
		}
	}
	if err := c.write(ctx, filepath.Join(targetPath, "bind"), address); err != nil {
		return err
	}
	actual, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return err
	}
	if actual != request.Driver {
		return &system.Error{Kind: system.NotReady, Target: address, Message: "target binding has not completed"}
	}
	return c.finishBinding(ctx, p, actual)
}

func (c *Client) finishBinding(ctx context.Context, p, driver string) error {
	if driver != system.NativeDriver {
		return nil
	}
	return c.setOverride(ctx, p, "")
}

func (c *Client) checkBinding(ctx context.Context, address string, expected *string) (string, error) {
	driver, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return "", err
	}
	if expected != nil && driver != *expected {
		return "", &system.Error{Kind: system.Conflict, Target: address, Message: "binding differs from expected driver"}
	}
	return driver, nil
}

// Unbind inhibits autoprobe with driver_override=none. A subsequent Bind
// selects the target explicitly; Reprobe clears the override. Driver is unused
// for Unbind; ExpectedDriver supplies the optional precondition.
func (c *Client) Unbind(ctx context.Context, request system.BindingRequest) (err error) {
	mutated := false
	defer func() {
		if mutated && err != nil {
			err = errors.Join(system.ErrIndeterminate, err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	release, err := hostlock.Acquire(ctx, c.opts.LockPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	p, err := c.requireDevice(ctx, request.PCIAddress)
	if err != nil {
		return err
	}
	address := filepath.Base(p)
	driver, err := c.checkBinding(ctx, address, request.ExpectedDriver)
	if err != nil {
		return err
	}
	info, err := c.ReadSRIOV(ctx, address)
	if err != nil {
		return err
	}
	if info.Kind == system.PhysicalFunction && info.EnabledVFs > 0 {
		return &system.Error{Kind: system.Conflict, Target: address, Message: "disable VFs before unbinding their PF"}
	}
	mutated = true
	if !request.AllowAutoprobeAfterUnbind {
		if err := c.setOverride(ctx, p, "none"); err != nil {
			return err
		}
	}
	if driver != "" {
		if err := c.write(ctx, filepath.Join(p, "driver", "unbind"), address); err != nil {
			return err
		}
	}
	current, err := c.CurrentDriver(ctx, address)
	if err != nil {
		return err
	}
	if current != "" {
		return &system.Error{Kind: system.NotReady, Target: address, Message: "driver remains bound"}
	}
	if request.AllowAutoprobeAfterUnbind {
		return c.setOverride(ctx, p, "")
	}
	return nil
}

func (c *Client) setOverride(ctx context.Context, p, value string) error {
	path := filepath.Join(p, "driver_override")
	current, err := readText(path)
	if err != nil {
		return err
	}
	if current == "(null)" {
		current = ""
	}
	if current == value {
		return nil
	}
	if err := c.write(ctx, path, value); err != nil {
		return err
	}
	current, err = readText(path)
	if err != nil {
		return err
	}
	if current == "(null)" {
		current = ""
	}
	if current != value {
		return &system.Error{Kind: system.NotReady, Target: p, Message: "driver override write not observed"}
	}
	return nil
}

// Reprobe is an explicit opt-in to normal kernel driver matching. It does not
// promise which driver will bind. GetDevice can observe the resulting binding.
func (c *Client) Reprobe(ctx context.Context, address string) (err error) {
	mutated := false
	defer func() {
		if mutated && err != nil {
			err = errors.Join(system.ErrIndeterminate, err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, c.opts.CommandTimeout)
	defer cancel()
	release, err := hostlock.Acquire(ctx, c.opts.LockPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	p, err := c.requireDevice(ctx, address)
	if err != nil {
		return err
	}
	mutated = true
	if err := c.setOverride(ctx, p, ""); err != nil {
		return err
	}
	return c.write(ctx, filepath.Join(c.pciRoot(), "drivers_probe"), filepath.Base(p))
}

var _ system.Binder = (*Client)(nil)
