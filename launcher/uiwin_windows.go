//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procIsZoomed            = user32.NewProc("IsZoomed")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procMonitorFromWin      = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
)

const swRestore = 9

// The page is laid out to fit exactly in this many CSS pixels. On bigger
// screens the window is made up to uiMaxScale times larger and the page
// scales itself up to fill it.
const uiClientW, uiClientH = 1104, 721
const uiMaxScale = 1.45

const uiTitle = "Boost Link"

type rect struct{ L, T, R, B int32 }

// uiWindows lists the visible Edge app windows showing the launcher page.
func uiWindows() []windows.HWND {
	var found []windows.HWND
	cb := windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		if !windows.IsWindowVisible(h) {
			return 1
		}
		var cls [64]uint16
		if n, _ := windows.GetClassName(h, &cls[0], int32(len(cls))); n == 0 || windows.UTF16ToString(cls[:n]) != "Chrome_WidgetWin_1" {
			return 1
		}
		var t [64]uint16
		n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&t[0])), uintptr(len(t)))
		if windows.UTF16ToString(t[:n]) == uiTitle {
			found = append(found, h)
		}
		return 1
	})
	_ = windows.EnumWindows(cb, nil)
	return found
}

// focusUIWindow brings an already open launcher window to the front. It
// reports false when there is none (for example, the window was closed while
// the game kept the launcher running).
func focusUIWindow() bool {
	ws := uiWindows()
	if len(ws) == 0 {
		return false
	}
	h := uintptr(ws[0])
	if m, _, _ := procIsIconic.Call(h); m != 0 {
		procShowWindow.Call(h, swRestore)
	}
	procSetForegroundWindow.Call(h)
	return true
}

// uiProfileDir is the launcher's own Edge profile. Using it instead of the
// person's everyday Edge profile keeps their browser's zoom and window memory
// out of the launcher, and lets the launcher reset them.
func uiProfileDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "BoostLink", "edge-ui")
}

// resetUIProfile clears the page zoom and remembered window size Edge keeps
// for the launcher, so a window that once came up zoomed in or the wrong size
// never comes back that way. Only call it while no launcher window is open: a
// running Edge would just write its own copy back.
func resetUIProfile(dir string) {
	p := filepath.Join(dir, "Default", "Preferences")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var prefs map[string]any
	if json.Unmarshal(b, &prefs) != nil {
		return
	}
	if part, ok := prefs["partition"].(map[string]any); ok {
		delete(part, "per_host_zoom_levels")
		delete(part, "default_zoom_level")
	}
	if br, ok := prefs["browser"].(map[string]any); ok {
		delete(br, "app_window_placement")
		delete(br, "window_placement")
	}
	prefs["background_mode"] = map[string]any{"enabled": false}
	if out, err := json.Marshal(prefs); err == nil {
		_ = os.WriteFile(p, out, 0o644)
	}
}

// edgeAppCmd builds the command that opens url as an Edge app window in the
// launcher's own profile.
func edgeAppCmd(edge, url string) *exec.Cmd {
	dir := uiProfileDir()
	_ = os.MkdirAll(dir, 0o755)
	resetUIProfile(dir)
	return exec.Command(edge, "--app="+url, "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--disable-sync",
		"--window-size=1120,760")
}

// fitUIWindow waits for a new Edge app window (one not in before) to appear
// and sizes it as large as fits on its monitor (up to uiMaxScale), keeping the
// page's shape, then centers it.
//
// It used to also strip the resize border with SetWindowLong and
// SWP_FRAMECHANGED, and a second launcher started while one was open ran two
// of these at once on whichever "Boost Link" window it found first. Changing
// the frame of an Edge window under it left Edge drawing the page shifted and
// cropped ("zoomed in"). Now only the size and position are set, once, on the
// window this launch opened.
func fitUIWindow(before map[windows.HWND]bool) {
	var h windows.HWND
	for i := 0; i < 60 && h == 0; i++ {
		time.Sleep(250 * time.Millisecond)
		for _, w := range uiWindows() {
			if !before[w] {
				h = w
				break
			}
		}
	}
	if h == 0 {
		return
	}
	// Let Edge finish placing the window itself before moving it.
	time.Sleep(400 * time.Millisecond)
	if z, _, _ := procIsZoomed.Call(uintptr(h)); z != 0 {
		procShowWindow.Call(uintptr(h), swRestore)
	}
	dpi := uintptr(96)
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(uintptr(h)); d != 0 {
			dpi = d
		}
	}
	var wr, cr rect
	procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&wr)))
	procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&cr)))
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
	if mon, _, _ := procMonitorFromWin.Call(uintptr(h), 2); mon != 0 { // MONITOR_DEFAULTTONEAREST
		if ok, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
			haveWork = true
			ww, wh := float64(mi.Work.R-mi.Work.L), float64(mi.Work.B-mi.Work.T)
			scale = min(scale, (ww*0.95-float64(fw))/baseW, (wh*0.95-float64(fh))/baseH)
		}
	}
	scale = max(scale, 1)
	w := int32(baseW*scale) + fw
	ht := int32(baseH*scale) + fh
	const swpNoMove, swpNoZOrder, swpNoActivate = 0x2, 0x4, 0x10
	if haveWork {
		x := mi.Work.L + (mi.Work.R-mi.Work.L-w)/2
		y := mi.Work.T + (mi.Work.B-mi.Work.T-ht)/2
		procSetWindowPos.Call(uintptr(h), 0, uintptr(x), uintptr(y), uintptr(w), uintptr(ht), swpNoZOrder|swpNoActivate)
	} else {
		procSetWindowPos.Call(uintptr(h), 0, 0, 0, uintptr(w), uintptr(ht), swpNoMove|swpNoZOrder|swpNoActivate)
	}
}
