package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Newer game builds ship testmode files with stale bytes after the svd1 payload.
func TestReadSvdIgnoresTrailingBytes(t *testing.T) {
	payload := []byte(`{"storage": {".TestModeParam.gameServerPcb.settingFile.[0].group.[0].battleMode": {"value": 1, "type": 1}}}`)
	good := encodeSvd(make([]byte, svdHeaderLen), payload)
	dir := t.TempDir()
	path := filepath.Join(dir, "testmode_pcb.json")
	os.WriteFile(path, append(append([]byte(nil), good...), []byte("1\n} 1\n}")...), 0644)
	_, p, err := readSvd(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != string(payload) {
		t.Fatalf("payload %q", p)
	}
	if _, changed, err := ensureOnlineMode(path, filepath.Join(dir, "b")); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(path)
	if len(b) != svdHeaderLen+len(payload) {
		t.Errorf("rewritten file still has %d trailing bytes", len(b)-svdHeaderLen-len(payload))
	}
	os.WriteFile(path, good[:len(good)-3], 0644)
	if _, _, err := readSvd(path); err == nil {
		t.Error("truncated file accepted")
	}
}

func TestFindCardServer(t *testing.T) {
	dir := t.TempDir()
	if got := findCardServer(dir); got != filepath.Join(dir, "Server", "Server-Infinite.exe") {
		t.Errorf("default = %s", got)
	}
	os.MkdirAll(filepath.Join(dir, "server"), 0755)
	os.WriteFile(filepath.Join(dir, "server", "Server.exe"), nil, 0644)
	if got := findCardServer(dir); filepath.Base(got) != "Server.exe" {
		t.Errorf("new layout = %s", got)
	}
	os.WriteFile(filepath.Join(dir, "server", "Server-Infinite.exe"), nil, 0644)
	if got := findCardServer(dir); filepath.Base(got) != "Server-Infinite.exe" {
		t.Errorf("classic layout should win = %s", got)
	}
}

func TestCardImageDir(t *testing.T) {
	dir := t.TempDir()
	if got := cardImageDirIn(dir); got != filepath.Join(dir, "images") {
		t.Errorf("default = %s", got)
	}
	os.MkdirAll(filepath.Join(dir, "wwwroot", "images", "mobile_suits"), 0755)
	if got := cardImageDirIn(dir); got != filepath.Join(dir, "wwwroot", "images") {
		t.Errorf("new build = %s", got)
	}
	os.MkdirAll(filepath.Join(dir, "images", "mobile_suits"), 0755)
	if got := cardImageDirIn(dir); got != filepath.Join(dir, "images") {
		t.Errorf("old build should win = %s", got)
	}
}
