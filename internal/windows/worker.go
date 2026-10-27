//go:build windows

// xsstrashy/internal/windows/worker.go
package windows

import (
	"fmt"
	"os"
	"unsafe"
)

var currentOp *runningOp

type runningOp struct {
	req  *moveRequest
	sink *sinkObj
	ifOp uintptr
}

func startOperation(req *moveRequest) {
	sink := newSink(req)

	ifOp, err := coCreateInstance(&CLSID_FileOperation, &IID_IFileOperation)
	if err != nil {
		req.result <- moveResult{failed: allFailed(req.paths, err)}
		return
	}

	var cookie uint32
	hr := comCall(ifOp, 3, uintptr(unsafe.Pointer(sink)), uintptr(unsafe.Pointer(&cookie))) // Advise
	if int32(hr) < 0 {
		comRelease(ifOp)
		req.result <- moveResult{failed: allFailed(req.paths, fmt.Errorf("Advise: 0x%08X", uint32(hr)))}
		return
	}
	sink.cookie = cookie

	flags := uintptr(0x0040 | 0x0004 | 0x0400) // FOF_ALLOWUNDO | FOF_SILENT | FOF_NOERRORUI
	hr = comCall(ifOp, 5, flags)               // SetOperationFlags
	if int32(hr) < 0 {
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{failed: allFailed(req.paths, fmt.Errorf("SetOperationFlags: 0x%08X", uint32(hr)))}
		return
	}

	var added int
	for _, p := range req.paths {
		if err := req.ctx.Err(); err != nil {
			break
		}
		if _, err := os.Lstat(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		norm, err := normalizePath(p)
		if err != nil {
			continue
		}
		item, err := shellItemFromPath(norm)
		if err != nil {
			continue
		}
		hr = comCall(ifOp, 18, item, 0) // DeleteItem
		comRelease(item)
		if int32(hr) < 0 {
			continue
		}
		added++
	}

	// Если не добавлено ни одного элемента, не вызываем PerformOperations
	if added == 0 {
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{
			failed: make(map[string]error),
		}
		return
	}

	currentOp = &runningOp{req: req, sink: sink, ifOp: ifOp}

	hr = comCall(ifOp, 21) // PerformOperations
	if int32(hr) < 0 {
		currentOp = nil
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{failed: allFailed(req.paths, fmt.Errorf("PerformOperations: 0x%08X", uint32(hr)))}
		return
	}
	// Результат будет отправлен в finishOperation, когда придёт FinishOperations.
}

func finishOperation() {
	op := currentOp
	if op == nil {
		return
	}
	currentOp = nil

	if op.ifOp != 0 && op.sink != nil {
		comCall(op.ifOp, 4, uintptr(op.sink.cookie)) // Unadvise
	}
	if op.ifOp != 0 {
		comRelease(op.ifOp)
	}
	if op.sink != nil {
		op.sink.release()
	}

	total := len(op.req.paths)
	okCount := op.sink.success
	var sinkErr error
	if op.sink != nil {
		sinkErr = op.sink.lastErr
	}

	var res moveResult
	switch {
	case okCount == total:
		res.moved = append([]string(nil), op.req.paths...)
	case okCount == 0:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("operation failed")
		}
		res.failed = allFailed(op.req.paths, sinkErr)
	default:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("partial failure")
		}
		res.moved = append([]string(nil), op.req.paths[:okCount]...)
		res.failed = make(map[string]error, total-okCount)
		for _, p := range op.req.paths[okCount:] {
			res.failed[p] = sinkErr
		}
	}

	select {
	case op.req.result <- res:
	default:
	}
}

func allFailed(paths []string, err error) map[string]error {
	m := make(map[string]error, len(paths))
	for _, p := range paths {
		m[p] = err
	}
	return m
}
