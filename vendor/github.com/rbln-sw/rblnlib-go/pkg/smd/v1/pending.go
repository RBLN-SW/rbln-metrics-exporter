package smd

import (
	"strings"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsPending requires every outcome in an aggregate to be a known transient
// device/serialization condition. Unknown completion and permission failures
// are not hidden by another target being busy.
func IsPending(err error) bool {
	if err == nil {
		return false
	}
	switch e := err.(type) {
	case *system.Error:
		if _, ok := status.FromError(e.Cause); e.Cause != nil && ok {
			return operationInProgress(e.Cause)
		}
		return e.Kind == system.NotReady || e.Kind == system.Busy
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !IsPending(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return IsPending(e.Unwrap())
	default:
		return operationInProgress(err)
	}
}

func operationInProgress(err error) bool {
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.Aborted && (s.Message() == "another operation is in progress" || strings.HasPrefix(s.Message(), "another operation is in progress on this device"))
}
