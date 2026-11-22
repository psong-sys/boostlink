//go:build !windows

package main

import "errors"

func runProbe(string) error { return errors.New("the game probe only works on Windows") }
