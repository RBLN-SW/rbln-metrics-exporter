package smd

import (
	"context"
	"strings"

	pb "github.com/rbln-sw/rblnlib-go/internal/proto/smdv1"
	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

// Version queries the daemon's own read-only version key, not a driver/firmware
// version. Older servers may return Unsupported/NotFound; callers must not
// infer a daemon version from unrelated package versions.
func (c *Client) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	r, err := c.config.GetConfig(ctx, &pb.GetConfigRequest{Key: "rsmd_version"})
	if err != nil {
		return "", rpcError("SMD version", "", err)
	}
	v := strings.TrimSpace(r.GetValue())
	if v == "" {
		return "", system.FieldError("SMD", "version", "empty")
	}
	return v, nil
}

func (c *Client) compatibilityVersion(ctx context.Context) string {
	if c.opts.ServerVersion != "" {
		return c.opts.ServerVersion
	}
	version, _ := c.Version(ctx)
	return version
}
