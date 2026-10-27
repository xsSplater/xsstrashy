//go:build windows

// xsstrashy/internal/windows/sta.go
package windows

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	WM_APP_MOVE   = 0x8001
	WM_APP_FINISH = 0x8002
	classname     = "xsstrashy_sta_v1"
)

const ERROR_CLASS_ALREADY_EXISTS = syscall.Errno(1410)

var (
	staOnce   sync.Once
	staErr    error
	staReady  = make(chan struct{})
	staHWND   uintptr
	pendingCh = make(chan *moveRequest, 16)
)

type moveRequest struct {
	paths  []string
	result chan moveResult
	ctx    context.Context
}
type moveResult struct {
	moved  []string
	failed map[string]error
}

func staInit() error {
	staOnce.Do(func() {
		go staThreadBody()
		<-staReady
	})
	return staErr
}

func postMove(req *moveRequest) error {
	if err := staInit(); err != nil {
		return err
	}
	select {
	case pendingCh <- req:
	default:
		return fmt.Errorf("request queue full")
	}
	hwnd := syscall.Handle(staHWND)
	user32 := syscall.NewLazyDLL("user32.dll")
	postMessage := user32.NewProc("PostMessageW")
	postMessage.Call(uintptr(hwnd), WM_APP_MOVE, 0, 0)
	return nil
}

func staThreadBody() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if r := recover(); r != nil {
			staErr = fmt.Errorf("panic: %v", r)
			select {
			case <-staReady:
			default:
				close(staReady)
			}
		}
	}()

	hr, _, _ := procCoInitializeEx.Call(0, 0x2|0x4) // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE
	if int32(hr) < 0 {
		staErr = fmt.Errorf("CoInitializeEx failed: 0x%08X", uint32(hr))
		close(staReady)
		return
	}
	defer procCoUninitialize.Call()

	hwnd, err := createMessageWindow()
	if err != nil {
		staErr = err
		close(staReady)
		return
	}
	staHWND = hwnd
	defer destroyWindow(hwnd)

	close(staReady)

	user32 := syscall.NewLazyDLL("user32.dll")
	getMessage := user32.NewProc("GetMessageW")
	translateMessage := user32.NewProc("TranslateMessage")
	dispatchMessage := user32.NewProc("DispatchMessageW")

	var msg MSG
	for {
		ret, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))

		if currentOp == nil {
			select {
			case req := <-pendingCh:
				startOperation(req)
			default:
			}
		}
	}
}

func createMessageWindow() (uintptr, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getModuleHandle := kernel32.NewProc("GetModuleHandleW")
	registerClassEx := user32.NewProc("RegisterClassExW")
	createWindowEx := user32.NewProc("CreateWindowExW")
	defWindowProc := user32.NewProc("DefWindowProcW")

	wndProc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
		return r
	})

	hInst, _, _ := getModuleHandle.Call(0)
	className, _ := syscall.UTF16PtrFromString(classname)

	type WNDCLASSEX struct {
		cbSize        uint32
		style         uint32
		lpfnWndProc   uintptr
		cbClsExtra    int32
		cbWndExtra    int32
		hInstance     syscall.Handle
		hIcon         syscall.Handle
		hCursor       syscall.Handle
		hbrBackground syscall.Handle
		lpszMenuName  *uint16
		lpszClassName *uint16
		hIconSm       syscall.Handle
	}
	var wc WNDCLASSEX
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = wndProc
	wc.hInstance = syscall.Handle(hInst)
	wc.lpszClassName = className

	ret, _, err := registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 && err != ERROR_CLASS_ALREADY_EXISTS {
		return 0, fmt.Errorf("RegisterClassEx: %w", err)
	}

	hwndMsg := ^uintptr(2) // HWND_MESSAGE = (HWND)-3
	hwnd, _, err := createWindowEx.Call(
		0, uintptr(unsafe.Pointer(className)), 0, 0,
		0, 0, 0, 0, hwndMsg, 0, hInst, 0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowEx: %w", err)
	}
	return hwnd, nil
}

func destroyWindow(hwnd uintptr) {
	user32 := syscall.NewLazyDLL("user32.dll")
	destroyWindowProc := user32.NewProc("DestroyWindow")
	destroyWindowProc.Call(hwnd)
}
