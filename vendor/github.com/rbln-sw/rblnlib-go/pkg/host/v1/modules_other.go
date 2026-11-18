//go:build !linux

package host

import system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"

func unloadModule(string) error { return system.ErrUnsupported }
func unmount(string) error      { return system.ErrUnsupported }
