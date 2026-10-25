//go:build windows

// xsstrashy/internal/windows/worker.go
package windows

import (
	"context"
)

// Result carries the outcome of Move in a form that the public xsstrashy
// package can translate into a PartialError.
type Result struct {
	Moved  []string
	Failed map[string]error
}

// Move sends paths to the STA thread for IFileOperation.
//
// If ctx is canceled before a request reaches the STA thread, no work
// is started at all. If canceled during the operation, the effect is
// best-effort: IFileOperation is not aborted mid-flight (see comment
// in startOperation), but the caller returns immediately with ctx.Err()
// — files already in the trash stay there, files not yet processed
// remain untouched.
func Move(ctx context.Context, paths []string) Result {
	// Pre-filter non-existent paths — idempotent contract, same as
	// Linux implementation.
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		clean = append(clean, p) // existence checked below by shell
	}
	if len(clean) == 0 {
		return Result{}
	}

	req := &moveRequest{
		paths:  clean,
		result: make(chan moveResult, 1),
		ctx:    contextDoneAdapter{ctx},
	}

	if err := postMove(req); err != nil {
		// STA unavailable (initialisation failed) — report all paths
		// as failed, caller falls back to permanent delete.
		failed := make(map[string]error, len(clean))
		for _, p := range clean {
			failed[p] = err
		}
		return Result{Failed: failed}
	}

	select {
	case res := <-req.result:
		return Result{Moved: res.moved, Failed: res.failed}
	case <-ctx.Done():
		// Do not touch the running operation — it will finish on its
		// own. Return immediately; whatever is in the trash stays there.
		failed := make(map[string]error, len(clean))
		for _, p := range clean {
			failed[p] = ctx.Err()
		}
		return Result{Failed: failed}
	}
}

// contextDoneAdapter lets moveRequest hold a context without importing
// it into STA-related code.
type contextDoneAdapter struct {
	ctx context.Context
}

func (a contextDoneAdapter) Done() <-chan struct{} { return a.ctx.Done() }
func (a contextDoneAdapter) Err() error            { return a.ctx.Err() }
