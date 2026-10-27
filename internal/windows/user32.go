//go:build windows

// xsstrashy/internal/windows/user32.go
package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
)

const (
	WM_APP = 0x8000
)

type MSG struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type WndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

func GetModuleHandle() (windows.Handle, error) {
	r0, _, e1 := procGetModuleHandleW.Call(0)
	if r0 == 0 {
		return 0, e1
	}
	return windows.Handle(r0), nil
}

func PostMessage(hwnd windows.HWND, msg uint32, wparam, lparam uintptr) error {
	r0, _, e1 := procPostMessageW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	if r0 == 0 {
		return e1
	}
	return nil
}

func DestroyWindow(hwnd windows.HWND) error {
	r0, _, e1 := procDestroyWindow.Call(uintptr(hwnd))
	if r0 == 0 {
		return e1
	}
	return nil
}

func GetMessage(msg *MSG, hwnd windows.HWND, wMsgFilterMin, wMsgFilterMax uint32) (int32, error) {
	r0, _, e1 := procGetMessageW.Call(
		uintptr(unsafe.Pointer(msg)),
		uintptr(hwnd),
		uintptr(wMsgFilterMin),
		uintptr(wMsgFilterMax),
	)
	ret := int32(r0)
	if ret == -1 {
		return -1, e1
	}
	return ret, nil
}

func TranslateMessage(msg *MSG) bool {
	r0, _, _ := procTranslateMessage.Call(uintptr(unsafe.Pointer(msg)))
	return r0 != 0
}

func DispatchMessage(msg *MSG) uintptr {
	r0, _, _ := procDispatchMessageW.Call(uintptr(unsafe.Pointer(msg)))
	return r0
}
