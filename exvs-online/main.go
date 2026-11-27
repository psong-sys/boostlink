// exvs-online: a matchmaking proxy and server for EXVS2 Infinite Boost online versus.
//
// proxy mode  (each player's PC): sits between the game and Server-Infinite, passes all
//
//	card-server traffic through, adds STUN/TURN/match addresses to RegisterPcb,
//	and forwards matchmaking calls to the shared match server.
//
// match mode  (one host for the group): pairs queued players into rooms.
// both mode   : proxy with the match server built in (for testing on a LAN or one PC).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HostPort struct {
	Host string `json:"host"`
	Port uint32 `json:"port"`
	User string `json:"user,omitempty"`
	Pass string `json:"pass,omitempty"`
}

type ProxyConfig struct {
	Listen      string           `json:"listen"`      // what config.ini Server= points at
	Upstream    string           `json:"upstream"`    // Server-Infinite
	MatchServer string           `json:"matchServer"` // shared match server base URL (ignored in "both" mode)
	MatchPort   uint32           `json:"matchPort"`   // port value written into the SRV_MATCH entry if we add one
	Stun        *HostPort        `json:"stun"`        // host "auto" = this PC's LAN address
	LocalStun   *LocalStunConfig `json:"localStun"`   // run a STUN server inside exvs-online
	Turn        *HostPort        `json:"turn"`        // host "auto" = the match host's built-in relay (/turninfo)
	LocalTurn   *LocalTurnConfig `json:"localTurn"`   // run a TURN relay inside exvs-online (host)
	LogTraffic  bool             `json:"logTraffic"`
	// Online versus rules added to LoadGameData when the card server leaves them out
	// (Server-Infinite does). Values follow the POC server's defaults.
	OnlineTimeLimitSeconds uint32 `json:"onlineTimeLimitSeconds"`
	OnlineDamageLevel      uint32 `json:"onlineDamageLevel"`
	// LMIPAddresses goes into RegisterPcb.lm_ip_addresses: the Live Monitors this PC may use.
	// Empty (default) = untouched. ["auto"] = this PC's own LAN IPv4 addresses, an untested
	// attempt to stop two PCs on one home network seeing each other's LM (error 21-11).
	LMIPAddresses []string `json:"lmIpAddresses,omitempty"`
	// CardMobileSuit is the mobile suit (mst_mobile_suit_id) drawn on the card on the
	// "Use this card?" screen. 0 = the card's first favorite suit, if it has one.
	CardMobileSuit uint32 `json:"cardMobileSuit,omitempty"`
	// TeamCode: players who use the same code are put on the same side of a 2v2.
	// With a code set, this PC only gets 2v2 rooms (never the 1v1 fallback).
	TeamCode string `json:"teamCode,omitempty"`
	// After each online match the result goes to the match server's leaderboard
	// (status page) under the card's pilot name. StatsName replaces that name;
	// NoShareStats keeps results on this PC only.
	StatsName    string `json:"statsName,omitempty"`
	NoShareStats bool   `json:"noShareStats,omitempty"`
}

type MatchConfig struct {
	Listen               string `json:"listen"`
	RoomSize             int    `json:"roomSize"`             // players per room (4 for 2v2)
	FallbackRoomSize     int    `json:"fallbackRoomSize"`     // smaller room allowed after waiting
	FallbackAfterSeconds int    `json:"fallbackAfterSeconds"` // 0 disables the fallback
	GroupIDBase          uint32 `json:"groupIdBase"`          // team ids start here (0 or 1, unknown which the game wants)
	WaitCode             string `json:"waitCode"`             // CHECK_MATCHING reply while waiting. The game treats any non-zero code as a failure (seen in Ghidra), so "SUCCESS" with no clients means "keep waiting".
	RequireSameWorld     bool   `json:"requireSameWorld"`
	TestBots             int    `json:"testBots"` // solo testing: fill each room with this many fake players
	// Small-scene options.
	RTTLimitMs                  uint32 `json:"rttLimitMs,omitempty"`                  // ping limit sent to every player's game (0 = card server's 300)
	DelayFrameLimit             uint32 `json:"delayFrameLimit,omitempty"`             // input-delay frame limit (0 = card server's 5)
	RetryFailedPairAfterSeconds int    `json:"retryFailedPairAfterSeconds,omitempty"` // re-pair players who failed to connect after this wait (default 45)
	DiscordWebhook              string `json:"discordWebhook,omitempty"`              // post when someone starts searching
	DiscordCooldownMinutes      int    `json:"discordCooldownMinutes,omitempty"`      // default 10
	PublicStatusURL             string `json:"publicStatusUrl,omitempty"`             // added to the Discord post
	DiscordInvite               string `json:"discordInvite,omitempty"`               // "Join the Discord" button on the home page
	SiteTagline                 string `json:"siteTagline,omitempty"`                 // home page subtitle
	// StoreLink is the UDP address of the Store Team relay (Route Battle co-op).
	// Empty = the same port as listen, on UDP. "off" turns it off.
	StoreLink string `json:"storeLink,omitempty"`
}

