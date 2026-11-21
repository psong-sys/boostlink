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
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	flag.Parse()
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

	switch cfg.Mode {
	case "match":
		log.Printf("match server listening on %s", cfg.Match.Listen)
		if err := startTurn(cfg.Proxy.LocalTurn, cfg.Proxy.Stun, tl); err != nil {
			log.Printf("relay disabled: %v", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/status", m.StatusHandler())
		mux.Handle("/status.json", m.StatusHandler())
		mux.HandleFunc("/turninfo", turnInfoHandler)
		mux.Handle("/report", m.ReportHandler())
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
		log.Fatal(http.ListenAndServe(cfg.Match.Listen, mux))
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
		log.Printf("%s mode: listening on %s, card server %s", cfg.Mode, cfg.Proxy.Listen, cfg.Proxy.Upstream)
		log.Fatal(http.ListenAndServe(cfg.Proxy.Listen, p))
	default:
		log.Fatalf("unknown mode %q (use proxy, match or both)", cfg.Mode)
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
