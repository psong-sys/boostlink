package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	gameExeName   = "vsac29_Release.exe"
	lmExeName     = "vsac29_Release_LM.exe"
	cardPort      = 28080
	proxyPort     = 28090
	uiAddr        = "127.0.0.1:28077"
	offlineServer = "127.0.0.1:28080"
	defaultServer = "149.28.115.108" // the public Boost Link match server
	onlineServer  = "127.0.0.1:28090"
)

// LauncherConfig is the launcher's own settings, kept in launcher.json next to
// the exe. Nothing here is read by the game.
type LauncherConfig struct {
	Mode      string `json:"mode"`      // offline | host | friend
	HostIP    string `json:"hostIp"`    // friend mode: the host's Radmin VPN IP
	GameDir   string `json:"gameDir"`   // folder with vsac29_Release.exe and config.ini
	OnlineExe string `json:"onlineExe"` // exvs-online.exe
	// LiveMonitor: how to run vsac29_Release_LM.exe, which the client needs
	// running or it shows LIVEMONITOR DISCONNECT. hide | minimize | show | off
	LiveMonitor string   `json:"liveMonitor"`
	LMSound     bool     `json:"lmSound"`               // false = mute the Live Monitor
	RecentHosts []string `json:"recentHosts,omitempty"` // last host IPs joined, newest first
	SetupDone   bool     `json:"setupDone"`
	Advanced    bool     `json:"advanced"`       // show Host mode and the server address
	ServerSet   bool     `json:"serverMigrated"` // switched to the public server once             // first-run setup finished
	// CardMobileSuit is the suit drawn on the card on the game's "Use this card?"
	// screen. Server-Infinite sends none ("NOT SET"), so exvs-online fills it in.
	// Online modes only. 0 = the card's first favorite suit.
	CardMobileSuit uint32 `json:"cardMobileSuit,omitempty"`
	// TeamCode: two players who type the same code are put on the same side of
	// a 2v2 by the match server. Empty = normal matching (1v1 or random 2v2).
	TeamCode string `json:"teamCode,omitempty"`
}

