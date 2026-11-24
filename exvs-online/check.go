package main

// Pre-match check: "exvs-online.exe -check". Finds the setup problems that most often stop
// two PCs from connecting, before anyone queues. Read-only, except check-report.txt.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pion/stun/v3"
)

type CheckItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok | warn | fail | info
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type CheckReport struct {
	Items          []CheckItem `json:"items"`
	CanFixFirewall bool        `json:"canFixFirewall"`
}

func (r *CheckReport) add(id, title, status, detail, fix string) {
	r.Items = append(r.Items, CheckItem{id, title, status, detail, fix})
}

func (r *CheckReport) failed() bool {
	for _, it := range r.Items {
		if it.Status == "fail" {
			return true
		}
	}
	return false
}

// checker holds what the checks read; tests swap the parts that touch Windows or the internet.
type checker struct {
	gameDir     string
	cfg         Config
	cfgFound    bool
	ifaces      func() ([]net.Interface, error)
	addrs       func(net.Interface) ([]net.Addr, error)
	systemDir   string
	firewall    func(programs []string) (map[string]string, error) // program -> "allow" | "block" | "none"
	stunMapped  func(local, server string) (string, error)         // returns mapped ip:port
	stunServers []string
	httpGet     func(url string) (time.Duration, error)
	procs       func() []string // running program names
}

func newChecker(gameDir string, cfg Config, cfgFound bool) *checker {
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
	return &checker{gameDir: gameDir, cfg: cfg, cfgFound: cfgFound,
		ifaces: net.Interfaces, addrs: func(i net.Interface) ([]net.Addr, error) { return i.Addrs() },
		systemDir: sys, firewall: windowsFirewall, stunMapped: stunMapped,
		stunServers: []string{"stun.l.google.com:19302", "stun.cloudflare.com:3478"}, procs: runningExes,
		httpGet: func(u string) (time.Duration, error) {
			c := &http.Client{Timeout: 5 * time.Second}
			t := time.Now()
			resp, err := c.Get(u)
			if err != nil {
				return 0, err
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				return 0, fmt.Errorf("HTTP %d (the host may be running an older exvs-online)", resp.StatusCode)
			}
			return time.Since(t), nil
		}}
}

// packSerial is the machine serial the common game copy ships with. Two players with
// the same serial look like one cabinet to the game, so the launcher replaces it.
const packSerial = "31Yf"

// findGameDir looks for vsac29_Release.exe next to exvs-online, one level up, or in the working dir.
func findGameDir(exeDir string) string {
	wd, _ := os.Getwd()
	for _, d := range []string{exeDir, filepath.Dir(exeDir), wd, filepath.Dir(wd)} {
		if _, err := os.Stat(filepath.Join(d, "vsac29_Release.exe")); err == nil {
			return d
		}
	}
	return ""
}

func readIniValue(path, key string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || t[0] == ';' || t[0] == '#' || t[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

func isVPNName(n string) bool {
	l := strings.ToLower(n)
	return strings.Contains(l, "zerotier") || strings.Contains(l, "radmin") || strings.Contains(l, "hamachi") || strings.Contains(l, "tailscale")
}

func (c *checker) ipv4Of(name string) (string, bool, bool) { // ip, adapterExists, isUp
	list, err := c.ifaces()
	if err != nil {
		return "", false, false
	}
	for _, i := range list {
		if i.Name != name {
			continue
		}
		as, _ := c.addrs(i)
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return n.IP.String(), true, i.Flags&net.FlagUp != 0
			}
		}
		return "", true, i.Flags&net.FlagUp != 0
	}
	return "", false, false
}

func (c *checker) adapterNames() []string {
	list, _ := c.ifaces()
	var out []string
	for _, i := range list {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		as, _ := c.addrs(i)
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, i.Name)
				break
			}
		}
	}
	return out
}

var bmValueRe = regexp.MustCompile(`"value"\s*:\s*(-?\d+)`)

