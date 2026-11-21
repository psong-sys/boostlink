package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = "[config]\r\n# Game mode, Client or LM.\r\nmode =Client\r\nserial =31Yf\r\n\r\n# Card server address\r\nServer =127.0.0.1:28080\r\nWindowed =True\r\nInterfaceName=Radmin VPN\r\n\r\n[keyboard]\r\nEnabled =True\r\nA =Z,A,S,F \r\nTest=F7\r\n\r\n[controller]\r\nEnabled =True\r\nTest=9\r\n\r\n[display]\r\nResolution=1080p\r\nFramerateLimit=True\r\n"

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
	if !d.StockCard || s.Card.ChipID != "exvs2ib123" || s.Game.Serial != "31Yf" {
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
