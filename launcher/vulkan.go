package main

// Vulkan mode: puts vkd3d-proton (d3d12.dll, d3d12core.dll) and DXVK (dxgi.dll)
// next to the game, so it draws through Vulkan instead of DirectX 12. On some
// NVIDIA PCs the DirectX 12 path ends in a VIDEO_MEMORY_MANAGEMENT_INTERNAL
// (0x10E) blue screen; Vulkan avoids it.
//
// The DLLs come straight from the official GitHub releases, pinned by version
// and SHA-256, never from a game pack. Files that were already there are moved
// to exvs-launcher/vulkan-backup first, and turning the mode off puts them back.
// The downloads stay in exvs-launcher/vulkan-cache so turning it on again works
// offline.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

type vulkanSource struct {
	Name    string
	Version string
	URL     string
	SHA256  string
	Zstd    bool
	Files   []string // paths inside the archive, after the top folder
}

var vulkanSources = []vulkanSource{
	{
		Name: "vkd3d-proton", Version: "3.1", Zstd: true,
		URL:    "https://github.com/HansKristian-Work/vkd3d-proton/releases/download/v3.1/vkd3d-proton-3.1.tar.zst",
		SHA256: "b85ea8219b71093a3924913dd7aae6172333dc0c069983ed510816d67f02120b",
		Files:  []string{"x64/d3d12.dll", "x64/d3d12core.dll"},
	},
	{
		Name: "DXVK", Version: "3.1.1",
		URL:    "https://github.com/doitsujin/dxvk/releases/download/v3.1.1/dxvk-3.1.1.tar.gz",
		SHA256: "40565b4a724aadc4433fa4e010b4b23916d9b1f1baeee64e17186db94f54e608",
		Files:  []string{"x64/dxgi.dll"},
	},
}

// vulkanDLLs are the files Vulkan mode puts in the game folder.
var vulkanDLLs = []string{"d3d12.dll", "d3d12core.dll", "dxgi.dll"}

type vulkanState struct {
	On       bool              `json:"on"`
	Versions map[string]string `json:"versions"`
	Files    map[string]string `json:"files"`    // name -> sha256 we installed
	BackedUp []string          `json:"backedUp"` // names moved to vulkan-backup
}

type vulkanStatus struct {
	State    string            `json:"state"` // on | off | other | partial
	Detail   string            `json:"detail"`
	Versions map[string]string `json:"versions,omitempty"`
}

var vulkanMu sync.Mutex

func vulkanStatePath(p Paths) string { return filepath.Join(p.LauncherDir, "vulkan.json") }
func vulkanBackupDir(p Paths) string { return filepath.Join(p.LauncherDir, "vulkan-backup") }
func vulkanCacheDir(p Paths) string  { return filepath.Join(p.LauncherDir, "vulkan-cache") }

func loadVulkanState(p Paths) (vulkanState, bool) {
	var s vulkanState
	b, err := os.ReadFile(vulkanStatePath(p))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return vulkanState{}, false
	}
	return s, true
}

func getVulkanStatus() vulkanStatus {
	vulkanMu.Lock()
	defer vulkanMu.Unlock()
	return vulkanStatusOf(resolvePaths(loadLauncherConfig()))
}

func vulkanStatusOf(p Paths) vulkanStatus {
	var present []string
	for _, n := range vulkanDLLs {
		if fileExists(filepath.Join(p.GameDir, n)) {
			present = append(present, n)
		}
	}
	st, ok := loadVulkanState(p)
	if ok && st.On {
		var changed []string
		for _, n := range vulkanDLLs {
			if !strings.EqualFold(fileSHA(filepath.Join(p.GameDir, n)), st.Files[n]) {
				changed = append(changed, n)
			}
		}
		if len(changed) == 0 {
			return vulkanStatus{State: "on", Versions: st.Versions,
				Detail: fmt.Sprintf("On: vkd3d-proton %s and DXVK %s.", st.Versions["vkd3d-proton"], st.Versions["DXVK"])}
		}
		return vulkanStatus{State: "partial", Detail: "On, but " + strings.Join(changed, ", ") + " changed or went missing since. Turn it off and on again to repair it."}
	}
	switch {
	case len(present) == 0:
		return vulkanStatus{State: "off", Detail: "Off: the game draws with DirectX 12."}
	case len(present) == len(vulkanDLLs):
		return vulkanStatus{State: "other", Detail: "The game folder already has Vulkan files that the launcher didn't put there (" + strings.Join(present, ", ") + "). Turning Vulkan mode on swaps them for the official ones and keeps yours in a backup."}
	default:
		return vulkanStatus{State: "other", Detail: "The game folder has " + strings.Join(present, ", ") + " from somewhere else. Turning Vulkan mode on replaces them with the official set and keeps yours in a backup."}
	}
}

// fetchVulkanArchive returns the archive's bytes, from the cache when it's there and intact.
func fetchVulkanArchive(p Paths, src vulkanSource) ([]byte, error) {
	dir := vulkanCacheDir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, path.Base(src.URL))
	if !strings.EqualFold(fileSHA(dst), src.SHA256) {
		if err := download(src.URL, dst+".download", src.SHA256); err != nil {
			return nil, fmt.Errorf("couldn't download %s %s: %v", src.Name, src.Version, err)
		}
		os.Remove(dst)
		if err := os.Rename(dst+".download", dst); err != nil {
			return nil, err
		}
	}
	return os.ReadFile(dst)
}