type Paths struct {
	LauncherDir string
	GameDir     string
	ConfigIni   string
	CardIni     string
	GameExe     string
	LMExe       string
	LMIni       string
	ServerExe   string
	OnlineExe   string
	OnlineCfg   string // generated exvs-online config (in the launcher folder)
	BackupDir   string
	LogDir      string
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func launcherJSONPath() string { return filepath.Join(exeDir(), "launcher.json") }

func defaultGameDir() string {
	d := exeDir()
	for _, c := range []string{filepath.Dir(d), d} {
		if fileExists(filepath.Join(c, gameExeName)) {
			return c
		}
	}
	return filepath.Dir(d)
}

func loadLauncherConfig() LauncherConfig {
	c := LauncherConfig{Mode: "offline"}
	if b, err := os.ReadFile(launcherJSONPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Mode == "" {
		c.Mode = "offline"
	}
	if c.LiveMonitor == "" {
		c.LiveMonitor = "hide"
	}
	// One-time move to the public server: online play now means joining it,
	// so old VPN host IPs and Host mode are dropped unless Advanced is on.
	if !c.ServerSet {
		if !c.Advanced {
			if net.ParseIP(c.HostIP) != nil || c.HostIP == "" {
				c.HostIP = defaultServer
			}
			if c.Mode == "host" {
				c.Mode = "friend"
			}
		}
		c.ServerSet = true
	}
	if c.HostIP == "" {
		c.HostIP = defaultServer
	}
	return c
}

func saveLauncherConfig(c LauncherConfig) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(launcherJSONPath(), b, 0644)
}

func resolvePaths(c LauncherConfig) Paths {
	p := Paths{LauncherDir: exeDir()}
	p.GameDir = c.GameDir
	if p.GameDir == "" {
		p.GameDir = defaultGameDir()
	}
	p.ConfigIni = filepath.Join(p.GameDir, "config.ini")
	p.CardIni = filepath.Join(p.GameDir, "card.ini")
	p.GameExe = filepath.Join(p.GameDir, gameExeName)
	p.LMExe = filepath.Join(p.GameDir, lmExeName)
	p.LMIni = filepath.Join(p.GameDir, "lmnfig.ini")
	p.ServerExe = filepath.Join(p.GameDir, "Server", "Server-Infinite.exe")
	p.OnlineExe = c.OnlineExe
	if p.OnlineExe == "" {
		p.OnlineExe = filepath.Join(p.GameDir, "exvs-online", "exvs-online.exe")
	}
	p.OnlineCfg = filepath.Join(p.LauncherDir, "exvs-online.json")
	p.BackupDir = filepath.Join(p.LauncherDir, "backups")
	p.LogDir = filepath.Join(p.LauncherDir, "logs")
	return p
}

// ---- settings exchanged with the UI ----

type GameSettings struct {
	Serial        string `json:"serial"`
	InterfaceName string `json:"interfaceName"`
	Windowed      bool   `json:"windowed"`
	Borderless    bool   `json:"borderless"`
	Resolution    string `json:"resolution"`
	FramerateLim  bool   `json:"framerateLimit"`
	Server        string `json:"server"` // read-only in the UI; set from the mode
}

type CardSettings struct {
	ChipID     string `json:"chipId"`
	AccessCode string `json:"accessCode"`
}

type Settings struct {
	Launcher   LauncherConfig `json:"launcher"`
	Game       GameSettings   `json:"game"`
	Card       CardSettings   `json:"card"`
	Keyboard   []KV           `json:"keyboard"`
	Controller []KV           `json:"controller"`
}

type Detected struct {
	GameDir     string   `json:"gameDir"`
	GameFound   bool     `json:"gameFound"`
	ConfigFound bool     `json:"configFound"`
	ServerFound bool     `json:"serverFound"`
	OnlineFound bool     `json:"onlineFound"`
	LMFound     bool     `json:"lmFound"`
	OnlineExe   string   `json:"onlineExe"`
	VpnIPs      []string `json:"vpnIps"`
	Adapters    []string `json:"adapters"`
	StockCard   bool     `json:"stockCard"`
	FirstRun    bool     `json:"firstRun"`
	Suggested   string   `json:"suggestedAdapter"`
	NoVCRT2010  bool     `json:"noVcrt2010"`
	Version     string   `json:"version"`
	Server      string   `json:"defaultServer"`
	NewSerial   bool     `json:"newSerial"` // serial was the pack default and got replaced
}

// packSerial is the machine serial the common game copy ships with.
const packSerial = "31Yf"

// randomSerial makes a serial shaped like the pack's (two digits, an upper and a lower
// case letter), never the pack's own.
func randomSerial() string {
	const digits, upper, lower = "0123456789", "ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnpqrstuvwxyz"
	pick := func(set string) byte {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return set[time.Now().UnixNano()%int64(len(set))]
		}
		return set[n.Int64()]
	}
	for {
		s := string([]byte{pick(digits), pick(digits), pick(upper), pick(lower)})
		if s != packSerial {
			return s
		}
	}
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func readSettings() (Settings, Detected, error) {
	lc := loadLauncherConfig()
	p := resolvePaths(lc)
	s := Settings{Launcher: lc}
	d := Detected{
		GameDir:     p.GameDir,
		GameFound:   fileExists(p.GameExe),
		ConfigFound: fileExists(p.ConfigIni),
		ServerFound: fileExists(p.ServerExe),
		OnlineFound: fileExists(p.OnlineExe),
		LMFound:     fileExists(p.LMExe),
		OnlineExe:   p.OnlineExe,
	}
	if !d.ConfigFound {
		return s, d, fmt.Errorf("config.ini not found in %s", p.GameDir)
	}
	cfg, err := LoadIni(p.ConfigIni)
	if err != nil {
		return s, d, err
	}
	g := func(k string) string { v, _ := cfg.Get("config", k); return v }
	s.Game = GameSettings{
		Serial:        g("serial"),
		InterfaceName: g("InterfaceName"),
		Windowed:      truthy(g("Windowed")),
		Borderless:    truthy(g("BorderlessWindow")),
		Server:        g("Server"),
	}
	if s.Game.Serial == "" || s.Game.Serial == packSerial {
		// Everyone who copied the same game folder has the same serial, and the game and
		// the match server treat two players with one serial as one cabinet. Pick a random
		// one; it's written to config.ini on the next save or start.
		s.Game.Serial = randomSerial()
		d.NewSerial = true
	}
	s.Game.Resolution, _ = cfg.Get("display", "Resolution")
	fl, _ := cfg.Get("display", "FramerateLimit")
	s.Game.FramerateLim = truthy(fl)
	s.Keyboard = cfg.Entries("keyboard")
	s.Controller = cfg.Entries("controller")

	if card, err := LoadIni(p.CardIni); err == nil {
		s.Card.ChipID, _ = card.Get("card", "chipId")
		s.Card.AccessCode, _ = card.Get("card", "accessCode")
	}
	d.StockCard = s.Card.ChipID == "exvs2ib123" || s.Card.AccessCode == "123456"
	// the address friends type into "host IP" is the Radmin one, whatever
	// adapter the game itself uses
	d.VpnIPs = vpnIPs()
	d.Adapters = adapterNames()
	d.Suggested = suggestAdapter()
	d.FirstRun = !lc.SetupDone
	d.NoVCRT2010 = missingVCRT2010()
	d.Version = version
	d.Server = defaultServer
	return s, d, nil
}

func interfaceIPs(name string) []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, it := range ifs {
		if name != "" && !strings.EqualFold(it.Name, name) {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func validate(s Settings) error {
	switch s.Launcher.Mode {
	case "offline", "host":
	case "friend":
		if !validHost(normalizeHost(s.Launcher.HostIP)) {
			return errors.New("enter the host's IP (like 26.12.34.56) or server address (like play.example.com) for Join mode")
		}
	default:
		return fmt.Errorf("unknown mode %q", s.Launcher.Mode)
	}
	switch s.Launcher.LiveMonitor {
	case "", "hide", "minimize", "show", "off":
	default:
		return fmt.Errorf("unknown Live Monitor setting %q", s.Launcher.LiveMonitor)
	}
	if strings.ContainsAny(s.Game.Serial+s.Card.ChipID+s.Card.AccessCode, "\r\n=") {
		return errors.New("serial, chip ID and access code can't contain line breaks or '='")
	}
	return nil
}

// backup copies path into the backups folder with a timestamp. The first
// backup of each file is also kept as <name>.original and never overwritten.
func backup(path, dir string) error {
	if !fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	base := filepath.Base(path)
	orig := filepath.Join(dir, base+".original")
	if !fileExists(orig) {
		if err := copyFile(path, orig); err != nil {
			return err
		}
	}
	return copyFile(path, filepath.Join(dir, base+"."+time.Now().Format("20060102-150405")))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// setBool writes a True/False key, but doesn't add a missing key just to say False.
// safeScreen keeps the game at 60 FPS: the frame limit is always on, and the
// game stays out of true fullscreen (Windowed=False), where bngrw.dll switches
// the limiter off. Without it the whole game runs too fast on monitors above
// 60 Hz. Borderless keeps the limit.
func safeScreen(g GameSettings) GameSettings {
	g.FramerateLim = true
	if !g.Windowed {
		g.Windowed = true
		g.Borderless = true
	}
	return g
}

func setBool(f *IniFile, section, key string, v bool) {
	if v || f.Has(section, key) {
		f.Set(section, key, boolStr(v))
	}
}

// writeSettings saves launcher.json, then updates config.ini and card.ini
// (after backing them up) only if something actually changed.
func writeSettings(s Settings) error {
	if err := validate(s); err != nil {
		return err
	}
	s.Launcher.HostIP = normalizeHost(s.Launcher.HostIP)
	if t := strings.TrimSpace(s.Launcher.TeamCode); len(t) > 24 {
		s.Launcher.TeamCode = t[:24]
	} else {
		s.Launcher.TeamCode = t
	}
	if err := saveLauncherConfig(s.Launcher); err != nil {
		return fmt.Errorf("saving launcher.json: %w", err)
	}
	p := resolvePaths(s.Launcher)

	cfg, err := LoadIni(p.ConfigIni)
	if err != nil {
		return fmt.Errorf("reading config.ini: %w", err)
	}
	before := cfg.String()
	server := offlineServer
	if s.Launcher.Mode != "offline" {
		server = onlineServer
	}
	cfg.Set("config", "Server", server)
	cfg.Set("config", "serial", s.Game.Serial)
	cfg.Set("config", "InterfaceName", s.Game.InterfaceName)
	s.Game = safeScreen(s.Game)
	cfg.Set("config", "Windowed", boolStr(s.Game.Windowed))
	setBool(cfg, "config", "BorderlessWindow", s.Game.Borderless)
	if s.Game.Resolution != "" {
		cfg.Set("display", "Resolution", s.Game.Resolution)
	}
	setBool(cfg, "display", "FramerateLimit", s.Game.FramerateLim)
	for _, kv := range s.Keyboard {
		if cfg.Has("keyboard", kv.Key) {
			cfg.Set("keyboard", kv.Key, strings.TrimSpace(kv.Value))
		}
	}
	for _, kv := range s.Controller {
		if cfg.Has("controller", kv.Key) {
			cfg.Set("controller", kv.Key, strings.TrimSpace(kv.Value))
		}
	}
	if cfg.String() != before {
		if err := backup(p.ConfigIni, p.BackupDir); err != nil {
			return fmt.Errorf("backing up config.ini: %w", err)
		}
		if err := cfg.Save(p.ConfigIni); err != nil {
			return fmt.Errorf("writing config.ini: %w", err)
		}
	}

	// The Live Monitor has its own config; its adapter must match the client's
	// or it fails with "failed to find interface".
	if lm, err := LoadIni(p.LMIni); err == nil {
		before := lm.String()
		if s.Game.InterfaceName != "" {
			lm.Set("config", "InterfaceName", s.Game.InterfaceName)
		}
		// the Live Monitor talks to the card server too, so in online modes it
		// has to go through exvs-online like the client does
		lm.Set("config", "Server", server)
		if lm.String() != before {
			if err := backup(p.LMIni, p.BackupDir); err != nil {
				return fmt.Errorf("backing up lmnfig.ini: %w", err)
			}
			if err := lm.Save(p.LMIni); err != nil {
				return fmt.Errorf("writing lmnfig.ini: %w", err)
			}
		}
	}

	card, err := LoadIni(p.CardIni)
	if err != nil {
		card = ParseIni("")
		card.crlf = true
	}
	before = card.String()
	card.Set("card", "chipId", strings.TrimSpace(s.Card.ChipID))
	card.Set("card", "accessCode", strings.TrimSpace(s.Card.AccessCode))
	if card.String() != before {
		if err := backup(p.CardIni, p.BackupDir); err != nil {
			return fmt.Errorf("backing up card.ini: %w", err)
		}
		if err := card.Save(p.CardIni); err != nil {
			return fmt.Errorf("writing card.ini: %w", err)
		}
	}
	return nil
}

// writeOnlineConfig builds the exvs-online config for host or friend mode.
// It starts from the template shipped next to exvs-online.exe, so it follows
// whatever that tool expects, and writes the result into the launcher folder.
func writeOnlineConfig(lc LauncherConfig, p Paths) error {
	tmplName := "exvs-online.host.json"
	if lc.Mode == "friend" {
		tmplName = "exvs-online.friend.json"
	}
	var cfg map[string]any
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(p.OnlineExe), tmplName)); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", tmplName, err)
		}
	} else {
		_ = json.Unmarshal([]byte(defaultOnlineTemplate(lc.Mode)), &cfg)
	}
	if lc.Mode == "friend" {
		proxy, _ := cfg["proxy"].(map[string]any)
		if proxy == nil {
			proxy = map[string]any{}
			cfg["proxy"] = proxy
		}
		proxy["matchServer"] = fmt.Sprintf("http://%s:%d", lc.HostIP, proxyPort)
		// Only this PC's game talks to the proxy in Join mode, so nobody else
		// on the network can reach the card server through it.
		proxy["listen"] = onlineServer
	}
	if lc.Mode == "friend" && strings.TrimSpace(lc.TeamCode) != "" {
		proxy, _ := cfg["proxy"].(map[string]any)
		if proxy == nil {
			proxy = map[string]any{}
			cfg["proxy"] = proxy
		}
		proxy["teamCode"] = strings.TrimSpace(lc.TeamCode)
	}
	if lc.CardMobileSuit > 0 {
		proxy, _ := cfg["proxy"].(map[string]any)
		if proxy == nil {
			proxy = map[string]any{}
			cfg["proxy"] = proxy
		}
		proxy["cardMobileSuit"] = lc.CardMobileSuit
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(p.OnlineCfg, b, 0644)
}

func defaultOnlineTemplate(mode string) string {
	if mode == "friend" {
		return `{"mode":"proxy","proxy":{"listen":"127.0.0.1:28090","upstream":"http://127.0.0.1:28080","matchPort":28090,"stun":{"host":"stun.l.google.com","port":19302},"logTraffic":true}}`
	}
	return `{"mode":"both","proxy":{"listen":"0.0.0.0:28090","upstream":"http://127.0.0.1:28080","matchPort":28090,"stun":{"host":"stun.l.google.com","port":19302},"logTraffic":true},"match":{"listen":"0.0.0.0:28100","roomSize":2,"fallbackRoomSize":2,"fallbackAfterSeconds":0,"groupIdBase":0,"waitCode":"PROCESS_WAIT","requireSameWorld":true}}`
}

// adapterNames lists this PC's network adapters that are up and have an IPv4
// address, by the name Windows shows (what InterfaceName must match).
func adapterNames() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(interfaceIPs(it.Name)) > 0 {
			out = append(out, it.Name)
		}
	}
	return out
}

