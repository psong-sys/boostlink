package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Step struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"` // waiting | running | done | skipped | error
	Note  string `json:"note,omitempty"`
}

type Status struct {
	Steps   []Step   `json:"steps"`
	Busy    bool     `json:"busy"`
	Stage   string   `json:"stage"`
	Error   string   `json:"error"`
	Log     []string `json:"log"`
	Mode    string   `json:"mode"`
	Started string   `json:"started"`
}

type Runner struct {
	mu     sync.Mutex
	st     Status
	helper []*exec.Cmd // processes we started and must stop
	solo   *exec.Cmd   // card server started on its own for the card editor
	stop   chan struct{}
}

var runner = &Runner{st: Status{Stage: "Ready"}}

func (r *Runner) logf(format string, a ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, a...)
	r.mu.Lock()
	r.st.Log = append(r.st.Log, line)
	if len(r.st.Log) > 200 {
		r.st.Log = r.st.Log[len(r.st.Log)-200:]
	}
	r.mu.Unlock()
	appendLauncherLog(line)
}

func (r *Runner) stage(s string) {
	r.mu.Lock()
	r.st.Stage = s
	r.mu.Unlock()
	r.logf("%s", s)
}

func (r *Runner) setStep(id, state, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.st.Steps {
		if r.st.Steps[i].ID == id {
			r.st.Steps[i].State = state
			r.st.Steps[i].Note = note
		}
	}
}

func planSteps(lc LauncherConfig) []Step {
	steps := []Step{{ID: "card", Label: "Card server", State: "waiting"}}
	if lc.Mode != "offline" {
		label := "Online (host)"
		if lc.Mode == "friend" {
			label = "Online (Boost Link)"
		}
		steps = append(steps, Step{ID: "online", Label: label, State: "waiting"})
	}
	if lc.LiveMonitor != "off" {
		steps = append(steps, Step{ID: "lm", Label: "Live Monitor", State: "waiting"})
	}
	return append(steps, Step{ID: "game", Label: "Game", State: "waiting"})
}

func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.st
	s.Log = append([]string(nil), r.st.Log...)
	s.Steps = append([]Step(nil), r.st.Steps...)
	return s
}

func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Busy
}

// Start runs the whole sequence in the background. It returns an error only
// when a run is already in progress.
func (r *Runner) Start(done func(error)) error {
	r.mu.Lock()
	if r.st.Busy {
		r.mu.Unlock()
		return errors.New("already running")
	}
	lc := loadLauncherConfig()
	r.st = Status{Busy: true, Stage: "Starting", Started: time.Now().Format(time.RFC3339), Steps: planSteps(lc)}
	r.stop = make(chan struct{})
	r.mu.Unlock()
	go func() {
		err := r.run()
		userStopped := err != nil && err.Error() == "stopped"
		if userStopped {
			err = nil
		}
		r.cleanup()
		r.mu.Lock()
		r.st.Busy = false
		for i := range r.st.Steps {
			if r.st.Steps[i].State == "running" {
				if userStopped {
					r.st.Steps[i].State = "skipped"
				} else if err != nil {
					r.st.Steps[i].State = "error"
				} else {
					r.st.Steps[i].State = "done"
				}
			}
		}
		if err != nil {
			r.st.Error = err.Error()
			r.st.Stage = "Stopped with an error"
		} else if userStopped {
			r.st.Stage = "Stopped. Everything is shut down."
		} else {
			r.st.Stage = "Game closed. Everything is shut down."
		}
		r.mu.Unlock()
		if err != nil {
			r.logf("ERROR: %v", err)
		} else {
			r.logf("Done")
		}
		if done != nil {
			done(err)
		}
	}()
	return nil
}

// Stop closes the game (which then shuts everything else down).
func (r *Runner) Stop() {
	r.mu.Lock()
	busy := r.st.Busy
	stop := r.stop
	r.mu.Unlock()
	if !busy {
		return
	}
	r.logf("Stop requested")
	select {
	case <-stop:
	default:
		close(stop)
	}
	killByName(gameExeName)
}

func (r *Runner) stopped() bool {
	select {
	case <-r.stop:
		return true
	default:
		return false
	}
}

