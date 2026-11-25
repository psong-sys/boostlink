package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sampleConfig = "[config]\r\n# Game mode, Client or LM.\r\nmode =Client\r\nserial =18Bt\r\n\r\n# Card server address\r\nServer =127.0.0.1:28080\r\nWindowed =True\r\nInterfaceName=Radmin VPN\r\n\r\n[keyboard]\r\nEnabled =True\r\nA =Z,A,S,F \r\nTest=F7\r\n\r\n[controller]\r\nEnabled =True\r\nTest=9\r\n\r\n[display]\r\nResolution=1080p\r\nFramerateLimit=True\r\n"

func TestIniPreservesLayout(t *testing.T) {
	f := ParseIni(sampleConfig)
	if f.String() != sampleConfig {
		t.Fatal("round trip changed the file")
	}
	f.Set("config", "Server", "127.0.0.1:28090")
	f.Set("controller", "Test", "10")
	f.Set("config", "BorderlessWindow", "False")
	out := f.String()
	if !strings.Contains(out, "Server =127.0.0.1:28090\r\n") {
		t.Error("Server not updated in place:\n" + out)
	}
	if !strings.Contains(out, "# Card server address\r\n") {
		t.Error("comment lost")
	}
	if !strings.Contains(out, "Test=F7") || !strings.Contains(out, "Test=10") {
		t.Error("wrong section edited:\n" + out)
	}
	if !strings.Contains(out, "InterfaceName=Radmin VPN\r\nBorderlessWindow=False\r\n\r\n[keyboard]") {
		t.Error("new key not added at the end of its section:\n" + out)
	}
	if v, _ := f.Get("keyboard", "a"); v != "Z,A,S,F" {
		t.Errorf("Get trimmed value = %q", v)
	}
}

func setupGameDir(t *testing.T) (string, LauncherConfig) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.ini"), []byte(sampleConfig), 0644)
	os.WriteFile(filepath.Join(dir, "lmnfig.ini"), []byte("[config]\r\nMode=LM\r\nServer=127.0.0.1:28080\r\nInterfaceName=Radmin VPN\r\nBootToken=\r\n"), 0644)
	os.WriteFile(filepath.Join(dir, "card.ini"), []byte("[card]\r\nchipId=exvs2ib123\r\naccessCode=123456"), 0644)
	return dir, LauncherConfig{Mode: "offline", GameDir: dir}
}

func TestWriteSettingsBacksUpAndEdits(t *testing.T) {
	dir, lc := setupGameDir(t)
	if err := saveLauncherConfig(lc); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(launcherJSONPath())
	s, d, err := readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !d.StockCard || s.Card.ChipID != "exvs2ib123" || s.Game.Serial != "18Bt" || d.NewSerial {
		t.Fatalf("read wrong values: %+v %+v", s, d)
	}

	// Saving unchanged settings must not touch the files or make backups.
	p := resolvePaths(lc)
	os.RemoveAll(p.BackupDir)
	if err := writeSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.BackupDir); err == nil {
		t.Error("backup made although nothing changed")
	}
	os.RemoveAll(p.BackupDir)

	s.Launcher.Mode = "friend"
	s.Launcher.HostIP = "26.1.2.3"
	s.Card.ChipID = "exvs2ib987"
	s.Keyboard[1].Value = "Q"
	s.Game.InterfaceName = "Ethernet"
	if err := writeSettings(s); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.ini"))
	if !strings.Contains(string(cfg), "Server =127.0.0.1:28090") || !strings.Contains(string(cfg), "A =Q") {
		t.Error("config.ini not updated:\n" + string(cfg))
	}
	lm, _ := os.ReadFile(filepath.Join(dir, "lmnfig.ini"))
	if string(lm) != "[config]\r\nMode=LM\r\nServer=127.0.0.1:28090\r\nInterfaceName=Ethernet\r\nBootToken=\r\n" {
		t.Errorf("lmnfig.ini = %q", lm)
	}
	if _, err := os.Stat(filepath.Join(p.BackupDir, "lmnfig.ini.original")); err != nil {
		t.Error("lmnfig.ini backup missing")
	}
	card, _ := os.ReadFile(filepath.Join(dir, "card.ini"))
	if string(card) != "[card]\r\nchipId=exvs2ib987\r\naccessCode=123456" {
		t.Errorf("card.ini = %q", card)
	}
	orig, err := os.ReadFile(filepath.Join(p.BackupDir, "config.ini.original"))
	if err != nil || string(orig) != sampleConfig {
		t.Error("original config.ini backup missing or wrong")
	}
	if _, err := os.Stat(filepath.Join(p.BackupDir, "card.ini.original")); err != nil {
		t.Error("card.ini backup missing")
	}
	os.RemoveAll(p.BackupDir)

	// back to offline
	s.Launcher.Mode = "offline"
	writeSettings(s)
	cfg, _ = os.ReadFile(filepath.Join(dir, "config.ini"))
	if !strings.Contains(string(cfg), "Server =127.0.0.1:28080") {
		t.Error("offline mode didn't restore Server")
	}
	os.RemoveAll(p.BackupDir)
}

