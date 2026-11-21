package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	src := os.Getenv("EXVS_EXE")
	if src == "" {
		t.Skip("EXVS_EXE not set")
	}
	orig, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	ps := loadPatches()
	if classify(orig, ps) != stJapanese {
		t.Fatal("original not detected as Japanese")
	}
	dir := t.TempDir()
	for _, n := range exeNames {
		os.WriteFile(filepath.Join(dir, n), orig, 0o644)
	}
	if err := run("install", dir, ps); err != nil {
		t.Fatal(err)
	}
	p, _ := os.ReadFile(filepath.Join(dir, exeNames[0]))
	if classify(p, ps) != stEnglish || len(p) != len(orig) {
		t.Fatal("not English after install")
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
	// restore without backup still works
	os.RemoveAll(backupDir(dir))
	run("install", dir, ps)
	os.RemoveAll(backupDir(dir))
	if err := run("restore", dir, ps); err != nil {
		t.Fatal(err)
	}
	r, _ = os.ReadFile(filepath.Join(dir, exeNames[0]))
	if string(r) != string(orig) {
		t.Fatal("in-place restore not byte-identical")
	}
}
