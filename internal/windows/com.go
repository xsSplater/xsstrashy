//go:build windows

// xsstrashy/internal/windows/com.go
package windows

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
var procCoCreateInstance = ole32.NewProc("CoCreateInstance")

// GUIDs
var (
	clsidFileOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidShellItem       = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	iidProgressSink    = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
	iidIUnknown        = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0xc000, Data4: [8]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

const (
	clsctxAll    = 0x17
	sOK          = 0
	eNoInterface = 0x80004002
	ePointer     = 0x80004003
)

// IFileOperation vtable indices (IUnknown = 0,1,2).
const (
	ifOpIdxAdvise     = 3
	ifOpIdxUnadvise   = 4
	ifOpIdxSetFlags   = 5
	ifOpIdxDeleteItem = 18
	ifOpIdxPerformOps = 21
)

// IFileOperationProgressSink vtable indices.
const (
	sinkIdxQueryInterface = 0
	sinkIdxAddRef         = 1
	sinkIdxRelease        = 2
	sinkIdxStartOps       = 3
	sinkIdxFinishOps      = 4
	sinkIdxPostDeleteItem = 12
	sinkVtblSize          = 19
)

// vcall performs a virtual call on a COM object.
func vcall(obj uintptr, index int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Add(unsafe.Pointer(vtbl), uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		vcall(obj, 2)
	}
}

// ─────────────────────────────────────────────────────────────────
// IFileOperation wrappers
// ─────────────────────────────────────────────────────────────────

func ifileOpCreate() (uintptr, error) {
	var obj uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOperation)),
		0, clsctxAll,
		uintptr(unsafe.Pointer(&iidFileOperation)),
		uintptr(unsafe.Pointer(&obj)),
	)
	if int32(hr) < 0 {
		return 0, fmt.Errorf("CoCreateInstance(IFileOperation): HRESULT 0x%08X", uint32(hr))
	}
	return obj, nil
}

func ifileOpAdvise(obj uintptr, s *sinkObj) (uint32, error) {
	var cookie uint32
	hr := vcall(obj, ifOpIdxAdvise, uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(&cookie)))
	if int32(hr) < 0 {
		return 0, fmt.Errorf("IFileOperation::Advise: HRESULT 0x%08X", uint32(hr))
	}
	return cookie, nil
}

func ifileOpUnadvise(obj uintptr, cookie uint32) {
	vcall(obj, ifOpIdxUnadvise, uintptr(cookie))
}

func ifileOpSetFlags(obj uintptr, flags uint32) error {
	hr := vcall(obj, ifOpIdxSetFlags, uintptr(flags))
	if int32(hr) < 0 {
		return fmt.Errorf("IFileOperation::SetOperationFlags: HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

func ifileOpDeleteItem(obj uintptr, shellItem uintptr) error {
	hr := vcall(obj, ifOpIdxDeleteItem, shellItem)
	if int32(hr) < 0 {
		return fmt.Errorf("IFileOperation::DeleteItem: HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

func ifileOpPerform(obj uintptr) error {
	hr := vcall(obj, ifOpIdxPerformOps)
	if int32(hr) < 0 {
		return fmt.Errorf("IFileOperation::PerformOperations: HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

func shellItemFromPath(path string) (uintptr, error) {
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(p16)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if int32(hr) < 0 {
		return 0, fmt.Errorf("SHCreateItemFromParsingName: HRESULT 0x%08X", uint32(hr))
	}
	return item, nil
}

// ─────────────────────────────────────────────────────────────────
// Sink implementation via runtime.Pinner.
// ─────────────────────────────────────────────────────────────────
//
// sinkObj is a real COM object: its first field is the vtable pointer.
// runtime.Pinner pins the object in memory, so *sinkObj is a stable
// C-address for the object's lifetime. COM passes that pointer back
// as "this" in every callback. We convert it back to *sinkObj via
// unsafe.Pointer — this works because the object was pinned and Go's
// GC will not move it.

type sinkObj struct {
	vtbl         [sinkVtblSize]uintptr // MUST be first field
	pinner       runtime.Pinner
	req          *moveRequest
	cookie       uint32
	successCount int
	err          error
}

var (
	sinkVtblOnce sync.Once
	sinkVtbl     [sinkVtblSize]uintptr
	sinkCounter  atomic.Uintptr // just for logging / diagnostics
)

func newSink(req *moveRequest) *sinkObj {
	initSinkVtbl()
	s := &sinkObj{req: req}
	s.vtbl = sinkVtbl
	s.pinner.Pin(s)
	sinkCounter.Add(1)
	return s
}

func (s *sinkObj) release() {
	s.pinner.Unpin()
	runtime.KeepAlive(s)
}

func sinkFromThis(this uintptr) *sinkObj {
	return (*sinkObj)(unsafe.Pointer(this))
}

func initSinkVtbl() {
	sinkVtblOnce.Do(func() {
		sinkVtbl[sinkIdxQueryInterface] = syscall.NewCallback(sinkQueryInterface)
		sinkVtbl[sinkIdxAddRef] = syscall.NewCallback(sinkAddRef)
		sinkVtbl[sinkIdxRelease] = syscall.NewCallback(sinkRelease)
		sinkVtbl[sinkIdxStartOps] = syscall.NewCallback(sinkStartOps)
		sinkVtbl[sinkIdxFinishOps] = syscall.NewCallback(sinkFinishOps)
		sinkVtbl[sinkIdxPostDeleteItem] = syscall.NewCallback(sinkPostDeleteItem)
		for i := 0; i < sinkVtblSize; i++ {
			if sinkVtbl[i] == 0 {
				sinkVtbl[i] = syscall.NewCallback(sinkNoop)
			}
		}
	})
}

func sinkQueryInterface(this, riid, ppv uintptr) uintptr {
	if riid == 0 || ppv == 0 {
		return ePointer
	}
	requested := (*windows.GUID)(unsafe.Pointer(riid))
	if *requested == iidProgressSink || *requested == iidIUnknown {
		*(*uintptr)(unsafe.Pointer(ppv)) = this
		sinkAddRef(this)
		return sOK
	}
	return eNoInterface
}

func sinkAddRef(this uintptr) uintptr {
	return 2
}

func sinkRelease(this uintptr) uintptr {
	return 1
}

func sinkStartOps(this uintptr) uintptr {
	return sOK
}

func sinkNoop(this uintptr) uintptr {
	return sOK
}

func sinkPostDeleteItem(this, flags, psiItem, hrDelete, psiNewlyCreated uintptr) uintptr {
	s := sinkFromThis(this)
	if int32(hrDelete) >= 0 {
		s.successCount++
	} else if s.err == nil {
		s.err = fmt.Errorf("delete failed: HRESULT 0x%08X", uint32(hrDelete))
	}
	return sOK
}

func sinkFinishOps(this, hrResult uintptr) uintptr {
	s := sinkFromThis(this)
	if int32(hrResult) < 0 && s.err == nil {
		s.err = fmt.Errorf("IFileOperation::FinishOperations: HRESULT 0x%08X", uint32(hrResult))
	}
	// Cleanup happens on the STA thread (from WndProc), not here —
	// calling Unadvise or Release from inside a sink callback risks
	// re-entering COM while the operation is still unwinding.
	_ = PostMessage(staHWND, wmAppFinish, 0, 0)
	return sOK
}
