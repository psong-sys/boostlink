// exvs-launcher: one-click launcher for the EXVS2 PC setup.
//
//	exvs-launcher.exe          opens the settings window
//	exvs-launcher.exe --play   skips the window: starts everything, runs the game,
//	                           and shuts everything down when the game closes
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

const version = "1.9.3"

func main() {
	play := flag.Bool("play", false, "start the game right away without the settings window")
	noBrowser := flag.Bool("no-browser", false, "don't open the settings window (for testing)")
	updated := flag.Bool("updated", false, "started by an update: wait for the old launcher to close")
	flag.Parse()
	go cleanOldExe()

	if *play {
		playHeadless()
		return
	}

	ln, err := net.Listen("tcp", uiAddr)
	for i := 0; err != nil && *updated && i < 40; i++ {
		time.Sleep(250 * time.Millisecond)
		ln, err = net.Listen("tcp", uiAddr)
	}
	if err != nil {
		// Already running: bring its window to the front instead of opening
		// a second one. Only open a window if it has none (it was closed
		// while the game kept the launcher running).
		if !focusUIWindow() {
			<-openUI("http://" + uiAddr + "/")
		}
		return
	}
	srv := newUIServer()
	go srv.watchIdle()
	if !*noBrowser {
		openUI("http://" + uiAddr + "/")
	}
	_ = http.Serve(ln, localOnly(srv.routes()))
}

func playHeadless() {
	done := make(chan error, 1)
	if err := runner.Start(func(err error) { done <- err }); err != nil {
		messageBox("Boost Link", err.Error(), true)
		return
	}
	if err := <-done; err != nil {
		messageBox("Boost Link", err.Error()+"\n\nOpen the launcher without --play to change settings, or check the logs folder.", true)
	}
}

type uiServer struct {
	mu       sync.Mutex
	lastSeen time.Time
	bye      bool
}

func newUIServer() *uiServer { return &uiServer{lastSeen: time.Now()} }

func (u *uiServer) touch() {
	u.mu.Lock()
	u.lastSeen = time.Now()
	u.bye = false
	u.mu.Unlock()
}

// watchIdle exits the launcher once the window is closed and nothing is
// running. While the game runs it stays alive even with the window closed.
func (u *uiServer) watchIdle() {
	for range time.Tick(time.Second) {
		u.mu.Lock()
		idle := time.Since(u.lastSeen)
		bye := u.bye
		u.mu.Unlock()
		if runner.Busy() {
			continue
		}
		if (bye && idle > 4*time.Second) || idle > 3*time.Minute {
			os.Exit(0)
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]string{"error": err.Error()})
}

// sameOrigin blocks other websites from poking the local API.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == "http://"+uiAddr
}

// localOnly refuses requests whose Host isn't the launcher's own address. A
// website that points its own name at 127.0.0.1 (DNS rebinding) would
// otherwise count as same-origin and could read the settings, card included.
func localOnly(h http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(uiAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != uiAddr && r.Host != "localhost:"+port {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (u *uiServer) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	u.cardRoutes(mux)
	u.updateRoutes(mux)

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		u.touch()
		s, d, err := readSettings()
		resp := map[string]any{"settings": s, "detected": d}
		if err != nil {
			resp["error"] = err.Error()
		}
		writeJSON(w, resp)
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		var s Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeErr(w, err)
			return
		}
		if runner.Busy() {
			writeErr(w, errString("can't save while the game is running"))
			return
		}
		if err := writeSettings(s); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/play", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		rememberHost()
		if err := runner.Start(nil); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		runner.Stop()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		u.touch()
		writeJSON(w, runner.Status())
	})
	mux.HandleFunc("POST /api/bye", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.bye = true
		u.mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/card-editor", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		if err := runner.StartCardServer(); err != nil {
			writeErr(w, err)
			return
		}
		openURL("http://127.0.0.1:28080/")
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/match-status", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		if !portOpen(proxyPort) {
			writeErr(w, errString("exvs-online isn't running. Press PLAY in Host mode first"))
			return
		}
		openURL("http://127.0.0.1:28090/status")
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/check", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		res, err := runCheck()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("GET /api/vulkan", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, getVulkanStatus())
	})
	mux.HandleFunc("POST /api/vulkan", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, err)
			return
		}
		var st vulkanStatus
		var err error
		if req.On {
			st, err = enableVulkan()
		} else {
			st, err = disableVulkan()
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, st)
	})
	mux.HandleFunc("POST /api/fix-firewall", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		out, err := fixFirewall()
		if err != nil {
			writeErr(w, errString("firewall fix didn't finish: "+err.Error()+" "+out))
			return
		}
		writeJSON(w, map[string]string{"output": out})
	})
	mux.HandleFunc("POST /api/shortcuts", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		where, err := createShortcuts()
		if err != nil {
			writeErr(w, errString("couldn't create shortcuts: "+err.Error()+" "+where))
			return
		}
		writeJSON(w, map[string]string{"desktop": where})
	})
	return mux
}

type errString string

func (e errString) Error() string { return string(e) }

// rememberHost keeps the last few host IPs joined in friend mode.
func rememberHost() {
	lc := loadLauncherConfig()
	if lc.Mode != "friend" || lc.HostIP == "" {
		return
	}
	list := []string{lc.HostIP}
	for _, h := range lc.RecentHosts {
		if h != lc.HostIP && len(list) < 5 {
			list = append(list, h)
		}
	}
	lc.RecentHosts = list
	_ = saveLauncherConfig(lc)
}