// extractVulkanFiles pulls the wanted files out of a .tar.gz or .tar.zst,
// ignoring the archive's top folder. It returns base name -> bytes.
func extractVulkanFiles(archive []byte, isZstd bool, want []string) (map[string][]byte, error) {
	var r io.Reader
	if isZstd {
		d, err := zstd.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		defer d.Close()
		r = d
	} else {
		g, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		defer g.Close()
		r = g
	}
	wanted := map[string]bool{}
	for _, w := range want {
		wanted[w] = true
	}
	out := map[string][]byte{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean(h.Name), "./")
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if !wanted[name] {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 100<<20))
		if err != nil {
			return nil, err
		}
		out[path.Base(name)] = b
	}
	for _, w := range want {
		if _, ok := out[path.Base(w)]; !ok {
			return nil, fmt.Errorf("%s is missing from the download", w)
		}
	}
	return out, nil
}

func shaBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func enableVulkan() (vulkanStatus, error) {
	vulkanMu.Lock()
	defer vulkanMu.Unlock()
	if runner.Busy() {
		return vulkanStatus{}, errString("close the game before changing Vulkan mode")
	}
	return enableVulkanAt(resolvePaths(loadLauncherConfig()))
}

func enableVulkanAt(p Paths) (vulkanStatus, error) {
	if !fileExists(p.GameExe) {
		return vulkanStatus{}, fmt.Errorf("the game isn't at %s", p.GameDir)
	}
	if st := vulkanStatusOf(p); st.State == "on" {
		return st, nil
	}
	files := map[string][]byte{}
	versions := map[string]string{}
	for _, src := range vulkanSources {
		a, err := fetchVulkanArchive(p, src)
		if err != nil {
			return vulkanStatus{}, err
		}
		got, err := extractVulkanFiles(a, src.Zstd, src.Files)
		if err != nil {
			return vulkanStatus{}, fmt.Errorf("%s %s: %v", src.Name, src.Version, err)
		}
		for k, v := range got {
			files[k] = v
		}
		versions[src.Name] = src.Version
	}

	old, hadState := loadVulkanState(p)
	st := vulkanState{On: true, Versions: versions, Files: map[string]string{}}
	if hadState {
		st.BackedUp = old.BackedUp // a repair keeps the original backup
	}
	bdir := vulkanBackupDir(p)
	for _, n := range vulkanDLLs {
		target := filepath.Join(p.GameDir, n)
		want := shaBytes(files[n])
		if fileExists(target) && !strings.EqualFold(fileSHA(target), want) {
			ours := hadState && strings.EqualFold(fileSHA(target), old.Files[n])
			if !ours && !fileExists(filepath.Join(bdir, n)) {
				if err := os.MkdirAll(bdir, 0755); err != nil {
					return vulkanStatus{}, err
				}
				if err := os.Rename(target, filepath.Join(bdir, n)); err != nil {
					return vulkanStatus{}, fmt.Errorf("couldn't back up %s: %v", n, err)
				}
				st.BackedUp = append(st.BackedUp, n)
			}
		}
		tmp := target + ".vulkan-new"
		if err := os.WriteFile(tmp, files[n], 0755); err != nil {
			return vulkanStatus{}, err
		}
		os.Remove(target)
		if err := os.Rename(tmp, target); err != nil {
			os.Remove(tmp)
			return vulkanStatus{}, fmt.Errorf("couldn't write %s: %v", n, err)
		}
		st.Files[n] = want
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(vulkanStatePath(p), b, 0644); err != nil {
		return vulkanStatus{}, err
	}
	return vulkanStatusOf(p), nil
}

func disableVulkan() (vulkanStatus, error) {
	vulkanMu.Lock()
	defer vulkanMu.Unlock()
	if runner.Busy() {
		return vulkanStatus{}, errString("close the game before changing Vulkan mode")
	}
	return disableVulkanAt(resolvePaths(loadLauncherConfig()))
}

func disableVulkanAt(p Paths) (vulkanStatus, error) {
	st, ok := loadVulkanState(p)
	if !ok || !st.On {
		if s := vulkanStatusOf(p); s.State == "other" {
			return s, errString("these Vulkan files weren't put there by the launcher, so it leaves them alone. Delete d3d12.dll, d3d12core.dll and dxgi.dll from the game folder yourself to go back to DirectX 12")
		}
		return vulkanStatusOf(p), nil
	}
	var kept []string
	for _, n := range vulkanDLLs {
		target := filepath.Join(p.GameDir, n)
		if fileExists(target) {
			if !strings.EqualFold(fileSHA(target), st.Files[n]) {
				kept = append(kept, n) // someone replaced it; don't delete their file
				continue
			}
			if err := os.Remove(target); err != nil {
				return vulkanStatus{}, fmt.Errorf("couldn't remove %s: %v", n, err)
			}
		}
	}
	bdir := vulkanBackupDir(p)
	for _, n := range st.BackedUp {
		target := filepath.Join(p.GameDir, n)
		if fileExists(target) {
			continue
		}
		if err := os.Rename(filepath.Join(bdir, n), target); err != nil && !os.IsNotExist(err) {
			return vulkanStatus{}, fmt.Errorf("couldn't put back %s: %v", n, err)
		}
	}
	os.Remove(bdir) // only if empty
	if err := os.Remove(vulkanStatePath(p)); err != nil && !os.IsNotExist(err) {
		return vulkanStatus{}, err
	}
	s := vulkanStatusOf(p)
	if len(kept) > 0 {
		return s, fmt.Errorf("Vulkan mode is off, but %s changed after it was turned on, so the launcher left it in place", strings.Join(kept, ", "))
	}
	return s, nil
}