type Config struct {
	Mode  string      `json:"mode"` // proxy | match | both
	Proxy ProxyConfig `json:"proxy"`
	Match MatchConfig `json:"match"`
}

func defaultConfig() Config {
	return Config{
		Mode: "both",
		Proxy: ProxyConfig{
			Listen:                 "0.0.0.0:28090",
			Upstream:               "http://127.0.0.1:28080",
			MatchPort:              28090,
			Stun:                   &HostPort{Host: "stun.l.google.com", Port: 19302},
			LogTraffic:             true,
			OnlineTimeLimitSeconds: 230,
			OnlineDamageLevel:      2,
		},
		Match: MatchConfig{
			Listen:               "0.0.0.0:28100",
			RoomSize:             4,
			FallbackRoomSize:     2,
			FallbackAfterSeconds: 0,
			GroupIDBase:          0,
			WaitCode:             "SUCCESS",
			RequireSameWorld:     true,
		},
	}
}

func main() {
	exe, _ := os.Executable()
	cfgPath := flag.String("config", filepath.Join(filepath.Dir(exe), "exvs-online.json"), "config file")
	check := flag.Bool("check", false, "run the pre-match check and exit")
	asJSON := flag.Bool("json", false, "with -check: print the result as JSON")
	gameDir := flag.String("game", "", "with -check: the game folder (default: found automatically)")
	fixFW := flag.Bool("fix-firewall", false, "add Windows Firewall allow rules (shows an admin prompt) and exit")
	probe := flag.Bool("probe", false, "read the running game's screen steps into hang-probe.txt and exit")
	patchGame := flag.Bool("patch-game", false, "apply the result-screen fix (game exe) and the keyboard focus fix (bngrw.dll), then exit")
	unpatchGame := flag.Bool("unpatch-game", false, "undo the result-screen and keyboard focus fixes, then exit")
	storeLink := flag.Bool("storelink", false, "Store Team mode (needs admin): join the team network for Route Battle co-op")
	slServer := flag.String("server", "", "with -storelink: the match server (IP or IP:port)")
	slTeam := flag.String("team", "", "with -storelink: the team code")
	slStatusAddr := flag.String("status", "127.0.0.1:28094", "with -storelink: where the launcher reads the status")
	slParent := flag.Int("parent", 0, "with -storelink: stop when this process exits")
	logFile := flag.String("log", "", "also write the log to this file")
	lightLM := flag.Bool("light-lm", false, "run the Light Live Monitor (instead of vsac29_Release_LM.exe) until killed")
	lmAdapter := flag.String("adapter", "", "with -light-lm: the network adapter name (InterfaceName) or its IPv4 address")
	lmSerial := flag.String("lm-serial", "", "with -light-lm: the 12-digit Live Monitor serial (blank works)")
	lmDecodeFile := flag.String("lm-decode", "", "print a Live Monitor recording (lm-capture-*.jsonl) and exit")
	lmDecodeFull := flag.Bool("full", false, "with -lm-decode: print whole messages")
	flag.Parse()
	if *logFile != "" {
		if f, err := os.OpenFile(*logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}
	if *lightLM {
		st := ""
		if isFlagSet("status") {
			st = *slStatusAddr
		}
		if err := runLightLM(*lmAdapter, *gameDir, st, *lmSerial); err != nil {
			log.Printf("light LM: %v", err)
			os.Exit(1)
		}
		return
	}
	if *lmDecodeFile != "" {
		if err := runLMDecode(*lmDecodeFile, os.Stdout, *lmDecodeFull); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}
	if *storeLink {
		log.Printf("Boost Link Store Team: server %s", *slServer)
		if err := runStoreLink(*slServer, *slTeam, *slStatusAddr, *slParent); err != nil {
			log.Printf("store team: %v", err)
			os.Exit(1)
		}
		log.Printf("store team: stopped")
		return
	}
	if *patchGame || *unpatchGame {
		dir := *gameDir
		if dir == "" {
			dir = findGameDir(filepath.Dir(exe))
		}
		if dir == "" {
			fmt.Println("couldn't find vsac29_Release.exe; pass -game <folder>")
			os.Exit(1)
		}
		fexe, fdll := patchGameExe, patchFocusDLL
		if *unpatchGame {
			fexe, fdll = unpatchGameExe, unpatchFocusDLL
		}
		failed := false
		if exe, err := os.ReadFile(filepath.Join(dir, "vsac29_Release.exe")); err == nil {
			if p, err := parsePE(exe); err == nil {
				fmt.Println("game build:", buildName(p))
			}
			if dll, err := os.ReadFile(filepath.Join(dir, "bngrw.dll")); err == nil {
				if err := checkDLLMatchesExe(dll, exe); err != nil {
					fmt.Println("warning:", err)
				}
			}
		}
		if msg, err := fexe(filepath.Join(dir, "vsac29_Release.exe")); err != nil {
			fmt.Println("result-screen fix:", err)
			failed = true
		} else {
			fmt.Println("result-screen fix:", msg)
		}
		if msg, err := fdll(filepath.Join(dir, "bngrw.dll")); err != nil {
			fmt.Println("keyboard focus fix:", err)
			failed = true
		} else {
			fmt.Println("keyboard focus fix:", msg)
		}
		if failed {
			os.Exit(1)
		}
		return
	}
	if *probe {
		fmt.Println("Boost Link game probe: reads which screen step the game is in. Leave the game running.")
		probeSay = func(format string, a ...any) { fmt.Printf(format+"\n", a...) }
		out := filepath.Join(filepath.Dir(exe), "hang-probe-"+time.Now().Format("20060102-150405")+".txt")
		if err := runProbe(out); err != nil {
			fmt.Println("probe failed:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", out)
		return
	}
	if *check || *fixFW {
		cp := ""
		if isFlagSet("config") {
			cp = *cfgPath
		}
		os.Exit(runCheck(cp, *gameDir, *asJSON, *fixFW))
	}

	cfg := defaultConfig()
	if b, err := os.ReadFile(*cfgPath); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			log.Fatalf("bad config %s: %v", *cfgPath, err)
		}
	} else {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile(*cfgPath, b, 0644)
		log.Printf("wrote default config to %s", *cfgPath)
	}
	if flag.NArg() > 0 {
		cfg.Mode = flag.Arg(0)
	}

	tl := newTrafficLog(filepath.Join(filepath.Dir(*cfgPath), "traffic.log"), cfg.Proxy.LogTraffic)
	m := NewMatcher(cfg.Match)
	if cfg.Mode != "proxy" {
		m.stats = newStatBook(filepath.Join(filepath.Dir(*cfgPath), "stats.json"))
	}

	switch cfg.Mode {
	case "match":
		log.Printf("match server listening on %s", cfg.Match.Listen)
		if sl := storeLinkAddr(cfg.Match); sl != "" {
			r := NewStoreRelay()
			if err := r.Listen(sl); err != nil {
				log.Printf("store team relay disabled: %v", err)
			} else {
				m.storeRelay = r
				log.Printf("store team relay on UDP %s", sl)
			}
		}
		if err := startStun(cfg.Proxy.LocalStun, tl); err != nil {
			log.Fatal(err)
		}
		if err := startTurn(cfg.Proxy.LocalTurn, cfg.Proxy.Stun, tl); err != nil {
			log.Printf("relay disabled: %v", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/status", m.StatusHandler())
		mux.Handle("/status.json", m.StatusHandler())
		mux.HandleFunc("/turninfo", turnInfoHandler)
		mux.Handle("/report", m.ReportHandler())
		mux.Handle("/result", m.ResultHandler())
		mux.Handle("/settings", m.SettingsHandler())
		mux.Handle("/home", m.HomeHandler())
		mmsH := m.Handler(tl)
		home := m.HomeHandler()
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/" {
				home.ServeHTTP(w, r)
				return
			}
			mmsH.ServeHTTP(w, r)
		}))
		log.Printf("status page: http://<this server>%s/status", cfg.Match.Listen[strings.LastIndex(cfg.Match.Listen, ":"):])
		log.Fatal(newServer(cfg.Match.Listen, mux).ListenAndServe())
	case "proxy", "both":
		var local *Matcher
		if cfg.Mode == "both" {
			local = m
		}
		if err := startStun(cfg.Proxy.LocalStun, tl); err != nil {
			log.Fatal(err)
		}
		if err := startTurn(cfg.Proxy.LocalTurn, cfg.Proxy.Stun, tl); err != nil {
			log.Printf("relay disabled: %v", err)
		}
		p, err := NewProxy(cfg.Proxy, local, tl)
		if err != nil {
			log.Fatal(err)
		}
		p.hw = newHangWatch(filepath.Dir(*cfgPath), tl)
		if dir := findGameDir(filepath.Dir(exe)); dir != "" {
			if msg, err := patchGameExe(filepath.Join(dir, "vsac29_Release.exe")); err != nil {
				log.Printf("result-screen fix not applied: %v", err)
			} else {
				log.Printf("result-screen fix: %s", msg)
			}
			exe, _ := os.ReadFile(filepath.Join(dir, "vsac29_Release.exe"))
			if dll, err := os.ReadFile(filepath.Join(dir, "bngrw.dll")); err == nil {
				if err := checkDLLMatchesExe(dll, exe); err != nil {
					log.Printf("warning: %v", err)
				}
			}
		}
		log.Printf("%s mode: listening on %s, card server %s", cfg.Mode, cfg.Proxy.Listen, cfg.Proxy.Upstream)
		log.Fatal(newServer(cfg.Proxy.Listen, p).ListenAndServe())
	default:
		log.Fatalf("unknown mode %q (use proxy, match or both)", cfg.Mode)
	}
}

// storeLinkAddr is the Store Team relay's UDP address, or "" when it is off.
func storeLinkAddr(mc MatchConfig) string {
	switch v := strings.TrimSpace(mc.StoreLink); v {
	case "off":
		return ""
	case "":
		if i := strings.LastIndex(mc.Listen, ":"); i >= 0 {
			return ":" + mc.Listen[i+1:]
		}
		return ":28090"
	default:
		return v
	}
}

func isFlagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
