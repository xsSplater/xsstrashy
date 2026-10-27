//go:build windows

// xsstrashy/internal/windows/worker.go
package windows

import (
	"fmt"
	"log"
	"os"
	"unsafe"
)

var currentOp *runningOp

type runningOp struct {
	req        *moveRequest
	sink       *sinkObj
	ifOp       uintptr
	addedPaths []string
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

	// FOFX_RECYCLEONDELETE | FOF_ALLOWUNDO | FOF_SILENT | FOF_NOERRORUI
	flags := uintptr(0x00080000 | 0x0040 | 0x0004 | 0x0400)
	hr = comCall(ifOp, 5, flags) // SetOperationFlags
	if int32(hr) < 0 {
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{failed: allFailed(req.paths, fmt.Errorf("SetOperationFlags: 0x%08X", uint32(hr)))}
		return
	}

	var addedPaths []string
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
			log.Printf("[xsstrashy] DeleteItem failed for %s: HRESULT 0x%08X", p, uint32(hr))
			continue
		}
		log.Printf("[xsstrashy] DeleteItem queued for %s", p)
		addedPaths = append(addedPaths, p)
	}

	// Если не добавлено ни одного элемента, не вызываем PerformOperations
	if len(addedPaths) == 0 {
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{
			failed: make(map[string]error),
		}
		return
	}

	currentOp = &runningOp{req: req, sink: sink, ifOp: ifOp, addedPaths: addedPaths}

	// PerformOperations синхронный: он блокирует поток до завершения всех
	// операций, после чего вызывает FinishOperations.
	hr = comCall(ifOp, 21) // PerformOperations
	if int32(hr) < 0 {
		log.Printf("[xsstrashy] PerformOperations failed: HRESULT 0x%08X", uint32(hr))
		currentOp = nil
		comCall(ifOp, 4, uintptr(cookie)) // Unadvise
		comRelease(ifOp)
		req.result <- moveResult{failed: allFailed(req.paths, fmt.Errorf("PerformOperations: 0x%08X", uint32(hr)))}
		return
	}
	log.Printf("[xsstrashy] PerformOperations returned S_OK")

	// Поскольку PerformOperations синхронный, завершаем операцию сразу.
	finishOperation()
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

	total := len(op.addedPaths)
	okCount := int(op.sink.success)
	var sinkErr error
	if op.sink != nil {
		sinkErr = op.sink.lastErr
	}

	var res moveResult
	switch {
	case okCount == total:
		res.moved = append([]string(nil), op.addedPaths...)
	case okCount == 0:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("operation failed")
		}
		res.failed = allFailed(op.addedPaths, sinkErr)
	default:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("partial failure")
		}
		res.moved = append([]string(nil), op.addedPaths[:okCount]...)
		res.failed = make(map[string]error, total-okCount)
		for _, p := range op.addedPaths[okCount:] {
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
