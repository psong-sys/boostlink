package main

import (
	"bytes"
	"os"
	"path/filepath"
)

// lockCardServer makes Server-Infinite listen on 127.0.0.1 only. It ships
// listening on every network adapter (0.0.0.0), which lets anyone on the same
// Wi-Fi or VPN read and change the cards on this PC. The game, the Live Monitor
// and exvs-online all reach it on 127.0.0.1. The first change keeps a copy as
// kestrel.json.original. It returns a log line when it changed something.
func lockCardServer(p Paths) string {
	f := filepath.Join(filepath.Dir(p.ServerExe), "Configurations", "kestrel.json")
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	nb := lockKestrel(b)
	if bytes.Equal(nb, b) {
		return ""
	}
	if _, err := os.Stat(f + ".original"); os.IsNotExist(err) {
		_ = os.WriteFile(f+".original", b, 0644)
	}
	if err := os.WriteFile(f, nb, 0644); err != nil {
		return "couldn't limit the card server to this PC: " + err.Error()
	}
	return "Card server limited to this PC (127.0.0.1) in Server\\Configurations\\kestrel.json"
}

func lockKestrel(b []byte) []byte {
	for _, any := range []string{"0.0.0.0", "*", "+", "[::]"} {
		b = bytes.ReplaceAll(b, []byte(`"http://`+any+`:`), []byte(`"http://127.0.0.1:`))
	}
	return b
}
