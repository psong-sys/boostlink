package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests against real game files are opt-in, because game files are never in the repo:
//
//	EXVS_EXES       Japanese vsac29_Release.exe files from any builds, separated by ;
//	EXVS_LEGACY_EN  an exe that the older, offset-based exvs-english patched
func TestRoundTrip(t *testing.T) {
	v := os.Getenv("EXVS_EXES")
	if v == "" {
		v = os.Getenv("EXVS_EXE")
	}
	if v == "" {
		t.Skip("EXVS_EXES not set")
	}
	ps := loadPatches()
	for _, src := range strings.Split(v, ";") {
		orig, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		st, sp, missing := classify(orig, ps, nil)
		if st != stJapanese {
			t.Fatalf("%s: original not detected as Japanese", src)
		}
		t.Logf("%s: %d spots, %d of %d strings missing", src, len(sp), missing, len(pairsOf(ps)))
		dir := t.TempDir()
		for _, n := range exeNames {
			os.WriteFile(filepath.Join(dir, n), orig, 0o644)
		}
		if err := run("install", dir, ps); err != nil {
			t.Fatal(err)
		}
		p, _ := os.ReadFile(filepath.Join(dir, exeNames[0]))
		if st, _, _ := classify(p, ps, loadRecord(dir, exeNames[0])); st != stEnglish || len(p) != len(orig) {
			t.Fatal("not English after install")
		}
		if string(p) == string(orig) {
			t.Fatal("install changed nothing")
		}
		if err := run("install", dir, ps); err != nil { // idempotent
			t.Fatal(err)
		}
		if err := run("restore", dir, ps); err != nil {
			t.Fatal(err)
		}
		r, _ := os.ReadFile(filepath.Join(dir, exeNames[0]))
		if string(r) != string(orig) {
			t.Fatal("restore not byte-identical")
		}
		// restore without the backup still works, from the record
		run("install", dir, ps)
		os.Remove(filepath.Join(backupDir(dir), exeNames[0]))
		if err := run("restore", dir, ps); err != nil {
			t.Fatal(err)
		}
		r, _ = os.ReadFile(filepath.Join(dir, exeNames[0]))
		if string(r) != string(orig) {
			t.Fatal("in-place restore not byte-identical")
		}
	}
}

func TestLegacyInstallRecognised(t *testing.T) {
	src := os.Getenv("EXVS_LEGACY_EN")
	if src == "" {
		t.Skip("EXVS_LEGACY_EN not set")
	}
	d, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if st, _, _ := classify(d, loadPatches(), nil); st != stEnglish {
		t.Fatalf("an exe patched by the older exvs-english reads as %s", st)
	}
}

func TestRefusesOtherExe(t *testing.T) {
	if st, _, _ := classify(make([]byte, 4096), loadPatches(), nil); st != stUnknown {
		t.Fatal("blank file not refused")
	}
}