// checkAdapter catches the game's "failed to find interface" before starting.
func checkAdapter(name string) error {
	names := adapterNames()
	if len(names) == 0 {
		return nil // couldn't list adapters; let the game decide
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return nil
		}
	}
	return fmt.Errorf("the network adapter %q isn't on this PC (or isn't connected), so the game would fail with \"failed to find interface\". "+
		"Set \"Network adapter\" to one of: %s. Use your real connection (Ethernet or Wi-Fi), not Radmin VPN",
		name, strings.Join(names, ", "))
}

// vpnIPs lists this PC's Radmin VPN and ZeroTier addresses, labeled, for
// friends to type into "host IP".
func vpnIPs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		low := strings.ToLower(it.Name)
		label := ""
		switch {
		case strings.Contains(low, "radmin"):
			label = "Radmin"
		case strings.Contains(low, "zerotier"):
			label = "ZeroTier"
		default:
			continue
		}
		for _, ip := range interfaceIPs(it.Name) {
			out = append(out, ip+" ("+label+")")
		}
	}
	return out
}

// suggestAdapter guesses the PC's real internet adapter: the one Windows would
// use to reach the internet, skipping VPN and virtual adapters.
func suggestAdapter() string {
	skip := []string{"radmin", "zerotier", "vethernet", "virtualbox", "vmware", "hyper-v", "tailscale", "bluetooth", "loopback", "wsl", "hamachi"}
	isVirtual := func(n string) bool {
		low := strings.ToLower(n)
		for _, s := range skip {
			if strings.Contains(low, s) {
				return true
			}
		}
		return false
	}
	// the local address of a UDP "connection" (nothing is sent) is the one on
	// the default route
	if c, err := net.Dial("udp", "8.8.8.8:53"); err == nil {
		local := c.LocalAddr().(*net.UDPAddr).IP.String()
		c.Close()
		ifs, _ := net.Interfaces()
		for _, it := range ifs {
			for _, ip := range interfaceIPs(it.Name) {
				if ip == local && !isVirtual(it.Name) {
					return it.Name
				}
			}
		}
	}
	for _, n := range adapterNames() {
		if !isVirtual(n) {
			return n
		}
	}
	return ""
}

var hostnameRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// normalizeHost accepts what people paste: "http://play.example.com:28090/status"
// becomes "play.example.com".
func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

func validHost(h string) bool {
	if ip := net.ParseIP(h); ip != nil {
		return ip.To4() != nil
	}
	return len(h) <= 253 && hostnameRe.MatchString(h) && strings.ContainsAny(h, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}
