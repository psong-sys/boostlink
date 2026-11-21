//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// A job object with KILL_ON_JOB_CLOSE: if the launcher dies for any reason,
// Windows closes the background programs too.
var job windows.Handle

func init() {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return
	}
	job = h
}

func addToJob(cmd *exec.Cmd) {
	if job == 0 || cmd.Process == nil {
		return
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(ph)
	_ = windows.AssignProcessToJobObject(job, ph)
}

func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	hideWindow(c)
	return c
}

func processRunning(image string) bool {
	out, err := hiddenCmd("tasklist", "/FI", "IMAGENAME eq "+image, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(image))
}

func killByName(image string) {
	_ = hiddenCmd("taskkill", "/IM", image, "/T", "/F").Run()
}

func killTree(pid int) error {
	return hiddenCmd("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

func messageBox(title, text string, isError bool) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	var flags uint32 = windows.MB_OK | windows.MB_ICONINFORMATION
	if isError {
		flags = windows.MB_OK | windows.MB_ICONERROR
	}
	windows.MessageBox(0, m, t, flags)
}

func findEdge() string {
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// openUI shows the settings page in an Edge app window (no tabs or address
// bar), falling back to the default browser.
func openUI(url string) {
	if edge := findEdge(); edge != "" {
		if exec.Command(edge, "--app="+url, "--window-size=1120,800").Start() == nil {
			return
		}
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// createShortcuts puts "EXVS2 Play" and "EXVS2 Launcher" on the desktop.
func createShortcuts() (string, error) {
	exe, _ := os.Executable()
	ps := `$d=[Environment]::GetFolderPath('Desktop'); $s=New-Object -ComObject WScript.Shell;` +
		`$l=$s.CreateShortcut((Join-Path $d 'Boost Link Play.lnk')); $l.TargetPath=$env:EXE; $l.Arguments='--play'; $l.WorkingDirectory=$env:DIR; $l.IconLocation=$env:GAME+',0'; $l.Save();` +
		`$l=$s.CreateShortcut((Join-Path $d 'Boost Link.lnk')); $l.TargetPath=$env:EXE; $l.WorkingDirectory=$env:DIR; $l.IconLocation=$env:GAME+',0'; $l.Save(); Write-Output $d`
	c := hiddenCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	lc := loadLauncherConfig()
	p := resolvePaths(lc)
	icon := p.GameExe
	if !fileExists(icon) {
		icon = exe
	}
	c.Env = append(os.Environ(), "EXE="+exe, "DIR="+filepath.Dir(exe), "GAME="+icon)
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// portOwner names the process listening on a local TCP port, like
// "exvs-online.exe (PID 1234)", or "" if it can't tell.
func portOwner(port int) string {
	out, err := hiddenCmd("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return ""
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		// A listening socket has no remote end. (The state word itself is
		// translated on non-English Windows, so it isn't checked.)
		if len(f) < 5 || !strings.HasSuffix(f[2], ":0") || !strings.HasSuffix(f[1], suffix) {
			continue
		}
		pid := f[4]
		name := "PID " + pid
		if t, err := hiddenCmd("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output(); err == nil {
			if cols := strings.Split(strings.TrimSpace(string(t)), ","); len(cols) > 1 {
				name = strings.Trim(cols[0], "\"") + " (PID " + pid + ")"
			}
		}
		return name
	}
	return ""
}

func openURL(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// missingVCRT2010 reports whether the Visual C++ 2010 x64 runtime the Live
// Monitor needs (msvcr100.dll) is missing.
func missingVCRT2010() bool {
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	return !fileExists(filepath.Join(sys, "System32", "msvcr100.dll"))
}