// battleModes reads the active setting file's battleMode for groups 0..2 from an svd1 file.
func battleModes(path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not created yet, start the game once")
		}
		return nil, err
	}
	if len(b) < 36 || string(b[:4]) != "svd1" {
		return nil, fmt.Errorf("not an svd1 file")
	}
	p := b[36:]
	get := func(key string) (int, bool) {
		k := bytes.Index(p, []byte(strconv.Quote(key)))
		if k < 0 {
			return 0, false
		}
		end := bytes.IndexByte(p[k:], '}')
		if end < 0 {
			return 0, false
		}
		m := bmValueRe.FindSubmatch(p[k : k+end])
		if m == nil {
			return 0, false
		}
		v, err := strconv.Atoi(string(m[1]))
		return v, err == nil
	}
	file, _ := get(".TestModeParam.gameServerPcb.useSettingFile")
	var out []int
	for g := 0; g < 3; g++ {
		v, ok := get(fmt.Sprintf(".TestModeParam.gameServerPcb.settingFile.[%d].group.[%d].battleMode", file, g))
		if !ok {
			return nil, fmt.Errorf("battle mode for group %d not found", g)
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *checker) programs() []string {
	exe, _ := os.Executable()
	return []string{filepath.Join(c.gameDir, "vsac29_Release.exe"), filepath.Join(c.gameDir, "vsac29_Release_LM.exe"), exe}
}

func (c *checker) Run() *CheckReport {
	r := &CheckReport{}

	// 1. Game files
	if c.gameDir == "" {
		r.add("game", "Game folder", "fail", "Couldn't find vsac29_Release.exe.",
			"Keep the exvs-online folder inside the game folder (next to vsac29_Release.exe), or pass -game <folder>.")
		return r
	}
	missing := []string{}
	for _, f := range []string{"vsac29_Release.exe", "vsac29_Release_LM.exe", "bngrw.dll", "config.ini", "lmnfig.ini"} {
		if _, err := os.Stat(filepath.Join(c.gameDir, f)); err != nil {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		r.add("game", "Game folder", "fail", "Missing: "+strings.Join(missing, ", ")+".", "Re-extract the game and the friend pack into the same folder.")
	} else {
		r.add("game", "Game folder", "ok", c.gameDir, "")
	}

	// 2. Visual C++ runtimes
	if runtime.GOOS == "windows" {
		var miss []string
		for f, name := range map[string]string{"msvcr100.dll": "Visual C++ 2010 x64", "vcruntime140.dll": "Visual C++ 2015-2022 x64"} {
			if _, err := os.Stat(filepath.Join(c.systemDir, f)); err != nil {
				miss = append(miss, name)
			}
		}
		if len(miss) > 0 {
			r.add("vcredist", "Visual C++ runtimes", "fail", "Not installed: "+strings.Join(miss, ", ")+". The Live Monitor closes right away without them.",
				"Install them from Microsoft (see START-HERE.txt step 0).")
		} else {
			r.add("vcredist", "Visual C++ runtimes", "ok", "2010 and 2015-2022 found.", "")
		}
	}

	// 3. Adapter
	cfgIni, lmIni := filepath.Join(c.gameDir, "config.ini"), filepath.Join(c.gameDir, "lmnfig.ini")
	ifName, _ := readIniValue(cfgIni, "InterfaceName")
	lmName, _ := readIniValue(lmIni, "InterfaceName")
	adapterIP := ""
	switch ip, exists, up := c.ipv4Of(ifName); {
	case ifName == "":
		r.add("adapter", "Network adapter", "fail", "config.ini has no InterfaceName.", "Pick your adapter in the launcher's Settings.")
	case !exists:
		r.add("adapter", "Network adapter", "fail", fmt.Sprintf("config.ini says %q, but this PC has no adapter with that name (the game shows \"failed to find interface\"). Adapters here: %s.", ifName, strings.Join(c.adapterNames(), ", ")),
			"Pick your real adapter (Ethernet or Wi-Fi) in the launcher's Settings.")
	case !up || ip == "":
		r.add("adapter", "Network adapter", "fail", fmt.Sprintf("%q exists but isn't connected.", ifName), "Connect it, or pick the adapter you're online with.")
	default:
		adapterIP = ip
		if isVPNName(ifName) {
			r.add("adapter", "Network adapter", "warn", fmt.Sprintf("%q (%s) is a VPN adapter.", ifName, ip),
				"Normally use your real adapter (Ethernet or Wi-Fi). Only use the VPN adapter if real adapters already failed, and then on both PCs.")
		} else {
			r.add("adapter", "Network adapter", "ok", fmt.Sprintf("%s (%s)", ifName, ip), "")
		}
	}
	if lmName != "" && ifName != "" && lmName != ifName {
		r.add("lmadapter", "Live Monitor adapter", "fail", fmt.Sprintf("lmnfig.ini says %q but config.ini says %q.", lmName, ifName), "Save settings once in the launcher; it writes both files.")
	}

	// 4. Serial
	if s, _ := readIniValue(cfgIni, "serial"); s == "" {
		r.add("serial", "Machine serial", "warn", "config.ini has no serial.", "Set one in the launcher's Settings. It must differ from your opponent's.")
	} else if s == packSerial {
		r.add("serial", "Machine serial", "warn", "Yours is "+s+", the one the game copy came with, so other players probably have it too.", "Open the launcher once: it picks a random serial for you. Or type any other 4 letters/digits in Settings.")
	} else {
		r.add("serial", "Machine serial", "info", "Yours is "+s+". It must be different from your opponent's, so compare them once.", "")
	}

	// 4b. SnowLauncher fights Boost Link: it starts the stock card server on port 28080
	// and rewrites config.ini from its own copy.
	if c.procs != nil {
		for _, p := range c.procs() {
			if strings.HasPrefix(strings.ToLower(p), "snowlauncher") {
				r.add("snowlauncher", "SnowLauncher", "warn", p+" is running. It starts its own card server and overwrites config.ini, which breaks Boost Link.", "Close SnowLauncher and start the game from the Boost Link launcher only.")
				break
			}
		}
	}

	// 5. Online mode
	var modes []string
	modeBad := false
	for _, f := range []string{"testmode_pcb.json", "testmode_lm.json"} {
		v, err := battleModes(filepath.Join(c.gameDir, "29", f))
		if err != nil {
			modes = append(modes, f+": unreadable ("+err.Error()+")")
			modeBad = true
			continue
		}
		for _, x := range v {
			if x != 0 {
				modeBad = true
			}
		}
		modes = append(modes, fmt.Sprintf("%s: %v", f, v))
	}
	if modeBad {
		r.add("online", "Online mode", "warn", "Not set to online (店外) yet: "+strings.Join(modes, "; ")+".",
			"Start the game from the launcher in Host or Join mode once; it switches this automatically. Or set it in the Live Monitor's F3 menu.")
	} else {
		r.add("online", "Online mode", "ok", "Set to online.", "")
	}

	// 6. Server address in config.ini
	if srv, _ := readIniValue(cfgIni, "Server"); srv != "" && !strings.HasSuffix(srv, ":28090") {
		r.add("server", "Game server address", "warn", "config.ini points at "+srv+", which skips matchmaking.", "Start from the launcher in Host or Join mode; it sets 127.0.0.1:28090.")
	}

	// 7. VPN
	var vpns []string
	for _, n := range c.adapterNames() {
		if isVPNName(n) {
			ip, _, _ := c.ipv4Of(n)
			vpns = append(vpns, fmt.Sprintf("%s (%s)", n, ip))
		}
	}
	if len(vpns) == 0 {
		r.add("vpn", "VPN (ZeroTier or Radmin)", "info", "None connected. That's fine: Boost Link plays over the internet through the server.", "")
	} else {
		r.add("vpn", "VPN (ZeroTier or Radmin)", "ok", strings.Join(vpns, ", ")+". Hosts share this IP with players.", "")
	}

	// 8. Firewall
	progs := c.programs()
	if fw, err := c.firewall(progs); err != nil {
		r.add("firewall", "Windows Firewall", "info", "Couldn't read firewall rules: "+err.Error(), "")
	} else if fw != nil {
		var blocked, none, priv []string
		for _, p := range progs {
			switch fw[p] {
			case "block":
				blocked = append(blocked, filepath.Base(p))
			case "allow":
			case "private":
				priv = append(priv, filepath.Base(p))
			default:
				none = append(none, filepath.Base(p))
			}
		}
		switch {
		case len(blocked) > 0:
			r.CanFixFirewall = true
			r.add("firewall", "Windows Firewall", "fail", "Blocked: "+strings.Join(blocked, ", ")+". Matches will time out.", "Click Fix firewall (needs admin), or allow them in Windows Security > Firewall > Allow an app.")
		case len(priv) > 0 && len(none) == 0:
			r.CanFixFirewall = true
			r.add("firewall", "Windows Firewall", "warn", "Allowed on private networks only: "+strings.Join(priv, ", ")+". ZeroTier and Radmin usually count as Public, so the other PC can't reach you.", "Click Fix firewall (needs admin).")
		case len(none) > 0 || len(priv) > 0:
			none = append(none, priv...)
			r.CanFixFirewall = true
			r.add("firewall", "Windows Firewall", "warn", "No allow rule yet for: "+strings.Join(none, ", ")+". Incoming connections may be dropped.", "Click Fix firewall (needs admin).")
		default:
			r.add("firewall", "Windows Firewall", "ok", "Game, Live Monitor and exvs-online are allowed.", "")
		}
	}

	// 9. NAT type
	local := "0.0.0.0"
	if adapterIP != "" {
		local = adapterIP
	}
	var mapped []string
	var stunErr error
	for _, s := range c.stunServers {
		m, err := c.stunMapped(local, s)
		if err != nil {
			stunErr = err
			continue
		}
		mapped = append(mapped, m)
	}
	switch {
	case len(mapped) == 0:
		r.add("nat", "Internet (UDP)", "fail", fmt.Sprintf("No reply from any STUN server through your adapter (%v).", stunErr),
			"Check this PC is online. If it is, UDP is being blocked: check firewall or antivirus, or try another network.")
	case len(mapped) == 1:
		r.add("nat", "Internet (UDP)", "info", "Reached the internet; only one STUN server answered, so the NAT type is unknown.", "")
	default:
		_, p1, _ := net.SplitHostPort(mapped[0])
		_, p2, _ := net.SplitHostPort(mapped[1])
		if p1 == p2 {
			r.add("nat", "NAT type", "ok", "Easy NAT: your router keeps the same port for everyone, so direct connections should work.", "")
		} else {
			r.add("nat", "NAT type", "warn", "Strict NAT: your router changes ports per destination, so direct connections may fail.",
				"The server's relay is used automatically when a direct connection fails.")
		}
	}

	// 10. Host
	switch {
	case !c.cfgFound:
		r.add("host", "Match host", "info", "No exvs-online config found. Pick Host or Join in the launcher first.", "")
	case c.cfg.Mode == "both" || c.cfg.Mode == "match":
		msg := "This PC is the host. Players type one of this PC's VPN IPs above as the host."
		if d, err := c.httpGet("http://127.0.0.1:28090/status.json"); err == nil {
			msg += fmt.Sprintf(" Matchmaking is running (%d ms).", d.Milliseconds())
		} else {
			msg += " Matchmaking isn't running right now; it starts with the game."
		}
		r.add("host", "Match host", "info", msg, "")
	default:
		base := strings.TrimRight(c.cfg.Proxy.MatchServer, "/")
		if base == "" {
			r.add("host", "Match host", "fail", "No host address set.", "Type the host's IP in the launcher's Join box.")
			break
		}
		if d, err := c.httpGet(base + "/status.json"); err != nil {
			r.add("host", "Match host", "fail", "Can't reach the host at "+base+": "+shortErr(err),
				"Check the server address in the launcher and your internet connection. If the server is down, tell the server owner.")
		} else {
			st := "ok"
			if d > 250*time.Millisecond {
				st = "warn"
			}
			r.add("host", "Match host", st, fmt.Sprintf("Reached %s in %d ms (the game drops matches over 300 ms).", base, d.Milliseconds()), "")
		}
	}
	return r
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 120 {
		s = s[i+2:]
	}
	return s
}

func stunMapped(local, server string) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return "", err
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(local)})
	if err != nil {
		return "", err
	}
	defer c.Close()
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	buf := make([]byte, 1500)
	for try := 0; try < 3; try++ {
		if _, err := c.WriteToUDP(msg.Raw, raddr); err != nil {
			return "", err
		}
		_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		res := &stun.Message{Raw: buf[:n]}
		if res.Decode() != nil {
			continue
		}
		var xor stun.XORMappedAddress
		if xor.GetFrom(res) == nil {
			return net.JoinHostPort(xor.IP.String(), strconv.Itoa(xor.Port)), nil
		}
	}
	return "", fmt.Errorf("no reply from %s", server)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// windowsFirewall returns allow/block/none per program from inbound rules. nil map off Windows.
