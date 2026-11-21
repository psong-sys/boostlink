package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// The checks themselves live in exvs-online.exe (-check -json); the launcher
// only runs it and shows the result.

type CheckItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok | warn | fail | info
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

type CheckResult struct {
	Items          []CheckItem `json:"items"`
	CanFixFirewall bool        `json:"canFixFirewall"`
}

func runCheck() (*CheckResult, error) {
	lc := loadLauncherConfig()
	p := resolvePaths(lc)
	if !fileExists(p.OnlineExe) {
		return nil, fmt.Errorf("exvs-online.exe not found at %s, which runs the checks", p.OnlineExe)
	}
	cfgLC := lc
	if cfgLC.Mode == "offline" {
		cfgLC.Mode = "host"
	}
	if cfgLC.Mode == "friend" && cfgLC.HostIP == "" {
		cfgLC.Mode = "host"
	}
	if err := writeOnlineConfig(cfgLC, p); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.OnlineExe, "-check", "-json", "-config", p.OnlineCfg, "-game", p.GameDir)
	cmd.Dir = p.LauncherDir
	hideWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run() // exit 1 just means something failed
	var res CheckResult
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &res); err != nil || len(res.Items) == 0 {
		if ctx.Err() != nil {
			return nil, errors.New("the check took longer than 60 seconds")
		}
		msg := errb.String()
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("this exvs-online.exe can't run the PC check yet (update it). %s", msg)
	}
	return &res, nil
}

func fixFirewall() (string, error) {
	p := resolvePaths(loadLauncherConfig())
	if !fileExists(p.OnlineExe) {
		return "", fmt.Errorf("exvs-online.exe not found at %s", p.OnlineExe)
	}
	cmd := exec.Command(p.OnlineExe, "-fix-firewall", "-game", p.GameDir)
	cmd.Dir = p.LauncherDir
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(bytes.TrimSpace(out)), err
}
