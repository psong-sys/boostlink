package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGamePatchRoundTrip(t *testing.T) {
	src := os.Getenv("EXVS_GAME_EXE")
	if src == "" {
		t.Skip("set EXVS_GAME_EXE to a vsac29_Release.exe to run")
	}
	orig, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "vsac29_Release.exe")
	os.WriteFile(p, orig, 0644)
	if msg, err := patchGameExe(p); err != nil || msg == "already patched" {
		t.Fatalf("patch: %q %v", msg, err)
	}
	if msg, _ := patchGameExe(p); msg != "already patched" {
		t.Fatalf("second patch should be a no-op, got %q", msg)
	}
	if _, err := unpatchGameExe(p); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(p)
	if !bytes.Equal(back, orig) {
		t.Fatal("unpatch did not restore the original bytes")
	}
}

func TestGamePatchCaveFits(t *testing.T) {
	if n := len(gpCave()); gpCaveVA+uint64(n) > gpImageBase+gpTextVA+gpTextVSizeOK {
		t.Fatalf("routine (%d bytes) runs past the new .text size", n)
	}
}
