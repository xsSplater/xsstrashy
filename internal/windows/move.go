//go:build windows

// xsstrashy/internal/windows/move.go
package windows

import "context"

type Result struct {
	Moved  []string
	Failed map[string]error
}

func Move(ctx context.Context, paths []string) Result {
	req := &moveRequest{
		paths:  paths,
		result: make(chan moveResult, 1),
		ctx:    ctx,
	}
	if err := postMove(req); err != nil {
		return Result{Failed: allFailed(paths, err)}
	}
	res := <-req.result
	return Result{Moved: res.moved, Failed: res.failed}
}
