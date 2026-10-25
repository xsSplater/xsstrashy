// Package xsstrashy moves files and directories to the system trash on
// Linux and Windows, with a predictable cross-platform API.
//
// # Platforms
//
// Linux: implements the FreeDesktop.org Trash Specification 1.0
// (https://specifications.freedesktop.org/trash-spec/) without any
// external tools. No dependencies beyond the Go standard library.
//
// Windows: uses IFileOperation on a dedicated STA thread with a running
// message pump, driven by FOF_ASYNC and IFileOperationProgressSink. This
// avoids the classic deadlocks of SHFileOperation and synchronous
// IFileOperation calls from arbitrary goroutines.
//
// Other platforms: MoveContext returns ErrNotSupported, IsSupported
// returns false.
//
// # Typical usage
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := xsstrashy.MoveContext(ctx, path1, path2); err != nil {
//		var partial *xsstrashy.PartialError
//		switch {
//		case errors.As(err, &partial):
//			// some moved, some didn't - inspect partial.Failed
//		case errors.Is(err, context.DeadlineExceeded):
//			// operation timed out; UI stays responsive
//		default:
//			// total failure
//		}
//	}

// xsstrashy/trashy.go
package xsstrashy

import "context"

// MoveContext moves each path to the system trash.
//
// Behavior:
//   - Non-existent paths are skipped silently (idempotent - useful when
//     the caller cannot cheaply pre-check existence).
//   - Symlinks are moved as links; their targets are not touched.
//   - If ctx is canceled or its deadline is exceeded, MoveContext
//     attempts to abort the ongoing operation and returns ctx.Err().
//     Files already moved remain in the trash.
//
// Returns:
//   - nil             - all paths moved successfully.
//   - ErrEmptyPath    - paths is empty.
//   - ErrNotSupported - platform has no trash implementation.
//   - *PartialError   - at least one moved, at least one failed.
//   - other error     - all paths failed, or context was canceled.
func MoveContext(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return ErrEmptyPath
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return movePaths(ctx, paths)
}

// Move is a convenience wrapper around MoveContext with no timeout.
//
// Warning: without a timeout, Move may block indefinitely on pathological
// systems (antivirus, network drives, broken shell extensions). In
// interactive applications, always prefer MoveContext.
func Move(paths ...string) error {
	return MoveContext(context.Background(), paths...)
}

// IsSupported reports whether the current platform has a trash
// implementation in xsstrashy.
func IsSupported() bool {
	return isSupported()
}

// IsAvailable reports whether the trash is usable right now.
//
// Linux: checks that the user's home trash directory exists and is
// writable.
//
// Windows: the recycle bin is available on all supported systems
// (Vista+), so returns true.
//
// Other platforms: false.
func IsAvailable() bool {
	return isAvailable()
}
