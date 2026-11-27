package main

// -light-lm: run the Light Live Monitor until killed. The launcher starts it in place
// of vsac29_Release_LM.exe and reads GET /status to see whether the game linked up.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// adapterIPv4 finds the IPv4 address of a network adapter by name (Windows' name,
// e.g. "Ethernet" or "Boost Link"), or returns ip itself if it is an address.
func adapterIPv4(name string) (net.IP, error) {
	if ip := net.ParseIP(name); ip != nil && ip.To4() != nil {
		return ip.To4(), nil
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifs {
		if !strings.EqualFold(i.Name, name) {
			continue
		}
		as, _ := i.Addrs()
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return n.IP.To4(), nil
			}
		}
		return nil, fmt.Errorf("adapter %q has no IPv4 address yet", name)
	}
	return nil, fmt.Errorf("no adapter called %q", name)
}

func runLightLM(adapter, gameDir, statusAddr, serial string) error {
	var ip net.IP
	var err error
	for i := 0; i < 30; i++ { // the Boost Link adapter can take a few seconds to get its IP
		if ip, err = adapterIPv4(adapter); err == nil {
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
	lm := &LightLM{IP: ip}
	resp := &lmResponse{Settings: settings, Serial: serial, lm: lm,
		Logf: func(f string, a ...any) { log.Printf("light LM: "+f, a...) }}
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