func windowsFirewall(programs []string) (map[string]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	var q []string
	for _, p := range programs {
		q = append(q, psQuote(p))
	}
	script := `$ErrorActionPreference='SilentlyContinue'
$want=@(` + strings.Join(q, ",") + `)
$out=@{}; foreach($w in $want){$out[$w]='none'}
foreach($f in Get-NetFirewallApplicationFilter){
  $p=[Environment]::ExpandEnvironmentVariables([string]$f.Program)
  foreach($w in $want){ if($p -ieq $w){
    $r=$f | Get-NetFirewallRule
    foreach($x in $r){ if("$($x.Direction)" -eq 'Inbound' -and "$($x.Enabled)" -eq 'True'){
      $pub = ("$($x.Profile)" -match 'Any|Public')
      if("$($x.Action)" -eq 'Block'){$out[$w]='block'}
      elseif($out[$w] -ne 'block'){ if($pub){$out[$w]='allow'} elseif($out[$w] -ne 'allow'){$out[$w]='private'} } } }
  } }
}
$out | ConvertTo-Json -Compress`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	hideWindow(cmd)
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	res := map[string]string{}
	if err := json.Unmarshal(bytes.TrimSpace(b), &res); err != nil {
		return nil, fmt.Errorf("unexpected output")
	}
	// keys come back as given; match case-insensitively
	out := map[string]string{}
	for _, p := range programs {
		for k, v := range res {
			if strings.EqualFold(k, p) {
				out[p] = v
			}
		}
	}
	return out, nil
}

