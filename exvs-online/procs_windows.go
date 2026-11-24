//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// runningExes lists the file names of running programs.
func runningExes() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []string
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for e := windows.Process32First(snap, &pe); e == nil; e = windows.Process32Next(snap, &pe) {
		out = append(out, windows.UTF16ToString(pe.ExeFile[:]))
	}
	return out
}
