//go:build windows

package main

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")

	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
)

const (
	swHide         = 0
	swShowMinNoAct = 7
)

func mkGUID(a uint32, b, c uint16, d [8]byte) windows.GUID {
	return windows.GUID{Data1: a, Data2: b, Data3: c, Data4: d}
}

// One callback for EnumWindows, made once: Windows only allows a limited
// number of callbacks per process, so it can't be created on every call.
var (
	enumMu   sync.Mutex
	enumPid  int
	enumHow  string
	enumN    int
	enumProc = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if int(wpid) != enumPid {
			return 1
		}
		if vis, _, _ := procIsWindowVisible.Call(hwnd); vis == 0 {
			return 1
		}
		if enumHow == "minimize" {
			if ic, _, _ := procIsIconic.Call(hwnd); ic == 0 {
				procShowWindow.Call(hwnd, swShowMinNoAct)
				enumN++
			}
		} else {
			procShowWindow.Call(hwnd, swHide)
			enumN++
		}
		return 1
	})
)

// hideWindowsOf hides (or minimizes) every visible top-level window owned by
// pid and returns how many windows it changed.
func hideWindowsOf(pid int, how string) int {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPid, enumHow, enumN = pid, how, 0
	procEnumWindows.Call(enumProc, 0)
	return enumN
}

// ---- per-app mute through the Windows audio session API ----

type comObj struct{ vtbl *[64]uintptr }

func (o *comObj) call(i int, args ...uintptr) uintptr {
	a := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[i], a...)
	return r
}
func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

var (
	clsidMMDeviceEnumerator = mkGUID(0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E})
	iidIMMDeviceEnumerator  = mkGUID(0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6})
	iidIAudioSessionMgr2    = mkGUID(0x77AA99A0, 0x1BD6, 0x484F, [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F})
	iidIAudioSessionCtl2    = mkGUID(0xBFB7FF88, 0x7239, 0x4FC9, [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D})
	iidISimpleAudioVolume   = mkGUID(0x87CE5498, 0x68D6, 0x44E5, [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8})
)

// muteProcess mutes every audio session that belongs to pid, on every active
// output device. It returns how many sessions it muted. Must run on a thread
// with COM initialized.
func muteProcess(pid int) int {
	var enum *comObj
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, 23, /*CLSCTX_ALL*/
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum))); int32(hr) < 0 || enum == nil {
		return 0
	}
	defer enum.release()
	var coll *comObj
	// EnumAudioEndpoints(eRender=0, DEVICE_STATE_ACTIVE=1)
	if int32(enum.call(3, 0, 1, uintptr(unsafe.Pointer(&coll)))) < 0 || coll == nil {
		return 0
	}
	defer coll.release()
	var nDev uint32
	coll.call(3, uintptr(unsafe.Pointer(&nDev)))
	muted := 0
	for d := uint32(0); d < nDev; d++ {
		var dev *comObj
		if int32(coll.call(4, uintptr(d), uintptr(unsafe.Pointer(&dev)))) < 0 || dev == nil {
			continue
		}
		var mgr *comObj
		if int32(dev.call(3, uintptr(unsafe.Pointer(&iidIAudioSessionMgr2)), 23, 0, uintptr(unsafe.Pointer(&mgr)))) >= 0 && mgr != nil {
			var se *comObj
			if int32(mgr.call(5, uintptr(unsafe.Pointer(&se)))) >= 0 && se != nil {
				var cnt int32
				se.call(3, uintptr(unsafe.Pointer(&cnt)))
				for i := int32(0); i < cnt; i++ {
					var ctl *comObj
					if int32(se.call(4, uintptr(i), uintptr(unsafe.Pointer(&ctl)))) < 0 || ctl == nil {
						continue
					}
					var ctl2 *comObj
					if int32(ctl.call(0, uintptr(unsafe.Pointer(&iidIAudioSessionCtl2)), uintptr(unsafe.Pointer(&ctl2)))) >= 0 && ctl2 != nil {
						var spid uint32
						ctl2.call(14, uintptr(unsafe.Pointer(&spid)))
						if int(spid) == pid {
							var vol *comObj
							if int32(ctl.call(0, uintptr(unsafe.Pointer(&iidISimpleAudioVolume)), uintptr(unsafe.Pointer(&vol)))) >= 0 && vol != nil {
								var m int32
								vol.call(6, uintptr(unsafe.Pointer(&m)))
								if m == 0 {
									vol.call(5, 1, 0)
									muted++
								}
								vol.release()
							}
						}
						ctl2.release()
					}
					ctl.release()
				}
				se.release()
			}
			mgr.release()
		}
		dev.release()
	}
	return muted
}

// tameProcess keeps pid's windows hidden and its audio muted until it exits
// (exited is closed) . The game may create its window and audio late, and may
// re-show its window, so it keeps checking.
func tameProcess(pid int, how string, mute bool, exited <-chan struct{}, logf func(string, ...any)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, 0 /*COINIT_MULTITHREADED*/)
	defer procCoUninitialize.Call()
	hidLogged, muteLogged := false, false
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-exited:
			return
		case <-t.C:
		}
		if how != "show" {
			if hideWindowsOf(pid, how) > 0 && !hidLogged {
				logf("Live Monitor window %s", map[string]string{"hide": "hidden", "minimize": "minimized"}[how])
				hidLogged = true
			}
		}
		if mute && i%3 == 0 {
			if muteProcess(pid) > 0 && !muteLogged {
				logf("Live Monitor muted")
				muteLogged = true
			}
		}
	}
}
