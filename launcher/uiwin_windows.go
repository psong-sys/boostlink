//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procFindWindowW     = user32.NewProc("FindWindowW")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procGetClientRect   = user32.NewProc("GetClientRect")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procIsZoomed        = user32.NewProc("IsZoomed")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow")
	procGetWindowLongW  = user32.NewProc("GetWindowLongW")
	procSetWindowLongW  = user32.NewProc("SetWindowLongW")
	procMonitorFromWin  = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo  = user32.NewProc("GetMonitorInfoW")
)

const (
	gwlStyle      = ^uintptr(15) // GWL_STYLE (-16)
	wsThickFrame  = 0x00040000
	wsMaximizeBox = 0x00010000
	swRestore     = 9
)

// The page is laid out to fit exactly in this many CSS pixels. On bigger
// screens the window is made up to uiMaxScale times larger and the page
// scales itself up to fill it.
const uiClientW, uiClientH = 1104, 721
const uiMaxScale = 1.45

type rect struct{ L, T, R, B int32 }

// fitUIWindow waits for the Edge app window to appear, makes it a fixed-size
// window (no resize border, no maximize), and sizes it as large as fits on
// its monitor (up to uiMaxScale), keeping the page's shape, then centers it. Edge remembers the last size an app
// window had and ignores --window-size, so this sets it directly.
func fitUIWindow() {
	cls, _ := windows.UTF16PtrFromString("Chrome_WidgetWin_1")
	title, _ := windows.UTF16PtrFromString("Boost Link")
	for i := 0; i < 60; i++ {
		time.Sleep(250 * time.Millisecond)
		h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)))
		if h == 0 {
			continue
		}
		if z, _, _ := procIsZoomed.Call(h); z != 0 {
			procShowWindow.Call(h, swRestore)
		}
		st, _, _ := procGetWindowLongW.Call(h, gwlStyle)
		procSetWindowLongW.Call(h, gwlStyle, st&^(wsThickFrame|wsMaximizeBox))
		// Apply the new frame before measuring it (0x27 = NOSIZE|NOMOVE|NOZORDER|FRAMECHANGED).
		procSetWindowPos.Call(h, 0, 0, 0, 0, 0, 0x27)
		dpi := uintptr(96)
		if procGetDpiForWindow.Find() == nil {
			if d, _, _ := procGetDpiForWindow.Call(h); d != 0 {
				dpi = d
			}
		}
		var wr, cr rect
		procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
		procGetClientRect.Call(h, uintptr(unsafe.Pointer(&cr)))
		fw, fh := (wr.R-wr.L)-(cr.R-cr.L), (wr.B-wr.T)-(cr.B-cr.T)
		baseW, baseH := float64(uiClientW*dpi/96), float64(uiClientH*dpi/96)
		scale := uiMaxScale
		var mi struct {
			Size    uint32
			Monitor rect
			Work    rect
			Flags   uint32
		}
		mi.Size = uint32(unsafe.Sizeof(mi))
		haveWork := false
		if mon, _, _ := procMonitorFromWin.Call(h, 2); mon != 0 { // MONITOR_DEFAULTTONEAREST
			if ok, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
				haveWork = true
				ww, wh := float64(mi.Work.R-mi.Work.L), float64(mi.Work.B-mi.Work.T)
				scale = min(scale, (ww*0.95-float64(fw))/baseW, (wh*0.95-float64(fh))/baseH)
			}
		}
		scale = max(scale, 1)
		w := int32(baseW*scale) + fw
		ht := int32(baseH*scale) + fh
		const swpNoMove, swpNoZOrder, swpNoActivate, swpFrameChanged = 0x2, 0x4, 0x10, 0x20
		if haveWork {
			x := mi.Work.L + (mi.Work.R-mi.Work.L-w)/2
			y := mi.Work.T + (mi.Work.B-mi.Work.T-ht)/2
			procSetWindowPos.Call(h, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(ht), swpNoZOrder|swpNoActivate|swpFrameChanged)
		} else {
			procSetWindowPos.Call(h, 0, 0, 0, uintptr(w), uintptr(ht), swpNoMove|swpNoZOrder|swpNoActivate|swpFrameChanged)
		}
		return
	}
}
