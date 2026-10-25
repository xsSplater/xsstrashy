//go:build windows

// xsstrashy/internal/windows/sta.go
package windows

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmAppMove   = WM_APP + 1
	wmAppFinish = WM_APP + 2

	msgWindowClass = "xsstrashy_sta_v1"
)

const errClassAlreadyExists syscall.Errno = 1410

// moveRequest is a single MoveContext call forwarded to the STA thread.
type moveRequest struct {
	paths  []string
	result chan moveResult
	ctx    contextDone
}

type moveResult struct {
	moved  []string
	failed map[string]error
}

// contextDone is the minimal subset of context.Context used inside
// the windows package.
type contextDone interface {
	Done() <-chan struct{}
	Err() error
}

var (
	staOnce     sync.Once
	staInitErr  error
	staReady    = make(chan struct{})
	staThreadID uint32
	staHWND     windows.HWND

	pendingCh = make(chan *moveRequest, 16)

	currentOp *runningOp
)

type runningOp struct {
	req  *moveRequest
	sink *sinkObj
	ifOp uintptr
}

func staInit() error {
	staOnce.Do(func() {
		go staThreadBody()
		<-staReady
	})
	return staInitErr
}

func postMove(req *moveRequest) error {
	if err := staInit(); err != nil {
		return err
	}
	select {
	case pendingCh <- req:
	default:
		return fmt.Errorf("xsstrashy: request queue full")
	}
	return PostMessage(staHWND, wmAppMove, 0, 0)
}

func staThreadBody() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		if r := recover(); r != nil {
			staInitErr = fmt.Errorf("panic in staThreadBody: %v", r)
			select {
			case <-staReady:
			default:
				close(staReady)
			}
		}
	}()

	if err := windows.CoInitializeEx(0,
		windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil {
		staInitErr = fmt.Errorf("CoInitializeEx: %w", err)
		close(staReady)
		return
	}
	defer windows.CoUninitialize()

	staThreadID = windows.GetCurrentThreadId()

	hwnd, err := createMessageWindow()
	if err != nil {
		staInitErr = fmt.Errorf("create message window: %w", err)
		close(staReady)
		return
	}
	staHWND = hwnd
	defer DestroyWindow(hwnd)

	close(staReady)

	var msg MSG
	for {
		ret, _ := GetMessage(&msg, 0, 0, 0)
		if ret == 0 || ret == -1 {
			return
		}
		TranslateMessage(&msg)
		DispatchMessage(&msg)

		// Выполняем работу ВНЕ WndProc, чтобы избежать вложенного cgocall!
		drainQueue()
	}
}

var finishRequested bool

func drainQueue() {
	if finishRequested {
		finishRequested = false
		finishOperation()
		return
	}
	if currentOp != nil {
		return
	}
	select {
	case req := <-pendingCh:
		startOperation(req)
	default:
	}
}

func createMessageWindow() (windows.HWND, error) {
	wndProc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		// WndProc больше ничего не делает, только будит GetMessage.
		// Вся работа выполняется в staThreadBody после DispatchMessage.
		switch msg {
		case wmAppFinish:
			finishRequested = true
			return 0
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
		return r
	})

	hInst, err := GetModuleHandle()
	if err != nil {
		return 0, err
	}

	className, err := windows.UTF16PtrFromString(msgWindowClass)
	if err != nil {
		return 0, err
	}

	var wc WndClassEx
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = wndProc
	wc.HInstance = hInst
	wc.LpszClassName = className

	r1, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if r1 == 0 {
		if err != errClassAlreadyExists {
			return 0, err
		}
	}

	hwndMessage := ^uintptr(2)
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), 0, 0,
		0, 0, 0, 0,
		hwndMessage, 0, uintptr(hInst), 0,
	)
	if hwnd == 0 {
		return 0, err
	}
	return windows.HWND(hwnd), nil
}

// Переименовали handleFinishMessage в finishOperation,
// так как теперь она вызывается из drainQueue
func finishOperation() {
	op := currentOp
	if op == nil {
		return
	}
	currentOp = nil

	if op.ifOp != 0 && op.sink != nil {
		ifileOpUnadvise(op.ifOp, op.sink.cookie)
	}
	if op.ifOp != 0 {
		comRelease(op.ifOp)
	}
	if op.sink != nil {
		op.sink.release()
	}

	total := len(op.req.paths)
	okCount := 0
	var sinkErr error
	if op.sink != nil {
		okCount = op.sink.successCount
		sinkErr = op.sink.err
	}

	var res moveResult
	switch {
	case okCount == total:
		res.moved = append([]string(nil), op.req.paths...)
	case okCount == 0:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("IFileOperation failed")
		}
		res.failed = make(map[string]error, total)
		for _, p := range op.req.paths {
			res.failed[p] = sinkErr
		}
	default:
		if sinkErr == nil {
			sinkErr = fmt.Errorf("IFileOperation partially failed")
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

func startOperation(req *moveRequest) {
	sink := newSink(req)

	ifOp, err := ifileOpCreate()
	if err != nil {
		sink.err = err
		sink.release()
		select {
		case req.result <- moveResult{failed: allFailed(req.paths, err)}:
		default:
		}
		return
	}

	cookie, err := ifileOpAdvise(ifOp, sink)
	if err != nil {
		comRelease(ifOp)
		sink.err = err
		sink.release()
		select {
		case req.result <- moveResult{failed: allFailed(req.paths, err)}:
		default:
		}
		return
	}
	sink.cookie = cookie

	const flags = 0x00010000 | 0x0040 | 0x0010 | 0x0400 | 0x0004 | 0x00080000
	if err := ifileOpSetFlags(ifOp, flags); err != nil {
		ifileOpUnadvise(ifOp, cookie)
		comRelease(ifOp)
		sink.err = err
		sink.release()
		select {
		case req.result <- moveResult{failed: allFailed(req.paths, err)}:
		default:
		}
		return
	}

	for _, p := range req.paths {
		norm, err := normalizePath(p)
		if err != nil {
			continue
		}
		item, err := shellItemFromPath(norm)
		if err != nil {
			continue
		}
		_ = ifileOpDeleteItem(ifOp, item)
		comRelease(item)
	}

	currentOp = &runningOp{req: req, sink: sink, ifOp: ifOp}

	if err := ifileOpPerform(ifOp); err != nil {
		currentOp = nil
		ifileOpUnadvise(ifOp, cookie)
		comRelease(ifOp)
		sink.err = err
		sink.release()
		select {
		case req.result <- moveResult{failed: allFailed(req.paths, err)}:
		default:
		}
		return
	}
}

func allFailed(paths []string, err error) map[string]error {
	m := make(map[string]error, len(paths))
	for _, p := range paths {
		m[p] = err
	}
	return m
}