// fixFirewall adds inbound allow rules (and disables block rules) for the programs, via one
// admin prompt.
func fixFirewall(programs []string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("only on Windows")
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Continue'\n")
	for _, p := range programs {
		name := "Boost Link - " + strings.TrimSuffix(filepath.Base(p), ".exe")
		fmt.Fprintf(&b, "Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Remove-NetFirewallRule\n", psQuote(name))
		fmt.Fprintf(&b, "New-NetFirewallRule -DisplayName %s -Direction Inbound -Action Allow -Program %s -Profile Any | Out-Null\n", psQuote(name), psQuote(p))
		fmt.Fprintf(&b, "foreach($f in Get-NetFirewallApplicationFilter){ if([Environment]::ExpandEnvironmentVariables([string]$f.Program) -ieq %s){ $f | Get-NetFirewallRule | Where-Object { \"$($_.Action)\" -eq 'Block' } | Disable-NetFirewallRule } }\n", psQuote(p))
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("boostlink-firewall-%d.ps1", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	outer := fmt.Sprintf("Start-Process powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File',%s", psQuote(`"`+tmp+`"`))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", outer)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s (was the admin prompt declined?)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func printReport(r *CheckReport) string {
	var b strings.Builder
	b.WriteString("Boost Link pre-match check, " + time.Now().Format("2006-01-02 15:04") + "\n\n")
	for _, it := range r.Items {
		tag := map[string]string{"ok": "[ OK ]", "warn": "[WARN]", "fail": "[FAIL]", "info": "[INFO]"}[it.Status]
		fmt.Fprintf(&b, "%s %s: %s\n", tag, it.Title, it.Detail)
		if it.Fix != "" {
			fmt.Fprintf(&b, "       Fix: %s\n", it.Fix)
		}
	}
	if r.failed() {
		b.WriteString("\nSomething needs fixing before you queue (see FAIL above).\n")
	} else {
		b.WriteString("\nNo blockers found. Good to queue.\n")
	}
	return b.String()
}

func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runCheck implements -check and -fix-firewall. It returns the process exit code.
func runCheck(cfgPath, gameDir string, asJSON, fixOnly bool) int {
	exe, _ := os.Executable()
	if gameDir == "" {
		gameDir = findGameDir(filepath.Dir(exe))
	}
	cfg := defaultConfig()
	found := false
	// An explicit -config wins; otherwise the launcher's generated config (the one actually used),
	// then the one next to this exe.
	cands := []string{cfgPath}
	if cfgPath == "" {
		cands = []string{filepath.Join(gameDir, "exvs-launcher", "exvs-online.json"), filepath.Join(filepath.Dir(exe), "exvs-online.json")}
	}
	for _, p := range cands {
		if p == "" {
			continue
		}
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &cfg) == nil {
			found = true
			break
		}
	}
	c := newChecker(gameDir, cfg, found)
	if fixOnly {
		if err := fixFirewall(c.programs()); err != nil {
			fmt.Fprintln(os.Stderr, "firewall fix failed:", err)
			return 1
		}
		fmt.Println("Firewall rules added.")
		return 0
	}
	r := c.Run()
	text := printReport(r)
	_ = os.WriteFile(filepath.Join(filepath.Dir(exe), "check-report.txt"), []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0644)
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(r)
	} else {
		fmt.Print(text)
		if r.CanFixFirewall && interactive() {
			fmt.Print("\nAdd firewall rules now? Windows will ask for admin permission. [Y/n] ")
			ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.TrimSpace(strings.ToLower(ans)); a == "" || a == "y" || a == "yes" {
				if err := fixFirewall(c.programs()); err != nil {
					fmt.Println("Didn't work:", err)
				} else {
					fmt.Println("Done. Run the check again to confirm.")
				}
			}
		}
		if interactive() {
			fmt.Print("\nPress Enter to close.")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
	}
	if r.failed() {
		return 1
	}
	return 0
}
