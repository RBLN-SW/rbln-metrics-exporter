//go:build linux

package host

import "golang.org/x/sys/unix"

func unloadModule(name string) error { return unix.DeleteModule(name, unix.O_NONBLOCK) }
func unmount(path string) error      { return unix.Unmount(path, 0) }
