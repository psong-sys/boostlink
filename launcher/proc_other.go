//go:build !windows

package main

// Stand-ins so the launcher builds and its tests run on Linux/macOS. The real
// launcher only targets Windows.

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {}
func addToJob(cmd *exec.Cmd)   {}

func processRunning(image string) bool {
	out, err := exec.Command("pgrep", "-f", image).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func killByName(image string) { _ = exec.Command("pkill", "-f", image).Run() }

func killTree(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

func messageBox(title, text string, isError bool) { fmt.Println(title+":", text) }

func openUI(url string) { fmt.Println("open", url) }

func createShortcuts() (string, error) { return "", errors.New("only on Windows") }

func tameProcess(pid int, how string, mute bool, exited <-chan struct{}, logf func(string, ...any)) {}

func portOwner(port int) string { return "" }

func openURL(url string)                 { fmt.Println("open", url) }
func missingVCRT2010() bool              { return false }
func fitUIWindow()                       {}
func openAppWindow(url string, w, h int) { fmt.Println("open", url, w, h) }
