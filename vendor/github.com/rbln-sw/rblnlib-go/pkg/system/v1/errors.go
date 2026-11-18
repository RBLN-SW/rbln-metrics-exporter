package system

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind is stable across sysfs, CLI and RPC backends.
type ErrorKind string

const (
	Unsupported      ErrorKind = "unsupported"
	NotFound         ErrorKind = "not_found"
	NotReady         ErrorKind = "not_ready"
	Busy             ErrorKind = "busy"
	PermissionDenied ErrorKind = "permission_denied"
	Unavailable      ErrorKind = "unavailable"
	InvalidArgument  ErrorKind = "invalid_argument"
	InvalidData      ErrorKind = "invalid_data"
	Conflict         ErrorKind = "conflict"
	Indeterminate    ErrorKind = "indeterminate"
	Internal         ErrorKind = "internal"
	NotReported      ErrorKind = "not_reported" // an in-scope metric omitted by a successful sample response
)

// Error retains the backend cause for diagnosis. Code/Errno/Category are
// diagnostic details; callers branch on Kind, not these transport values.
type Error struct {
	Kind                ErrorKind
	Op, Target, Message string
	Code, Errno         int32
	Category            string
	Cause               error
}

func (e *Error) Error() string {
	s := string(e.Kind)
	if prefix := strings.TrimSpace(e.Op + " " + e.Target); prefix != "" {
		s = prefix + ": " + s
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.Cause }
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e.Kind == t.Kind
}

var (
	ErrNotReported = &Error{Kind: NotReported}
	// ErrDeviceNotFound identifies an absent host PCI function specifically.
	// The error also matches ErrNotFound and retains the OS cause.
	ErrDeviceNotFound   = errors.New("PCI function not found")
	ErrUnsupported      = &Error{Kind: Unsupported}
	ErrNotFound         = &Error{Kind: NotFound}
	ErrNotReady         = &Error{Kind: NotReady}
	ErrBusy             = &Error{Kind: Busy}
	ErrPermissionDenied = &Error{Kind: PermissionDenied}
	ErrUnavailable      = &Error{Kind: Unavailable}
	ErrInvalidArgument  = &Error{Kind: InvalidArgument}
	ErrInvalidData      = &Error{Kind: InvalidData}
	ErrConflict         = &Error{Kind: Conflict}
	ErrIndeterminate    = &Error{Kind: Indeterminate}
)

// Issue identifies an unavailable entity or field in a partial observation.
type Issue struct {
	Target, Field string
	Err           error
}

func IssuesError(issues []Issue) error {
	errs := make([]error, 0, len(issues))
	for _, i := range issues {
		if i.Err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", i.Target, i.Field, i.Err))
		}
	}
	return errors.Join(errs...)
}
