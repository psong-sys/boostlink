//go:build windows

package main

import (
	"strings"

	"golang.org/x/sys/windows"
)

// startElevated runs exe as admin (Windows shows its admin prompt), hidden.
func startElevated(exe string, args []string, dir string) error {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		q[i] = a
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(q, " "))
	wd, _ := windows.UTF16PtrFromString(dir)
	return windows.ShellExecute(0, verb, file, params, wd, windows.SW_HIDE)
}
