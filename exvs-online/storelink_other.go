//go:build !windows

package main

import "errors"

func runStoreLink(server, code, statusAddr string, parent int) error {
	return errors.New("Store Team mode runs on Windows only")
}