func TestValidate(t *testing.T) {
	s := Settings{Launcher: LauncherConfig{Mode: "friend", HostIP: "HOST_IP_HERE"}}
	if validate(s) == nil {
		t.Error("bad host IP accepted")
	}
	s.Launcher.HostIP = "26.10.20.30"
	if validate(s) != nil {
		t.Error("good host IP rejected")
	}
	s.Card.ChipID = "a\nb"
	if validate(s) == nil {
		t.Error("newline accepted")
	}
}

func TestOnlineConfigFromTemplate(t *testing.T) {
	dir := t.TempDir()
	online := filepath.Join(dir, "exvs-online")
	os.MkdirAll(online, 0755)
	os.WriteFile(filepath.Join(online, "exvs-online.friend.json"),
		[]byte(`{"mode":"proxy","proxy":{"listen":"0.0.0.0:28090","matchServer":"http://HOST_IP_HERE:28090","extra":1}}`), 0644)
	lc := LauncherConfig{Mode: "friend", HostIP: "26.5.6.7", GameDir: dir}
	p := resolvePaths(lc)
	p.OnlineCfg = filepath.Join(dir, "out.json")
	if err := writeOnlineConfig(lc, p); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(p.OnlineCfg)
	json.Unmarshal(b, &got)
	proxy := got["proxy"].(map[string]any)
	if proxy["listen"] != "127.0.0.1:28090" {
		t.Errorf("Join mode proxy listens on %v, want 127.0.0.1:28090", proxy["listen"])
	}
	if proxy["matchServer"] != "http://26.5.6.7:28090" || proxy["extra"] != float64(1) || got["mode"] != "proxy" {
		t.Errorf("got %s", b)
	}
	// host mode with no template falls back to the built-in one
	lc.Mode = "host"
	if err := writeOnlineConfig(lc, p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p.OnlineCfg)
	if !strings.Contains(string(b), `"mode": "both"`) {
		t.Errorf("host config = %s", b)
	}
	if strings.Contains(string(b), "cardMobileSuit") {
		t.Errorf("card picture written while unset: %s", b)
	}
	// the card picture goes to exvs-online's proxy
	lc.Mode, lc.CardMobileSuit = "friend", 135
	if err := writeOnlineConfig(lc, p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p.OnlineCfg)
	got = nil
	json.Unmarshal(b, &got)
	if got["proxy"].(map[string]any)["cardMobileSuit"] != float64(135) {
		t.Errorf("card picture not passed on: %s", b)
	}
	if strings.Contains(string(b), "teamCode") {
		t.Errorf("team code written while unset: %s", b)
	}
	// the team code goes to exvs-online's proxy in Online mode
	lc.TeamCode = " duo "
	if err := writeOnlineConfig(lc, p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p.OnlineCfg)
	got = nil
	json.Unmarshal(b, &got)
	if got["proxy"].(map[string]any)["teamCode"] != "duo" {
		t.Errorf("team code not passed on: %s", b)
	}
}

func TestEnsureOnlineMode(t *testing.T) {
	for _, name := range []string{"testmode_pcb.json", "testmode_lm.json"} {
		orig, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Skip("no sample files")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		hdr, payload, err := readSvd(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		// already online: nothing changes
		os.WriteFile(path, orig, 0644)
		if _, changed, err := ensureOnlineMode(path, filepath.Join(dir, "b")); err != nil || changed {
			t.Fatalf("%s: changed=%v err=%v", name, changed, err)
		}
		// make groups 0 and 2 "in-store" (1), and leave file [4]'s tournament preset alone
		p2 := append([]byte(nil), payload...)
		for _, g := range []int{2, 0} {
			_, s, e, ok := findValue(p2, battleModeKey(0, g))
			if !ok {
				t.Fatal("key not found")
			}
			p2 = append(p2[:s], append([]byte("1"), p2[e:]...)...)
		}
		os.WriteFile(path, encodeSvd(hdr, p2), 0644)
		desc, changed, err := ensureOnlineMode(path, filepath.Join(dir, "b"))
		if err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", name, changed, err)
		}
		if !strings.Contains(desc, "[1 0 1]") {
			t.Errorf("desc = %s", desc)
		}
		hdr3, p3, err := readSvd(path)
		if err != nil {
			t.Fatal("rewritten file invalid:", err)
		}
		if !bytes.Equal(p3, payload) {
			t.Error("payload not restored byte-for-byte")
		}
		if hdr3[4] != hdr[4]+2 {
			t.Errorf("counter %d, want %d", hdr3[4], hdr[4]+2)
		}
		if v, _, _, _ := findValue(p3, battleModeKey(4, 0)); v != 6 {
			t.Error("touched another setting file")
		}
		if _, err := os.Stat(filepath.Join(dir, "b", name+".original")); err != nil {
			t.Error("no backup")
		}
	}
}

