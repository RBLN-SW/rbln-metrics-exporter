// Package command implements bounded, context-aware execution without a shell.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	system "github.com/rbln-sw/rblnlib-go/pkg/system/v1"
)

type buffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *buffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout := &buffer{limit: 16 * 1024 * 1024}
	stderr := &buffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		kind := system.Unavailable
		if errors.Is(err, exec.ErrNotFound) {
			kind = system.Unsupported
		}
		return nil, &system.Error{Kind: kind, Op: binary, Message: stderr.String(), Cause: err}
	}
	if stdout.overflow {
		return nil, &system.Error{Kind: system.InvalidData, Op: binary, Message: fmt.Sprintf("output exceeds %d bytes", stdout.limit)}
	}
	return stdout.Bytes(), nil
}
