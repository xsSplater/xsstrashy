//go:build windows

// xsstrashy/trashy_windows.go
package xsstrashy

import (
	"context"

	"github.com/xsSplater/xsstrashy/internal/windows"
)

func movePaths(ctx context.Context, paths []string) error {
	r := windows.Move(ctx, paths)

	if len(r.Moved) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	return newPartialError(r.Moved, r.Failed)
}

func isSupported() bool { return true }

func isAvailable() bool { return true }