func TestHostField(t *testing.T) {
	for in, want := range map[string]string{
		" 26.1.2.3 ": "26.1.2.3", "http://play.boostlink.gg:28090/status": "play.boostlink.gg",
		"play.boostlink.gg": "play.boostlink.gg", "10.0.0.5:28090": "10.0.0.5",
	} {
		if got := normalizeHost(in); got != want || !validHost(got) {
			t.Errorf("%q -> %q valid=%v", in, got, validHost(got))
		}
	}
	for _, bad := range []string{"", "HOST_IP_HERE", "a b", "300.1.1.1", "-x.com"} {
		if validHost(normalizeHost(bad)) && bad != "HOST_IP_HERE" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLocalOnly(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for host, want := range map[string]int{uiAddr: 200, "localhost:28077": 200, "evil.example:28077": 403, "127.0.0.1:28078": 403, "": 403} {
		r := httptest.NewRequest("GET", "/api/settings", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: got %d, want %d", host, w.Code, want)
		}
	}
}

func TestLockKestrel(t *testing.T) {
	in := "\xEF\xBB\xBF{\"Kestrel\":{\"Endpoints\":{\"BaseServer\":{\"Url\": \"http://0.0.0.0:28080\"}}}}"
	want := "\xEF\xBB\xBF{\"Kestrel\":{\"Endpoints\":{\"BaseServer\":{\"Url\": \"http://127.0.0.1:28080\"}}}}"
	if got := string(lockKestrel([]byte(in))); got != want {
		t.Errorf("got %q", got)
	}
	if got := string(lockKestrel([]byte(want))); got != want {
		t.Error("already-locked file changed")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Server", "Configurations"), 0755)
	f := filepath.Join(dir, "Server", "Configurations", "kestrel.json")
	os.WriteFile(f, []byte(in), 0644)
	p := Paths{ServerExe: filepath.Join(dir, "Server", "Server-Infinite.exe")}
	if lockCardServer(p) == "" {
		t.Error("no change reported")
	}
	if b, _ := os.ReadFile(f); string(b) != want {
		t.Errorf("file not locked: %q", b)
	}
	if b, _ := os.ReadFile(f + ".original"); string(b) != in {
		t.Error("no .original backup")
	}
	if lockCardServer(p) != "" {
		t.Error("second run changed something")
	}
}

func TestSafeScreenAvoidsTrueFullscreen(t *testing.T) {
	g := safeScreen(GameSettings{Windowed: false, Borderless: false})
	if !g.Windowed || !g.Borderless || !g.FramerateLim {
		t.Fatalf("true fullscreen should become borderless, got %+v", g)
	}
	w := safeScreen(GameSettings{Windowed: true, Borderless: false})
	if !w.Windowed || w.Borderless {
		t.Fatalf("window mode should stay a window, got %+v", w)
	}
}

func TestPackSerialReplaced(t *testing.T) {
	dir, lc := setupGameDir(t)
	cfgPath := filepath.Join(dir, "config.ini")
	os.WriteFile(cfgPath, []byte(strings.Replace(sampleConfig, "serial =18Bt", "serial =31Yf", 1)), 0644)
	if err := saveLauncherConfig(lc); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(launcherJSONPath())
	s, d, err := readSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !d.NewSerial || s.Game.Serial == packSerial || !regexp.MustCompile(`^[0-9]{2}[A-Z][a-z]$`).MatchString(s.Game.Serial) {
		t.Fatalf("serial %q newSerial %v", s.Game.Serial, d.NewSerial)
	}
	if err := writeSettings(s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), "serial ="+s.Game.Serial+"\r\n") {
		t.Errorf("new serial not saved:\n%s", b)
	}
	if s2, d2, _ := readSettings(); s2.Game.Serial != s.Game.Serial || d2.NewSerial {
		t.Error("serial changed again on the next read")
	}
}

func TestCommentIpAddress(t *testing.T) {
	f := ParseIni("[config]\nInterfaceName=Ethernet\nIpAddress=26.1.2.3\n# IpAddress=10.0.0.1\n[keyboard]\nIpAddress=x\n")
	if !f.Comment("config", "IpAddress") {
		t.Fatal("nothing commented")
	}
	if f.Has("config", "IpAddress") || !f.Has("keyboard", "IpAddress") {
		t.Fatal(f.String())
	}
	if f.Comment("config", "IpAddress") {
		t.Fatal("second call changed something")
	}
}

func TestSetSvdInt(t *testing.T) {
	payload := []byte(`{"storage": {".TestModeParam.game.networkId": {"type": 1, "value": 0}}}`)
	hdr := make([]byte, svdHeaderLen)
	copy(hdr, "svd1")
	path := filepath.Join(t.TempDir(), "testmode_pcb.json")
	if err := os.WriteFile(path, encodeSvd(hdr, payload), 0644); err != nil {
		t.Fatal(err)
	}
	old, err := setSvdInt(path, networkIDKey, 2)
	if err != nil || old != 0 {
		t.Fatal(old, err)
	}
	_, p, err := readSvd(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _, ok := findValue(p, networkIDKey); !ok || v != 2 {
		t.Fatal(string(p))
	}
	if n, ok := storeCabinetID("10.77.0.3"); !ok || n != 3 {
		t.Fatal(n)
	}
}
