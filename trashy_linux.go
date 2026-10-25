//go:build linux

// xsstrashy/trashy_linux.go
package xsstrashy

import (
	"context"

	"github.com/xsSplater/xsstrashy/internal/linux"
)

func movePaths(ctx context.Context, paths []string) error {
	r := linux.Move(ctx, paths)

	// If nothing moved and ctx is done, return the context error
	// directly so callers can errors.Is against context.Canceled /
	// DeadlineExceeded.
	if len(r.Moved) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	return newPartialError(r.Moved, r.Failed)
}

func isSupported() bool { return true }

func isAvailable() bool { return linux.IsAvailable() }
