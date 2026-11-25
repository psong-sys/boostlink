package main

// Store Team (Route Battle co-op over the internet).
//
// The game links cabinets in one arcade through one Live Monitor on the shop LAN.
// With Store Team on, exvs-online -storelink (started with an admin prompt) adds a
// virtual network card called "Boost Link" that joins everyone with the same team
// code into one private network through the match server. The game and the Live
// Monitor use that card for this run (InterfaceName=Boost Link). One player is the
// store ("host": runs the Live Monitor); the others join it and run no Live Monitor
// of their own, since two Live Monitors in one store give error 21-11.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	storeAdapter    = "Boost Link"
	storeStatusAddr = "127.0.0.1:28094"
)

// storeTeamOn: Store Team only works in Online mode with a team code.
func storeTeamOn(lc LauncherConfig) bool {
	return lc.Mode == "friend" && strings.TrimSpace(lc.TeamCode) != "" &&
		(lc.StoreTeam == "host" || lc.StoreTeam == "join")
}

type storeStatus struct {
	State   string `json:"state"`
	IP      string `json:"ip"`
	Members int    `json:"members"`
	Error   string `json:"error"`
}

var storeClient = &http.Client{Timeout: 2 * time.Second}

func getStoreStatus() (storeStatus, error) {
	var st storeStatus
	resp, err := storeClient.Get("http://" + storeStatusAddr + "/status")
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&st)
	return st, err
}

func stopStoreLink() {
	resp, err := storeClient.Post("http://"+storeStatusAddr+"/stop", "text/plain", nil)
	if err == nil {
		resp.Body.Close()
	}
}

// setGameAdapter writes InterfaceName to config.ini and lmnfig.ini without touching
// anything else (and without a backup: it is put back after the run).
func setGameAdapter(p Paths, name string) error {
	for _, path := range []string{p.ConfigIni, p.LMIni} {
		f, err := LoadIni(path)
		if err != nil {
			if path == p.LMIni {
				continue
			}
			return err
		}
		ip := f.Comment("config", "IpAddress") // can't have both
		if v, _ := f.Get("config", "InterfaceName"); v == name && !ip {
			continue
		}
		f.Set("config", "InterfaceName", name)
		if err := f.Save(path); err != nil {
			return err
		}
	}
	return nil
}

// startStoreTeam starts the Store Team network and points the game at it. The
// returned func undoes it.
func (r *Runner) startStoreTeam(lc LauncherConfig, p Paths, realAdapter string) (func(), error) {
	r.setStep("store", "running", "")
	r.stage("Starting Store Team (click Yes on the Windows admin prompt)")
	if _, err := getStoreStatus(); err == nil {
		r.logf("A Store Team network from an earlier run is still up, restarting it")
		stopStoreLink()
		time.Sleep(2 * time.Second)
	}
	args := []string{"-storelink", "-server", lc.HostIP, "-team", strings.TrimSpace(lc.TeamCode),
		"-parent", fmt.Sprint(os.Getpid()), "-log", filepath.Join(p.LogDir, "store-team.log")}
	_ = os.MkdirAll(p.LogDir, 0755)
	if err := startElevated(p.OnlineExe, args, filepath.Dir(p.OnlineExe)); err != nil {
		return nil, fmt.Errorf("Store Team needs the admin prompt answered with Yes: %w", err)
	}
	undo := func() {
		stopStoreLink()
		if err := setGameAdapter(p, realAdapter); err != nil {
			r.logf("Couldn't put the network adapter back to %s: %v", realAdapter, err)
		}
	}
	lastIP := ""
	deadline := time.Now().Add(90 * time.Second)
	last := ""
	for {
		if r.stopped() {
			undo()
			return nil, errors.New("stopped")
		}
		st, err := getStoreStatus()
		if err == nil {
			if st.State == "error" {
				undo()
				return nil, fmt.Errorf("Store Team: %s", st.Error)
			}
			if st.State == "connected" && st.IP != "" {
				lastIP = st.IP
				r.logf("Store Team network up: this PC is %s, %d player(s) on code %q so far", st.IP, st.Members, strings.TrimSpace(lc.TeamCode))
				break
			}
			if st.State != last {
				last = st.State
				if st.State == "connecting" {
					r.stage("Store Team: connecting to the server")
				}
			}
		}
		if time.Now().After(deadline) {
			undo()
			if err != nil {
				return nil, errors.New("Store Team didn't start (was the admin prompt answered?). Details in logs\\store-team.log")
			}
			return nil, errors.New("Store Team couldn't reach the server. Details in logs\\store-team.log")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := setGameAdapter(p, storeAdapter); err != nil {
		undo()
		return nil, fmt.Errorf("pointing the game at the Boost Link adapter: %w", err)
	}
	// Every cabinet in a store needs its own GAME PCB ID, or the Live Monitor stops
	// the newcomer with 21-14 "Duplicate GAME PCB ID". Use the team address's last
	// number (1-8), which the relay hands out uniquely per team.
	if id, ok := storeCabinetID(lastIP); ok {
		pcb := filepath.Join(p.GameDir, "29", "testmode_pcb.json")
		if old, err := setSvdInt(pcb, networkIDKey, id); err != nil {
			r.logf("Couldn't set this cabinet's ID for the store: %v", err)
		} else {
			if old != id {
				r.logf("Cabinet ID for the store: %d (was %d, put back after the game)", id, old)
			}
			prev := undo
			undo = func() {
				prev()
				if old != id {
					if _, err := setSvdInt(pcb, networkIDKey, old); err != nil {
						r.logf("Couldn't put the cabinet ID back to %d: %v", old, err)
					}
				}
			}
		}
	}
	note := "you're the store"
	if lc.StoreTeam == "join" {
		note = "joining your friend's store"
	}
	r.setStep("store", "done", note)
	return undo, nil
}

// storeCabinetID turns a team address (10.77.0.N) into a cabinet ID N.
func storeCabinetID(ip string) (int, bool) {
	i := strings.LastIndexByte(ip, '.')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(ip[i+1:])
	if err != nil || n < 1 || n > 15 {
		return 0, false
	}
	return n, true
}
