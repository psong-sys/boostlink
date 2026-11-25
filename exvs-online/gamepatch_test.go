package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests against real game files are opt-in, because game files are never in the repo:
//
//	EXVS_GAME_EXES   unpatched vsac29_Release.exe files from any builds, separated by ;
//	EXVS_RESULT_REF  a vsac29_Release.exe patched by an earlier version of this tool; the
//	                 matching unpatched exe in EXVS_GAME_EXES must patch to exactly this
//	EXVS_BNGRW_DLLS  unpatched bngrw.dll files, separated by ;
//	EXVS_FOCUS_REF   a bngrw.dll patched by hand earlier; same check as EXVS_RESULT_REF
func envFiles(t *testing.T, name string) []string {
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("set %s to run", name)
	}
	return strings.Split(v, ";")
}

func TestGamePatchRealBuilds(t *testing.T) {
	var ref []byte
	if r := os.Getenv("EXVS_RESULT_REF"); r != "" {
		ref, _ = os.ReadFile(r)
	}
	refMatched := ref == nil
	for _, src := range envFiles(t, "EXVS_GAME_EXES") {
		orig, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		g, err := planGamePatch(orig)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		t.Logf("%s: %s, wait check at %#x, routine at %#x", filepath.Base(filepath.Dir(src)), buildName(g.pe), g.siteVA(), g.caveVA)
		p := filepath.Join(t.TempDir(), "vsac29_Release.exe")
		os.WriteFile(p, orig, 0644)
		if msg, err := patchGameExe(p); err != nil || msg == "already patched" {
			t.Fatalf("patch: %q %v", msg, err)
		}
		patched, _ := os.ReadFile(p)
		if bytes.Equal(patched, ref) {
			refMatched = true
		}
		if msg, _ := patchGameExe(p); msg != "already patched" {
			t.Fatalf("second patch should be a no-op, got %q", msg)
		}
		// undo in place (no backup) must give back the same code
		os.Remove(p + ".before-result-fix")
		if _, err := unpatchGameExe(p); err != nil {
			t.Fatal(err)
		}
		back, _ := os.ReadFile(p)
		if st, err := gamePatchState(back); err != nil || st != "original" {
			t.Fatalf("after in-place undo: %q %v", st, err)
		}
		if d := diffCount(back, orig); d > 4 {
			t.Fatalf("in-place undo left %d bytes different (only .text size may differ)", d)
		}
		// undo with the backup is byte-exact
		os.WriteFile(p, orig, 0644)
		patchGameExe(p)
		if msg, err := unpatchGameExe(p); err != nil || msg != "restored from backup" {
			t.Fatalf("unpatch: %q %v", msg, err)
		}
		back, _ = os.ReadFile(p)
		if !bytes.Equal(back, orig) {
			t.Fatal("unpatch did not restore the original bytes")
		}
	}
	if !refMatched {
		t.Error("no exe patched to exactly EXVS_RESULT_REF")
	}
}

func TestFocusPatchRealDLLs(t *testing.T) {
	var ref []byte
	if r := os.Getenv("EXVS_FOCUS_REF"); r != "" {
		ref, _ = os.ReadFile(r)
	}
	refMatched := ref == nil
	for _, src := range envFiles(t, "EXVS_BNGRW_DLLS") {
		orig, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "bngrw.dll")
		os.WriteFile(p, orig, 0644)
		if msg, err := patchFocusDLL(p); err != nil || msg == "already patched" {
			t.Fatalf("%s: patch: %q %v", src, msg, err)
		}
		patched, _ := os.ReadFile(p)
		if bytes.Equal(patched, ref) {
			refMatched = true
		}
		if ep1, _ := dllEntryPoint(orig); true {
			if ep2, _ := dllEntryPoint(patched); ep1 != ep2 {
				t.Error("patch changed the entry point check")
			}
		}
		if _, err := unpatchFocusDLL(p); err != nil {
			t.Fatal(err)
		}
		if back, _ := os.ReadFile(p); !bytes.Equal(back, orig) {
			t.Fatal("unpatch did not restore the original bytes")
		}
	}
	if !refMatched {
		t.Error("no DLL patched to exactly EXVS_FOCUS_REF")
	}
}

func diffCount(a, b []byte) int {
	if len(a) != len(b) {
		return len(a) + len(b)
	}
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

func TestFindPattern(t *testing.T) {
	d := []byte{1, 2, 3, 9, 1, 2, 4, 9, 1, 5}
	if got := findPattern(d, 0, len(d), []int{1, 2, wildcard, 9}); len(got) != 2 || got[0] != 0 || got[1] != 4 {
		t.Errorf("got %v", got)
	}
	if got := findPattern(d, 1, len(d), []int{1, 2, wildcard, 9}); len(got) != 1 {
		t.Errorf("lo bound: %v", got)
	}
}

func TestFocusRefusesOtherDLL(t *testing.T) {
	if _, err := focusPatchState(make([]byte, dllSize)); err == nil {
		t.Error("blank DLL accepted")
	}
}

func TestSaveBackupKeepsOlderBuild(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "x.before-result-fix")
	os.WriteFile(b, []byte("old build"), 0644)
	if err := saveBackup(b, []byte("new build")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(b); string(got) != "new build" {
		t.Errorf("backup = %q", got)
	}
	m, _ := filepath.Glob(b + ".*")
	if len(m) != 1 {
		t.Fatalf("older backup not kept aside: %v", m)
	}
}

func TestGameProtoRevision(t *testing.T) {
	dir := os.Getenv("EXVS_GAME_DIR") // set it to a game folder to run this
	exe, err := os.ReadFile(filepath.Join(dir, "vsac29_Release.exe"))
	if dir == "" || err != nil {
		t.Skip("set EXVS_GAME_DIR to the game folder")
	}
	if got := gameProtoRevision(exe); got != ourProtoRevision {
		t.Fatalf("game revision %d, ours %d", got, ourProtoRevision)
	}
}
