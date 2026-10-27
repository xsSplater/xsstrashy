//go:build windows

// xsstrashy/internal/windows/com.go
package windows

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
)

var (
	CLSID_FileOperation            = GUID{0x3AD05575, 0x8857, 0x4850, [8]byte{0x92, 0x77, 0x11, 0xB8, 0x5B, 0xDB, 0x8E, 0x09}}
	IID_IFileOperation             = GUID{0x947AAB5F, 0x0A5C, 0x4C13, [8]byte{0xB4, 0xD6, 0x4B, 0xF7, 0x83, 0x6F, 0xC9, 0xF8}}
	IID_IShellItem                 = GUID{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	IID_IFileOperationProgressSink = GUID{0x04B0F1A7, 0x9490, 0x44BC, [8]byte{0x96, 0xE1, 0x42, 0x96, 0xA3, 0x12, 0x52, 0xE2}}
	IID_IUnknown                   = GUID{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// comCall вызывает метод по индексу в VTable COM-объекта.
//
//go:nocheckptr
func comCall(obj uintptr, index int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Add(unsafe.Pointer(vtbl), uintptr(index)*unsafe.Sizeof(uintptr(0))))
	allArgs := append([]uintptr{obj}, args...)
	ret, _, _ := syscall.SyscallN(fn, allArgs...)
	return ret
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2) // IUnknown::Release
	}
}

func checkHR(hr uintptr, context string) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08X", context, uint32(hr))
	}
	return nil
}

// Создание экземпляра COM-объекта.
func coCreateInstance(clsid, iid *GUID) (uintptr, error) {
	var obj uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)),
		0,
		0x17, // CLSCTX_ALL
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&obj)),
	)
	if err := checkHR(hr, "CoCreateInstance"); err != nil {
		return 0, err
	}
	return obj, nil
}

// Создание IShellItem из пути.
func shellItemFromPath(path string) (uintptr, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(unsafe.Pointer(&IID_IShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if err := checkHR(hr, "SHCreateItemFromParsingName"); err != nil {
		return 0, err
	}
	return item, nil
}
