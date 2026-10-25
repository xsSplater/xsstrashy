//go:build !linux && !windows

// xsstrashy/trashy_other.go
package xsstrashy

import "context"

// movePaths is a stub for platforms without a trash implementation.
// Present so that the package compiles on macOS, BSD, etc. - MoveContext
// returns ErrNotSupported there, which the caller can detect with
// errors.Is.
func movePaths(_ context.Context, _ []string) error {
	return ErrNotSupported
}

func isSupported() bool { return false }
func isAvailable() bool { return false }
