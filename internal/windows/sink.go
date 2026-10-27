//go:build windows

// xsstrashy/internal/windows/sink.go
package windows

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type sinkObj struct {
	vtbl    uintptr
	pinner  runtime.Pinner
	req     *moveRequest
	cookie  uint32
	success int
	lastErr error
}

var sinkVtbl [19]uintptr // 3 (IUnknown) + 16 методов sink
var sinkVtblOnce sync.Once

func initSinkVtbl() {
	sinkVtblOnce.Do(func() {
		sinkVtbl[0] = syscall.NewCallback(sinkQueryInterface)
		sinkVtbl[1] = syscall.NewCallback(sinkAddRef)
		sinkVtbl[2] = syscall.NewCallback(sinkRelease)
		sinkVtbl[3] = syscall.NewCallback(sinkStartOperations)
		sinkVtbl[4] = syscall.NewCallback(sinkFinishOperations)
		sinkVtbl[12] = syscall.NewCallback(sinkPostDeleteItem)
		for i := range sinkVtbl {
			if sinkVtbl[i] == 0 {
				sinkVtbl[i] = syscall.NewCallback(sinkNoop)
			}
		}
	})
}

func newSink(req *moveRequest) *sinkObj {
	initSinkVtbl()
	s := &sinkObj{req: req}
	s.vtbl = uintptr(unsafe.Pointer(&sinkVtbl[0]))
	s.pinner.Pin(s) // закрепляем в памяти, чтобы GC не переместил
	return s
}

func (s *sinkObj) release() {
	// Не открепляем, так как COM всё ещё может держать ссылку.
	runtime.KeepAlive(s)
}

// Колбэки для IUnknown
func sinkQueryInterface(this, riid, ppv uintptr) uintptr {
	if riid == 0 || ppv == 0 {
		return 0x80004003 // E_POINTER
	}
	requested := (*GUID)(unsafe.Pointer(riid))
	if *requested == IID_IFileOperationProgressSink || *requested == IID_IUnknown {
		*(*uintptr)(unsafe.Pointer(ppv)) = this
		sinkAddRef(this)
		return 0 // S_OK
	}
	return 0x80004002 // E_NOINTERFACE
}
func sinkAddRef(this uintptr) uintptr  { return 2 }
func sinkRelease(this uintptr) uintptr { return 1 }

// Колбэки для IFileOperationProgressSink
func sinkStartOperations(this uintptr) uintptr { return 0 }
func sinkFinishOperations(this, hrResult uintptr) uintptr {
	s := (*sinkObj)(unsafe.Pointer(this))
	if int32(hrResult) < 0 && s.lastErr == nil {
		s.lastErr = checkHR(hrResult, "FinishOperations")
	}
	finishRequested = true
	return 0
}
func sinkPostDeleteItem(this, flags, psiItem, hrDelete, psiNewlyCreated uintptr) uintptr {
	s := (*sinkObj)(unsafe.Pointer(this))
	if int32(hrDelete) >= 0 {
		s.success++
	} else if s.lastErr == nil {
		s.lastErr = checkHR(hrDelete, "PostDeleteItem")
	}
	return 0
}
func sinkNoop(this uintptr) uintptr { return 0 }
