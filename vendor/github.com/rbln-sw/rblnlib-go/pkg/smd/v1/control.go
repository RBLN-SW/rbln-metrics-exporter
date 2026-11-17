package smd

import (
	"context"
	"errors"
	"math"
	"time"

	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func targets(names []string) *pb.VfTargets {
	refs := make([]*pb.DeviceRef, len(names))
	for i, name := range names {
		refs[i] = &pb.DeviceRef{Name: name}
	}
	return &pb.VfTargets{Selector: &pb.VfTargets_Devices{Devices: &pb.DeviceRefList{Devices: refs}}}
}

func commandContext(ctx context.Context, count int, opts system.CommandOptions) (context.Context, context.CancelFunc, uint32, error) {
	if opts.Timeout < 0 || opts.PerTargetTimeout < 0 {
		return nil, nil, 0, &system.Error{Kind: system.InvalidArgument, Message: "negative command timeout"}
	}
	perTarget := opts.PerTargetTimeout
	if perTarget == 0 {
		perTarget = 300 * time.Second
	}
	seconds := math.Ceil(perTarget.Seconds())
	if seconds > math.MaxUint32 || count <= 0 {
		return nil, nil, 0, &system.Error{Kind: system.InvalidArgument, Message: "command timeout or target count out of range"}
	}
	// The wire accepts whole seconds. Budget the same rounded duration that
	// the server receives rather than a shorter sub-second client estimate.
	perTarget = time.Duration(seconds) * time.Second
	if int64(count) > (math.MaxInt64-int64(30*time.Second))/int64(perTarget) {
		return nil, nil, 0, &system.Error{Kind: system.InvalidArgument, Message: "command timeout overflow"}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = perTarget*time.Duration(count) + 30*time.Second
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	return c, cancel, uint32(seconds), nil
}

func commandError(op string, err error) (system.CommandResult, error) {
	r := system.CommandResult{}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Internal, codes.Unknown, codes.DataLoss, codes.ResourceExhausted:
		r.Indeterminate = true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		r.Indeterminate = true
	}
	err = rpcError(op, "", err)
	if r.Indeterminate {
		err = errors.Join(err, system.ErrIndeterminate)
	}
	return r, err
}

func (c *Client) outcomes(op, audit string, names []string, raw []*pb.EntityStatus) (system.CommandResult, error) {
	return c.outcomesForVersion(c.opts.ServerVersion, op, audit, names, raw)
}

func (c *Client) outcomesForVersion(version, op, audit string, names []string, raw []*pb.EntityStatus) (system.CommandResult, error) {
	r := system.CommandResult{AuditID: audit, Outcomes: []system.Outcome{}}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	for _, item := range raw {
		if !wanted[item.GetRef()] {
			r.Indeterminate = true
			return r, errors.Join(system.ErrIndeterminate, system.FieldError(item.GetRef(), op, "unexpected/duplicate result"))
		}
		delete(wanted, item.GetRef())
		err := detailError(op, item.GetRef(), item.GetError())
		detail := item.GetError()
		// Narrow compatibility quirk; EPERM in every other combination remains
		// PermissionDenied. Never guess this from the human-readable message.
		if (op == "EnableVFs" || op == "DisableVFs") && version == "3.5.1" && detail.GetCode() == 7002 && detail.GetCategory() == pb.ErrorCategory_ERROR_CATEGORY_CONFIG && detail.GetOsErrno() == 1 {
			err = &system.Error{Kind: system.NotReady, Op: op, Target: item.GetRef(), Code: detail.GetCode(), Errno: detail.GetOsErrno(), Category: detail.GetCategory().String(), Message: "SMD 3.5.1 PF reinitialization: " + detail.GetMessage()}
		}
		r.Outcomes = append(r.Outcomes, system.Outcome{Target: item.GetRef(), Err: err})
	}
	if len(wanted) != 0 {
		r.Indeterminate = true
		for _, n := range names {
			if wanted[n] {
				r.Outcomes = append(r.Outcomes, system.Outcome{Target: n, Err: &system.Error{Kind: system.Indeterminate, Op: op, Target: n, Message: "missing target result"}})
			}
		}
	}
	return r, r.Err()
}

func (c *Client) EnableVFs(ctx context.Context, names []string, count uint32, opts system.CommandOptions) (system.CommandResult, error) {
	if err := system.ValidTargets(names); err != nil {
		return system.CommandResult{}, err
	}
	if count != 1 && count != 2 && count != 4 {
		return system.CommandResult{}, &system.Error{Kind: system.Unsupported, Message: "SMD v1 supports 1, 2 or 4 VFs; hardware support must also be checked"}
	}
	ctx, cancel, seconds, err := commandContext(ctx, len(names), opts)
	if err != nil {
		return system.CommandResult{}, err
	}
	defer cancel()
	if err := ctx.Err(); err != nil {
		return system.CommandResult{}, err
	}
	version := c.compatibilityVersion(ctx)
	if err := ctx.Err(); err != nil {
		return system.CommandResult{}, err
	}
	r, err := c.control.EnableVfs(ctx, &pb.EnableVfsRequest{Targets: targets(names), NumVfs: count, TimeoutSeconds: seconds, RequestId: opts.RequestID, IdempotencyKey: opts.IdempotencyKey})
	if err != nil {
		return commandError("EnableVFs", err)
	}
	return c.outcomesForVersion(version, "EnableVFs", r.GetAuditId(), names, r.GetResults())
}

func (c *Client) DisableVFs(ctx context.Context, names []string, opts system.CommandOptions) (system.CommandResult, error) {
	if err := system.ValidTargets(names); err != nil {
		return system.CommandResult{}, err
	}
	ctx, cancel, _, err := commandContext(ctx, len(names), opts)
	if err != nil {
		return system.CommandResult{}, err
	}
	defer cancel()
	if err := ctx.Err(); err != nil {
		return system.CommandResult{}, err
	}
	version := c.compatibilityVersion(ctx)
	if err := ctx.Err(); err != nil {
		return system.CommandResult{}, err
	}
	r, err := c.control.DisableVfs(ctx, &pb.DisableVfsRequest{Targets: targets(names), RequestId: opts.RequestID, IdempotencyKey: opts.IdempotencyKey})
	if err != nil {
		return commandError("DisableVFs", err)
	}
	return c.outcomesForVersion(version, "DisableVFs", r.GetAuditId(), names, r.GetResults())
}
