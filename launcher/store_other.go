//go:build !windows

package main

import "errors"

func startElevated(exe string, args []string, dir string) error {
	return errors.New("Store Team runs on Windows only")
}
