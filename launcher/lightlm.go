package main

// Light Live Monitor: exvs-online -light-lm answers the game in place of
// vsac29_Release_LM.exe (a second full copy of the game). If the game hasn't linked
// to it 25 seconds after starting, the launcher closes the game, starts the real
// Live Monitor and starts the game again, and stays on the real one from then on.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	lightLMStatusAddr = "127.0.0.1:28095"
	lightLMWait       = 25 * time.Second
)

type lightLMStatus struct {
	IP         string `json:"ip"`
	Games      int    `json:"games"`
	EverLinked bool   `json:"everLinked"`
}

func getLightLMStatus() (lightLMStatus, error) {
	var st lightLMStatus
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + lightLMStatusAddr + "/status")
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&st)
	return st, err
}

func (r *Runner) startLightLM(p Paths) error {
	r.setStep("lm", "running", "light")
	r.stage("Starting the Light Live Monitor")
	if !fileExists(p.OnlineExe) {
		return fmt.Errorf("the Light Live Monitor is part of exvs-online.exe, which isn't at %s", p.OnlineExe)
	}
	adapter := ""
	if ini, err := LoadIni(p.LMIni); err == nil {
		adapter, _ = ini.Get("config", "InterfaceName")
	}
	if strings.TrimSpace(adapter) == "" {
		adapter = suggestAdapter()
	}
	if adapter == "" {
		return errors.New("the Light Live Monitor needs a network adapter: pick one in SETTINGS")
	}
	args := []string{"-light-lm", "-adapter", adapter, "-game", p.GameDir, "-status", lightLMStatusAddr}
	exited, err := r.startHelper("light-live-monitor", p.OnlineExe, args, p.LauncherDir, p)
	if err != nil {
		return fmt.Errorf("starting the Light Live Monitor: %w", err)
	}
	r.mu.Lock()
	r.lightLM = r.helper[len(r.helper)-1]
	r.mu.Unlock()
	deadline := time.Now().Add(20 * time.Second) // the Store Team adapter can be slow to get its IP
	for {
		if st, err := getLightLMStatus(); err == nil {
			r.logf("Light Live Monitor is up on %s (%s)", st.IP, adapter)
			break
		}
		select {
		case <-exited:
			r.lightLM = nil
			return errors.New("the Light Live Monitor closed right after starting (see logs\\light-live-monitor.log). Set Live Monitor back to \"Start it hidden\" in SETTINGS")
		case <-r.stop:
			return errors.New("stopped")
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return errors.New("the Light Live Monitor didn't come up in 20 seconds (see logs\\light-live-monitor.log)")
		}
	}
	r.setStep("lm", "done", "light")
	return nil
}

func (r *Runner) stopLightLM() {
	r.mu.Lock()
	c := r.lightLM
	r.lightLM = nil
	r.mu.Unlock()
	if c != nil && c.Process != nil {
		_ = killTree(c.Process.Pid)
	}
}

// watchLightLM closes fellBack (and the game) if the game never links up.
func (r *Runner) watchLightLM(game *exec.Cmd, gameDone <-chan struct{}, fellBack chan<- struct{}) {
	deadline := time.After(lightLMWait)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-gameDone:
			return
		case <-tick.C:
			if st, err := getLightLMStatus(); err == nil && st.EverLinked {
				r.logf("The game linked to the Light Live Monitor")
				return
			}
		case <-deadline:
			r.logf("The game didn't link to the Light Live Monitor in %v. Restarting it with the normal Live Monitor, and keeping that setting", lightLMWait)
			lc := loadLauncherConfig()
			lc.LiveMonitor = "hide"
			if err := saveLauncherConfig(lc); err != nil {
				r.logf("Couldn't save the Live Monitor setting: %v", err)
			}
			close(fellBack)
			if game.Process != nil {
				_ = killTree(game.Process.Pid)
			}
			killByName(gameExeName)
			return
		}
	}
}