func portOpen(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// waitPort waits until something listens on port, or the process exits, or
// the timeout passes.
func (r *Runner) waitPort(port int, exited <-chan struct{}, timeout time.Duration, what string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portOpen(port) {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("%s closed right after starting (see logs\\%s.log)", what, logName(what))
		case <-r.stop:
			return errors.New("stopped")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s didn't open port %d within %s", what, port, timeout)
}

func logName(what string) string {
	if strings.Contains(what, "online") {
		return "exvs-online"
	}
	return "card-server"
}

func (r *Runner) startHelper(name, exe string, args []string, dir string, p Paths) (<-chan struct{}, error) {
	return r.startHelperWith(name, exe, args, dir, p, hideWindow)
}

func (r *Runner) startHelperWith(name, exe string, args []string, dir string, p Paths, setup func(*exec.Cmd)) (<-chan struct{}, error) {
	_ = os.MkdirAll(p.LogDir, 0755)
	lf, err := os.Create(filepath.Join(p.LogDir, name+".log"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdout = lf
	cmd.Stderr = lf
	if setup != nil {
		setup(cmd)
	}
	if err := cmd.Start(); err != nil {
		lf.Close()
		return nil, err
	}
	addToJob(cmd)
	r.mu.Lock()
	r.helper = append(r.helper, cmd)
	r.mu.Unlock()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		lf.Close()
		close(exited)
	}()
	return exited, nil
}

func (r *Runner) run() error {
	s, _, err := readSettings()
	if err != nil {
		return err
	}
	lc := s.Launcher
	p := resolvePaths(lc)
	r.mu.Lock()
	r.st.Mode = lc.Mode
	r.mu.Unlock()
	r.logf("Mode: %s, game folder: %s", lc.Mode, p.GameDir)

	if !fileExists(p.GameExe) {
		return fmt.Errorf("%s not found in %s", gameExeName, p.GameDir)
	}
	if err := checkAdapter(s.Game.InterfaceName); err != nil {
		return err
	}
	if processRunning(gameExeName) {
		return errors.New("the game is already running. Close it first")
	}
	// Make sure config.ini points at the right server for this mode
	// (it may have been edited by hand since the last save).
	want := offlineServer
	if lc.Mode != "offline" {
		want = onlineServer
	}
	lmServer := want
	if lm, err := LoadIni(p.LMIni); err == nil {
		lmServer, _ = lm.Get("config", "Server")
	}
	if s.Game.Server != want || lmServer != want {
		r.logf("Setting Server=%s in config.ini and lmnfig.ini for %s mode", want, lc.Mode)
		if err := writeSettings(s); err != nil {
			return err
		}
	}

	// 1. card server
	r.setStep("card", "running", "")
	if portOpen(cardPort) {
		r.setStep("card", "done", "already running")
		r.logf("Card server is already running on port %d (%s), using it", cardPort, portOwner(cardPort))
	} else {
		if !fileExists(p.ServerExe) {
			return fmt.Errorf("card server not found at %s", p.ServerExe)
		}
		r.stage("Starting the card server (Server-Infinite)")
		if msg := lockCardServer(p); msg != "" {
			r.logf("%s", msg)
		}
		exited, err := r.startHelper("card-server", p.ServerExe, nil, filepath.Dir(p.ServerExe), p)
		if err != nil {
			return fmt.Errorf("starting card server: %w", err)
		}
		if err := r.waitPort(cardPort, exited, 90*time.Second, "card server"); err != nil {
			return err
		}
		r.logf("Card server is up")
		r.setStep("card", "done", "")
	}

	// 2. online proxy
	if lc.Mode != "offline" {
		r.setStep("online", "running", "")
		if portOpen(proxyPort) {
			owner := portOwner(proxyPort)
			if owner != "" && !strings.Contains(strings.ToLower(owner), "exvs-online") {
				return fmt.Errorf("port %d is taken by %s, not exvs-online. Close it and try again", proxyPort, owner)
			}
			if owner == "" {
				owner = "an unknown program"
			}
			r.logf("exvs-online was NOT started by the launcher: port %d is already used by %s, so the launcher uses that one. "+
				"It runs with its own config, and its traffic.log is next to its own config file, not in exvs-launcher. "+
				"Close it first if you want the launcher's %s setup", proxyPort, owner, lc.Mode)
			r.setStep("online", "done", "already running")
		} else {
			if !fileExists(p.OnlineExe) {
				return fmt.Errorf("exvs-online.exe not found at %s", p.OnlineExe)
			}
			if err := writeOnlineConfig(lc, p); err != nil {
				return err
			}
			if lc.Mode == "host" {
				r.stage("Starting exvs-online as host")
			} else {
				r.stage("Starting exvs-online, connecting to host " + lc.HostIP)
			}
			exited, err := r.startHelper("exvs-online", p.OnlineExe, []string{"-config", p.OnlineCfg}, p.LauncherDir, p)
			if err != nil {
				return fmt.Errorf("starting exvs-online: %w", err)
			}
			if err := r.waitPort(proxyPort, exited, 30*time.Second, "exvs-online"); err != nil {
				return err
			}
			r.logf("exvs-online is up")
			r.setStep("online", "done", "")
		}
	}

	if r.stopped() {
		return errors.New("stopped")
	}

	// 3. online battle mode (normally switched on by hand in the LM's F3 menu)
	if lc.Mode != "offline" {
		r.ensureOnline(p)
	}

	// 3. the Live Monitor, which the client needs running
	if err := r.startLiveMonitor(lc, p); err != nil {
		return err
	}
	if r.stopped() {
		return errors.New("stopped")
	}

	// 4. the game
	r.setStep("game", "running", "")
	r.stage("Game running")
	game := exec.Command(p.GameExe)
	game.Dir = p.GameDir
	if err := game.Start(); err != nil {
		return fmt.Errorf("starting the game: %w", err)
	}
	_ = game.Wait()
	// The game may hand off to another copy of itself; wait for all of them.
	for i := 0; ; i++ {
		time.Sleep(2 * time.Second)
		if !processRunning(gameExeName) {
			break
		}
		if i == 0 {
			r.logf("Game process is still around, waiting for it to close")
		}
	}
	r.logf("Game closed")
	r.setStep("game", "done", "")
	return nil
}

func (r *Runner) cleanup() {
	r.mu.Lock()
	helpers := r.helper
	r.helper = nil
	r.mu.Unlock()
	if len(helpers) > 0 {
		r.stage("Shutting down background programs")
	}
	for i := len(helpers) - 1; i >= 0; i-- {
		c := helpers[i]
		if c.Process != nil {
			_ = killTree(c.Process.Pid)
		}
	}
}

var logMu sync.Mutex

func appendLauncherLog(line string) {
	logMu.Lock()
	defer logMu.Unlock()
	dir := filepath.Join(exeDir(), "logs")
	_ = os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(filepath.Join(dir, "launcher.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func (r *Runner) startLiveMonitor(lc LauncherConfig, p Paths) error {
	if lc.LiveMonitor == "off" {
		r.setStep("lm", "skipped", "")
		r.logf("Live Monitor is set to not start")
		return nil
	}
	if !fileExists(p.LMExe) {
		r.setStep("lm", "skipped", "not found")
		r.logf("%s not found, skipping the Live Monitor", lmExeName)
		return nil
	}
	if processRunning(lmExeName) {
		r.logf("Live Monitor is already running, using it")
		r.setStep("lm", "done", "already running")
		return nil
	}
	r.setStep("lm", "running", "")
	r.stage("Starting the Live Monitor")
	how := lc.LiveMonitor
	setup := func(c *exec.Cmd) {
		if how == "hide" {
			hideWindow(c)
		}
	}
	exited, err := r.startHelperWith("live-monitor", p.LMExe, nil, p.GameDir, p, setup)
	if err != nil {
		return fmt.Errorf("starting the Live Monitor: %w", err)
	}
	r.mu.Lock()
	pid := r.helper[len(r.helper)-1].Process.Pid
	r.mu.Unlock()
	go tameProcess(pid, how, !lc.LMSound, exited, r.logf)
	// give it a few seconds to come up before the client looks for it
	select {
	case <-exited:
		return errors.New("the Live Monitor closed right after starting. Check that the network adapter setting is right (it is written to lmnfig.ini too)")
	case <-r.stop:
		return errors.New("stopped")
	case <-time.After(6 * time.Second):
	}
	r.setStep("lm", "done", "")
	return nil
}

func (r *Runner) ensureOnline(p Paths) {
	if processRunning(lmExeName) {
		r.logf("Live Monitor is already running, so the online battle mode setting wasn't checked")
		return
	}
	for _, f := range testmodeFiles(p.GameDir) {
		name := filepath.Base(f)
		if !fileExists(f) {
			r.logf("%s not found, skipping the online mode check for it", name)
			continue
		}
		desc, changed, err := ensureOnlineMode(f, p.BackupDir)
		switch {
		case err != nil:
			r.logf("Couldn't check online mode in %s: %v. Switch it on in the Live Monitor's F3 menu if online modes are missing", name, err)
		case changed:
			r.logf("Switched battle mode to online in %s (was: %s). Backup in backups\\", name, desc)
		default:
			r.logf("Online battle mode already on in %s (%s)", name, desc)
		}
	}
}

// StartCardServer starts the card server on its own (for its card editor web
// page) unless it is already running. It is closed when the launcher exits.
func (r *Runner) StartCardServer() error {
	if portOpen(cardPort) {
		return nil
	}
	p := resolvePaths(loadLauncherConfig())
	if !fileExists(p.ServerExe) {
		return fmt.Errorf("card server not found at %s", p.ServerExe)
	}
	if msg := lockCardServer(p); msg != "" {
		r.logf("%s", msg)
	}
	_ = os.MkdirAll(p.LogDir, 0755)
	lf, err := os.Create(filepath.Join(p.LogDir, "card-server-editor.log"))
	if err != nil {
		return err
	}
	cmd := exec.Command(p.ServerExe)
	cmd.Dir = filepath.Dir(p.ServerExe)
	cmd.Stdout, cmd.Stderr = lf, lf
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		lf.Close()
		return err
	}
	addToJob(cmd)
	r.mu.Lock()
	r.solo = cmd
	r.mu.Unlock()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); lf.Close(); close(exited) }()
	r.logf("Started the card server for the card editor")
	for i := 0; i < 180; i++ {
		if portOpen(cardPort) {
			return nil
		}
		select {
		case <-exited:
			return errors.New("the card server closed right after starting (see logs\\card-server-editor.log)")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("the card server didn't start within 90 seconds")
}
