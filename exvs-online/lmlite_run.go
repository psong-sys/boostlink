package main

// -light-lm: run the Light Live Monitor until killed. The launcher starts it in place
// of vsac29_Release_LM.exe and reads GET /status to see whether the game linked up.

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adapterIPv4 finds the IPv4 address (and directed broadcast) of a network adapter by
// name (Windows' name, e.g. "Ethernet" or "Boost Link"), or takes an address as is.
func adapterIPv4(name string) (net.IP, net.IP, error) {
	if ip := net.ParseIP(name); ip != nil && ip.To4() != nil {
		return ip.To4(), nil, nil
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	for _, i := range ifs {
		if !strings.EqualFold(i.Name, name) {
			continue
		}
		as, _ := i.Addrs()
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				ip := n.IP.To4()
				bc := make(net.IP, 4)
				m := n.Mask
				if len(m) == 16 {
					m = m[12:]
				}
				for i := range bc {
					bc[i] = ip[i] | ^m[i]
				}
				return ip, bc, nil
			}
		}
		return nil, nil, fmt.Errorf("adapter %q has no IPv4 address yet", name)
	}
	return nil, nil, fmt.Errorf("no adapter called %q", name)
}

// lmDefaultSerial: a 12-digit LM serial that stays the same on this PC. The real LM
// sends its PCB serial (12 digits, e.g. 2895xxxxxxxx); the new build retries its boot
// questions every 5 s and then gives 21-01 when it is blank.
func lmDefaultSerial() string {
	h := fnv.New32a()
	name, _ := os.Hostname()
	h.Write([]byte("boost-link-lm:" + name))
	return fmt.Sprintf("2895%08d", h.Sum32()%100000000)
}

func runLightLM(adapter, gameDir, statusAddr, serial string) error {
	var ip, bc net.IP
	var err error
	for i := 0; i < 30; i++ { // the Boost Link adapter can take a few seconds to get its IP
		if ip, bc, err = adapterIPv4(adapter); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if gameDir == "" {
		return errors.New("-game is needed (the game folder, for 29\\testmode_lm.json)")
	}
	settings, err := loadLMSettings(filepath.Join(gameDir, "29", "testmode_lm.json"))
	if err != nil {
		log.Printf("light LM: using default settings (%v)", err)
	}
	if serial == "" {
		serial = lmDefaultSerial()
	}
	lm := &LightLM{IP: ip, Bcast: bc, Verbose: true}
	resp := &lmResponse{Settings: settings, Serial: serial, lm: lm,
		Logf: func(f string, a ...any) { log.Printf("light LM: "+f, a...) }}
	rp := os.Getenv("EXVS_LM_REPLAY")
	if exe, err := os.Executable(); err == nil && rp == "" && fileExistsLM(filepath.Join(filepath.Dir(exe), "lm-replay.jsonl")) {
		rp = filepath.Join(filepath.Dir(exe), "lm-replay.jsonl")
	}
	if rp != "" {
		if resp.Replay, err = loadLMReplay(rp); err != nil {
			log.Printf("light LM: replay file: %v", err)
		} else {
			log.Printf("light LM: debug: boot replies from %s (%d recorded)", rp, len(resp.Replay))
		}
	}
	lm.Responder = resp
	started := time.Now()
	if err := lm.Start(); err != nil {
		return err
	}
	if statusAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"ip": ip.String(), "games": lm.Cabs(), "everLinked": lm.EverLinked(),
				"uptime": int(time.Since(started).Seconds()),
			})
		})
		go func() {
			if err := http.ListenAndServe(statusAddr, mux); err != nil {
				log.Printf("light LM: status page: %v", err)
			}
		}()
	}
	select {}
}

func fileExistsLM(p string) bool { _, err := os.Stat(p); return err == nil }
