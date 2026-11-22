package main

// Self-update. The launcher reads a small manifest (latest.json) published
// with each release, and replaces its own files and exvs-online's with the
// ones listed there, after checking each file's SHA-256.
//
// latest.json:
//
//	{"version": "1.5.0", "notes": "...",
//	 "files": [{"path": "exvs-launcher/exvs-launcher.exe", "url": "https://...", "sha256": "...", "size": 123}]}
//
// Paths are relative to the game folder and may only point into
// exvs-launcher/ or exvs-online/, so an update can never touch game files.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// updateURL can be changed at build time with -ldflags "-X main.updateURL=...".
var updateURL = "https://github.com/psong-sys/boostlink/releases/latest/download/latest.json"

type updateFile struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type updateManifest struct {
	Version string       `json:"version"`
	Notes   string       `json:"notes"`
	Files   []updateFile `json:"files"`
}

type updateInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Version   string `json:"version,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Error     string `json:"error,omitempty"`
}

var updateHTTP = &http.Client{Timeout: 10 * time.Minute}

// newerVersion reports whether a is a higher dotted version than b.
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// updateTarget turns a manifest path into a full path, or "" if it isn't allowed.
func updateTarget(gameDir, p string) string {
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return ""
	}
	if !strings.HasPrefix(p, "exvs-launcher/") && !strings.HasPrefix(p, "exvs-online/") {
		return ""
	}
	return filepath.Join(gameDir, filepath.FromSlash(p))
}

func fetchManifest() (*updateManifest, error) {
	if updateURL == "" {
		return nil, errString("updates aren't set up in this build")
	}
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(updateURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("update server answered %d", resp.StatusCode)
	}
	var m updateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("bad update manifest: %v", err)
	}
	return &m, nil
}

var (
	updMu     sync.Mutex
	updCached *updateInfo
	updAt     time.Time
)

func checkUpdate(force bool) *updateInfo {
	updMu.Lock()
	defer updMu.Unlock()
	if !force && updCached != nil && time.Since(updAt) < 30*time.Minute {
		return updCached
	}
	info := &updateInfo{Current: version}
	m, err := fetchManifest()
	if err != nil {
		info.Error = err.Error()
	} else if newerVersion(m.Version, version) {
		info.Available, info.Version, info.Notes = true, m.Version, m.Notes
	}
	updCached, updAt = info, time.Now()
	return info
}

func fileSHA(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func download(url, dst, want string) error {
	resp, err := updateHTTP.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download of %s answered %d", filepath.Base(dst), resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		os.Remove(dst)
		return fmt.Errorf("%s didn't download correctly (checksum mismatch)", filepath.Base(dst))
	}
	return nil
}

// applyUpdate downloads every changed file next to its target, then swaps
// them in. The running launcher exe is renamed to .old first, since Windows
// lets a running exe be renamed but not overwritten. It returns true when
// the launcher itself changed and needs a restart.
func applyUpdate() (bool, error) {
	if runner.Busy() {
		return false, errString("close the game before updating")
	}
	m, err := fetchManifest()
	if err != nil {
		return false, err
	}
	if !newerVersion(m.Version, version) {
		return false, errString("you already have the newest version")
	}
	gameDir := resolvePaths(loadLauncherConfig()).GameDir
	self := selfPath

	type job struct{ target, tmp string }
	var jobs []job
	cleanup := func() {
		for _, j := range jobs {
			os.Remove(j.tmp)
		}
	}
	for _, f := range m.Files {
		t := updateTarget(gameDir, f.Path)
		if t == "" || f.URL == "" || len(f.SHA256) != 64 {
			cleanup()
			return false, fmt.Errorf("the update lists a file it isn't allowed to change: %s", f.Path)
		}
		if strings.EqualFold(fileSHA(t), f.SHA256) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(t), 0755); err != nil {
			cleanup()
			return false, err
		}
		tmp := t + ".download"
		if err := download(f.URL, tmp, f.SHA256); err != nil {
			cleanup()
			return false, err
		}
		jobs = append(jobs, job{t, tmp})
	}
	// exvs-online can't be replaced while it runs.
	killByName("exvs-online.exe")
	restart := false
	for _, j := range jobs {
		if strings.EqualFold(filepath.Clean(j.target), filepath.Clean(self)) {
			old := self + ".old"
			os.Remove(old)
			if err := os.Rename(self, old); err != nil {
				cleanup()
				return false, fmt.Errorf("couldn't replace the launcher: %v", err)
			}
			restart = true
		}
		var err error
		for i := 0; i < 10; i++ {
			os.Remove(j.target)
			if err = os.Rename(j.tmp, j.target); err == nil {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if err != nil {
			cleanup()
			return restart, fmt.Errorf("couldn't replace %s: %v", filepath.Base(j.target), err)
		}
	}
	updMu.Lock()
	updCached = nil
	updMu.Unlock()
	return restart, nil
}

// selfPath is where the launcher was started from, read before an update
// renames the running exe (after that, os.Executable reports the new name).
var selfPath = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}()

// cleanOldExe removes the previous launcher left behind by an update.
func cleanOldExe() {
	if self := selfPath; self != "" {
		for i := 0; i < 20; i++ {
			if err := os.Remove(self + ".old"); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// restartSelf starts the new launcher (without opening another window; the
// open page reloads itself) and exits this one.
func restartSelf() {
	cmd := exec.Command(selfPath, "--updated", "--no-browser")
	cmd.Dir = filepath.Dir(selfPath)
	if err := cmd.Start(); err != nil {
		return
	}
	os.Exit(0)
}

func (u *uiServer) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, checkUpdate(r.URL.Query().Get("force") == "1"))
	})
	mux.HandleFunc("POST /api/update", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		restart, err := applyUpdate()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true, "restart": restart})
		if restart {
			go func() { time.Sleep(500 * time.Millisecond); restartSelf() }()
		}
	})
}
