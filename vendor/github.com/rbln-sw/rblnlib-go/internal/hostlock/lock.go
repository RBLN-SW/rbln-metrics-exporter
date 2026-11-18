// Package hostlock coordinates cooperating processes through an advisory lock.
package hostlock

import (
	"context"
	"errors"
	"os"
	"time"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"golang.org/x/sys/unix"
)

func Acquire(ctx context.Context, path string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, lockError(path, err)
	}
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() error { return lockError(path, errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close())) }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return nil, lockError(path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func lockError(path string, err error) error {
	if err == nil {
		return nil
	}
	kind := system.Unavailable
	switch {
	case errors.Is(err, os.ErrNotExist):
		kind = system.NotFound
	case errors.Is(err, os.ErrPermission):
		kind = system.PermissionDenied
	}
	return &system.Error{Kind: kind, Op: "host lock", Target: path, Cause: err}
}
