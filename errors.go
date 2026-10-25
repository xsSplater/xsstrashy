// xsstrashy/errors.go
package xsstrashy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors. Use errors.Is to detect them.
var (
	// ErrNotSupported is returned when the current platform has no
	// trash implementation.
	ErrNotSupported = errors.New("xsstrashy: trash is not supported on this platform")

	// ErrNotAvailable is returned when the trash exists but is currently
	// unusable (e.g. home trash directory is not writable on Linux).
	ErrNotAvailable = errors.New("xsstrashy: trash is not available in this environment")

	// ErrEmptyPath is returned when MoveContext is called with no paths.
	ErrEmptyPath = errors.New("xsstrashy: no paths provided")
)

// PartialError is returned when at least one path was moved to the trash
// and at least one failed. If ALL paths fail, a regular (non-partial)
// error is returned instead - callers don't have to check both cases.
//
// Use errors.As to extract, and errors.Is on the result to test
// against any of the underlying causes.
type PartialError struct {
	// Moved lists paths that ended up in the trash.
	Moved []string
	// Failed maps paths that did not make it to the reason.
	Failed map[string]error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("xsstrashy: %d moved, %d failed", len(e.Moved), len(e.Failed))
}

// Unwrap returns all underlying errors joined together.
// Thanks to errors.Join's []error support, errors.Is(err, someSentinel)
// will match if ANY of the failed paths carries that sentinel.
func (e *PartialError) Unwrap() error {
	errs := make([]error, 0, len(e.Failed))
	for _, err := range e.Failed {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// newPartialError builds the appropriate error for a mixed outcome.
// Returns nil if all succeeded. Returns a flat error if nothing succeeded
// (so callers can treat "total failure" and "partial success" differently
// just by checking for *PartialError).
//
// Path list is sorted for deterministic error messages - useful in tests
// and logs.
func newPartialError(moved []string, failed map[string]error) error {
	if len(failed) == 0 {
		return nil
	}
	if len(moved) == 0 {
		paths := make([]string, 0, len(failed))
		for p := range failed {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var sb strings.Builder
		sb.WriteString("xsstrashy: all paths failed:")
		for _, p := range paths {
			sb.WriteString("\n  ")
			sb.WriteString(p)
			sb.WriteString(": ")
			sb.WriteString(failed[p].Error())
		}
		return errors.New(sb.String())
	}
	return &PartialError{Moved: moved, Failed: failed}
}
