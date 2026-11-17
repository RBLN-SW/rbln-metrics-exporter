// Package smd implements the system/v1 contract over the SMD public gRPC API.
// Wire types stay internal. Only explicit UNIMPLEMENTED on a query permits
// legacy fallback. Commands have no transport fallback or automatic replay.
package smd

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	legacy "github.com/rbln-sw/rblnlib-go/internal/proto/smdlegacy"
	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

type Options struct {
	Endpoint string
	// TLS=nil selects plaintext explicitly configured by the deployment. TLS
	// credentials/authorization failures never trigger plaintext fallback.
	TLS           *tls.Config
	QueryTimeout  time.Duration
	DisableLegacy bool
	// ServerVersion enables only documented, release-specific semantic fixes.
	// Unknown versions do not reinterpret permission errors as transient.
	ServerVersion string
	// ContextDialer supports unix sockets and in-process contract tests.
	ContextDialer func(context.Context, string) (net.Conn, error)
}

type Client struct {
	conn      *grpc.ClientConn
	opts      Options
	inventory pb.InventoryServiceClient
	topology  pb.TopologyServiceClient
	metrics   pb.MetricServiceClient
	control   pb.ControlServiceClient
	config    pb.ConfigServiceClient
	legacy    legacy.RBLNServicesClient
}

func New(opts Options) (*Client, error) {
	if opts.Endpoint == "" {
		return nil, &system.Error{Kind: system.InvalidArgument, Message: "SMD endpoint required"}
	}
	if opts.QueryTimeout == 0 {
		opts.QueryTimeout = 30 * time.Second
	}
	if opts.QueryTimeout < 0 {
		return nil, &system.Error{Kind: system.InvalidArgument, Message: "negative query timeout"}
	}
	creds := insecure.NewCredentials()
	if opts.TLS != nil {
		cfg := opts.TLS.Clone()
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
		creds = credentials.NewTLS(cfg)
	}
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds), grpc.WithDisableRetry()}
	if opts.ContextDialer != nil {
		dialOpts = append(dialOpts, grpc.WithContextDialer(opts.ContextDialer))
	}
	conn, err := grpc.NewClient(opts.Endpoint, dialOpts...)
	if err != nil {
		return nil, rpcError("connect", opts.Endpoint, err)
	}
	return &Client{conn: conn, opts: opts, inventory: pb.NewInventoryServiceClient(conn), topology: pb.NewTopologyServiceClient(conn), metrics: pb.NewMetricServiceClient(conn), control: pb.NewControlServiceClient(conn), config: pb.NewConfigServiceClient(conn), legacy: legacy.NewRBLNServicesClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func rpcError(op, target string, err error) error {
	if err == nil {
		return nil
	}
	var normalized *system.Error
	if errors.As(err, &normalized) {
		return err
	}
	kind := system.Internal
	switch status.Code(err) {
	case codes.Unimplemented:
		kind = system.Unsupported
	case codes.NotFound:
		kind = system.NotFound
	case codes.FailedPrecondition:
		kind = system.NotReady
	case codes.Aborted, codes.ResourceExhausted:
		kind = system.Busy
	case codes.PermissionDenied, codes.Unauthenticated:
		kind = system.PermissionDenied
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		kind = system.Unavailable
	case codes.InvalidArgument, codes.OutOfRange:
		kind = system.InvalidArgument
	case codes.AlreadyExists:
		kind = system.Conflict
	case codes.DataLoss:
		kind = system.InvalidData
	}
	cause := err
	if status.Code(err) == codes.DeadlineExceeded {
		cause = errors.Join(err, context.DeadlineExceeded)
	}
	if status.Code(err) == codes.Canceled {
		cause = errors.Join(err, context.Canceled)
	}
	return &system.Error{Kind: kind, Op: op, Target: target, Cause: cause}
}

func detailError(op, target string, d *pb.ErrorDetail) error {
	if d == nil || (d.Code == 0 && d.Message == "" && d.OsErrno == 0 && d.Category == pb.ErrorCategory_ERROR_CATEGORY_UNSPECIFIED) {
		return nil
	}
	kind := system.Internal
	switch d.OsErrno {
	case 1, 13:
		kind = system.PermissionDenied
	case 2, 19:
		kind = system.NotFound
	case 11:
		kind = system.NotReady
	case 16:
		kind = system.Busy
	case 22:
		kind = system.InvalidArgument
	case 95:
		kind = system.Unsupported
	}
	return &system.Error{Kind: kind, Op: op, Target: target, Message: d.Message, Code: d.Code, Errno: d.OsErrno, Category: d.Category.String()}
}

func unreachable(op string, values []*pb.EntityStatus) []system.Issue {
	out := make([]system.Issue, 0, len(values))
	for _, value := range values {
		err := detailError(op, value.GetRef(), value.GetError())
		if err == nil {
			err = &system.Error{Kind: system.Unavailable, Op: op, Target: value.GetRef(), Message: "entity listed as unreachable"}
		}
		out = append(out, system.Issue{Target: value.GetRef(), Field: op, Err: err})
	}
	return out
}

func nextPage(seen map[string]bool, token string) error {
	if token == "" {
		return nil
	}
	if seen[token] {
		return &system.Error{Kind: system.InvalidData, Message: "repeated pagination token"}
	}
	seen[token] = true
	return nil
}

func state(raw pb.DeviceState) system.State {
	switch raw {
	case pb.DeviceState_DEVICE_STATE_READY:
		return system.StateReady
	case pb.DeviceState_DEVICE_STATE_INIT:
		return system.StateInit
	case pb.DeviceState_DEVICE_STATE_BUSY:
		return system.StateBusy
	case pb.DeviceState_DEVICE_STATE_FAULT:
		return system.StateFault
	case pb.DeviceState_DEVICE_STATE_FINISH:
		return system.StateFinish
	default:
		return system.StateUnknown
	}
}

var _ system.Inventory = (*Client)(nil)
var _ system.PartitionController = (*Client)(nil)
