package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func tarOf(t *testing.T, top string, files map[string]string) []byte {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: top + "/" + n, Mode: 0644, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	return b.Bytes()
}

func TestVulkanOnOff(t *testing.T) {
	vk := tarOf(t, "vkd3d-proton-9", map[string]string{"x64/d3d12.dll": "VK12", "x64/d3d12core.dll": "VKCORE", "x86/d3d12.dll": "wrong"})
	var zs bytes.Buffer
	zw, _ := zstd.NewWriter(&zs)
	zw.Write(vk)
	zw.Close()
	dx := tarOf(t, "dxvk-9", map[string]string{"x64/dxgi.dll": "DXGI", "x32/dxgi.dll": "wrong"})
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write(dx)
	gw.Close()

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if filepath.Ext(r.URL.Path) == ".zst" {
			w.Write(zs.Bytes())
		} else {
			w.Write(gz.Bytes())
		}
	}))
	defer srv.Close()
	saved := vulkanSources
	defer func() { vulkanSources = saved }()
	vulkanSources = []vulkanSource{
		{Name: "vkd3d-proton", Version: "9", Zstd: true, URL: srv.URL + "/vk.tar.zst", SHA256: shaBytes(zs.Bytes()), Files: []string{"x64/d3d12.dll", "x64/d3d12core.dll"}},
		{Name: "DXVK", Version: "9", URL: srv.URL + "/dx.tar.gz", SHA256: shaBytes(gz.Bytes()), Files: []string{"x64/dxgi.dll"}},
	}

	game := t.TempDir()
	p := Paths{GameDir: game, LauncherDir: filepath.Join(game, "exvs-launcher"), GameExe: filepath.Join(game, gameExeName)}
	os.MkdirAll(p.LauncherDir, 0755)
	os.WriteFile(p.GameExe, []byte("exe"), 0644)
	os.WriteFile(filepath.Join(game, "dxgi.dll"), []byte("OLD"), 0644) // something already there

	if s := vulkanStatusOf(p); s.State != "other" {
		t.Fatalf("before: %+v", s)
	}
	st, err := enableVulkanAt(p)
	if err != nil || st.State != "on" {
		t.Fatalf("enable: %v %+v", err, st)
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(game, n)); return string(b) }
	if read("d3d12.dll") != "VK12" || read("d3d12core.dll") != "VKCORE" || read("dxgi.dll") != "DXGI" {
		t.Fatal("wrong files installed")
	}
	if b, _ := os.ReadFile(filepath.Join(p.LauncherDir, "vulkan-backup", "dxgi.dll")); string(b) != "OLD" {
		t.Fatal("old dxgi.dll not backed up")
	}

	// Broken install: repair by turning on again, from the cache.
	os.Remove(filepath.Join(game, "d3d12core.dll"))
	if s := vulkanStatusOf(p); s.State != "partial" {
		t.Fatalf("partial: %+v", s)
	}
	if st, err := enableVulkanAt(p); err != nil || st.State != "on" {
		t.Fatalf("repair: %v %+v", err, st)
	}
	if hits != 2 {
		t.Fatalf("repair downloaded again (%d requests)", hits)
	}
	if b, _ := os.ReadFile(filepath.Join(p.LauncherDir, "vulkan-backup", "dxgi.dll")); string(b) != "OLD" {
		t.Fatal("repair lost the backup")
	}

	st, err = disableVulkanAt(p)
	if err != nil || st.State != "other" { // the original dxgi.dll is back
		t.Fatalf("disable: %v %+v", err, st)
	}
	if read("dxgi.dll") != "OLD" || fileExists(filepath.Join(game, "d3d12.dll")) || fileExists(filepath.Join(game, "d3d12core.dll")) {
		t.Fatal("disable didn't restore the folder")
	}
	if fileExists(filepath.Join(p.LauncherDir, "vulkan.json")) {
		t.Fatal("state file left behind")
	}
}

func TestVulkanBadChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tampered") }))
	defer srv.Close()
	saved := vulkanSources
	defer func() { vulkanSources = saved }()
	vulkanSources = []vulkanSource{{Name: "DXVK", Version: "9", URL: srv.URL + "/dx.tar.gz", SHA256: shaBytes([]byte("real")), Files: []string{"x64/dxgi.dll"}}}
	game := t.TempDir()
	p := Paths{GameDir: game, LauncherDir: filepath.Join(game, "exvs-launcher"), GameExe: filepath.Join(game, gameExeName)}
	os.WriteFile(p.GameExe, []byte("exe"), 0644)
	if _, err := enableVulkanAt(p); err == nil {
		t.Fatal("accepted a download with the wrong checksum")
	}
	for _, n := range vulkanDLLs {
		if fileExists(filepath.Join(game, n)) {
			t.Fatal("wrote files after a failed download")
		}
	}
}
