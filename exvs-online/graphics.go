package main

// "Graphics" line of the PC check: is the game drawing through Vulkan
// (vkd3d-proton + DXVK next to the game) or DirectX 12, and has this PC had the
// 0x10E blue screen that Vulkan mode works around.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var vulkanDLLs = []string{"d3d12.dll", "d3d12core.dll", "dxgi.dll"}

// launcherVulkan is the part of exvs-launcher/vulkan.json the check reads.
type launcherVulkan struct {
	On       bool              `json:"on"`
	Versions map[string]string `json:"versions"`
}

func (c *checker) checkGraphics(r *CheckReport) {
	var present []string
	for _, n := range vulkanDLLs {
		if _, err := os.Stat(filepath.Join(c.gameDir, n)); err == nil {
			present = append(present, n)
		}
	}
	var lv launcherVulkan
	if b, err := os.ReadFile(filepath.Join(c.gameDir, "exvs-launcher", "vulkan.json")); err == nil {
		_ = json.Unmarshal(b, &lv)
	}
	_, vkErr := os.Stat(filepath.Join(c.systemDir, "vulkan-1.dll"))
	var crashes []string
	if c.bugchecks != nil {
		crashes = c.bugchecks()
	}
	crashNote := ""
	if len(crashes) > 0 {
		crashNote = fmt.Sprintf(" This PC had the VIDEO_MEMORY_MANAGEMENT_INTERNAL (0x10E) blue screen %d time(s) in the last 30 days, last on %s.", len(crashes), crashes[0])
	}

	switch {
	case len(present) == len(vulkanDLLs) && runtime.GOOS == "windows" && vkErr != nil:
		r.add("graphics", "Graphics", "fail", "Vulkan mode is on, but this PC has no Vulkan driver (vulkan-1.dll), so the game can't start.",
			"Update your graphics driver, or turn Vulkan mode off in the launcher's Settings.")
	case len(present) == len(vulkanDLLs) && lv.On:
		r.add("graphics", "Graphics", "ok", fmt.Sprintf("Vulkan mode (vkd3d-proton %s, DXVK %s).", lv.Versions["vkd3d-proton"], lv.Versions["DXVK"])+crashNote, "")
	case len(present) == len(vulkanDLLs):
		r.add("graphics", "Graphics", "info", "Vulkan, with files the launcher didn't install ("+strings.Join(present, ", ")+"). Turn on Vulkan mode in Settings to swap them for the official ones."+crashNote,
			"To use the official files, press Turn on Vulkan mode in the launcher's Settings; it keeps yours in a backup.")
	case len(present) > 0:
		r.add("graphics", "Graphics", "warn", "Only part of the Vulkan files are in the game folder ("+strings.Join(present, ", ")+"), which can stop the game from starting."+crashNote,
			"Press Turn on Vulkan mode in the launcher's Settings to install the full set, or remove those files.")
	case len(crashes) > 0:
		r.add("graphics", "Graphics", "warn", "DirectX 12."+crashNote,
			"Turn on Vulkan mode in the launcher's Settings. It stopped this blue screen on the PCs we tested.")
	default:
		r.add("graphics", "Graphics", "info", "DirectX 12. If the PC blue-screens with VIDEO_MEMORY_MANAGEMENT_INTERNAL while playing, turn on Vulkan mode in the launcher's Settings.", "")
	}
}

// recentGPUBugchecks lists the dates (newest first) of 0x10E blue screens in the
// last 30 days, from the System event log. Empty when there are none or the log
// can't be read.
func recentGPUBugchecks() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	ps := `$ErrorActionPreference='SilentlyContinue';` +
		`Get-WinEvent -FilterHashtable @{LogName='System';Id=1001;StartTime=(Get-Date).AddDays(-30)} -MaxEvents 50 |` +
		` Where-Object { $_.Message -match '0x0000010e' } | ForEach-Object { $_.TimeCreated.ToString('yyyy-MM-dd') }`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var dates []string
	for _, l := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			dates = append(dates, l)
		}
	}
	return dates
}
