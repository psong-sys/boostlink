//go:build windows

package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procMonitorFromWindow            = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW              = user32.NewProc("GetMonitorInfoW")
	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
)

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

// The game's window: a visible top-level window of vsac29_Release.exe that is
// at least 640 px wide (skips any small helper windows).
var (
	gameWnd     uintptr
	gameWndProc = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if !isGameProcess(pid) {
			return 1
		}
		var r rect
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		if r.R-r.L < 640 {
			return 1
		}
		gameWnd = hwnd
		return 0
	})
)

func isGameProcess(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:n])), gameExeName)
}

func findGameWindow() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	gameWnd = 0
	procEnumWindows.Call(gameWndProc, 0)
	return gameWnd
}

// fillScreen stretches the game's borderless window over the whole monitor it
// is on. The game always makes a 1920x1080 window and centers it, which leaves
// a border on bigger screens. The picture scales with the window, and the
// game keeps its 60 FPS limit (unlike true fullscreen). It runs until done is
// closed, re-applying the size if the game moves the window back, at most a
// few times so it can't fight the game forever.
func fillScreen(done <-chan struct{}, logf func(string, ...any)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Work in real pixels even when Windows display scaling is on.
	if procSetThreadDpiAwarenessContext.Find() == nil {
		procSetThreadDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	}
	fixes := 0
	for {
		select {
		case <-done:
			return
		case <-time.After(time.Second):
		}
		h := findGameWindow()
		if h == 0 {
			continue
		}
		mon, _, _ := procMonitorFromWindow.Call(h, 2) // MONITOR_DEFAULTTONEAREST
		mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
			continue
		}
		var r rect
		procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
		m := mi.Monitor
		if r == m {
			continue
		}
		if fixes >= 5 {
			logf("The game keeps resizing its window, so the launcher stopped stretching it to fill the screen")
			return
		}
		fixes++
		const swpNoZOrder, swpNoActivate, swpFrameChanged = 0x4, 0x10, 0x20
		procSetWindowPos.Call(h, 0, uintptr(m.L), uintptr(m.T), uintptr(m.R-m.L), uintptr(m.B-m.T), swpNoZOrder|swpNoActivate|swpFrameChanged)
		logf("Stretched the game window from %dx%d to fill the screen (%dx%d)", r.R-r.L, r.B-r.T, m.R-m.L, m.B-m.T)
	}
}
