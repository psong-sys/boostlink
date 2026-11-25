package main

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeSvd(t *testing.T, path, payload string) {
	hdr := make([]byte, 36)
	copy(hdr, "svd1")
	binary.LittleEndian.PutUint32(hdr[4:], 1)
	binary.LittleEndian.PutUint64(hdr[8:], uint64(len(payload)))
	h := sha1.New()
	h.Write(hdr[:16])
	h.Write([]byte(payload))
	copy(hdr[16:], h.Sum(nil))
	if err := os.WriteFile(path, append(hdr, payload...), 0644); err != nil {
		t.Fatal(err)
	}
}

func fakeGame(t *testing.T, iface string, mode int) string {
	d := t.TempDir()
	// the Visual C++ DLLs stand in for the system folder (the checker's systemDir is this folder)
	for _, f := range []string{"vsac29_Release.exe", "vsac29_Release_LM.exe", "bngrw.dll", "msvcr100.dll", "vcruntime140.dll"} {
		os.WriteFile(filepath.Join(d, f), []byte("x"), 0644)
	}
	os.WriteFile(filepath.Join(d, "config.ini"), []byte("[config]\r\nserial =31Yf\r\nServer =127.0.0.1:28090\r\nInterfaceName="+iface+"\r\n"), 0644)
	os.WriteFile(filepath.Join(d, "lmnfig.ini"), []byte("[config]\r\nInterfaceName="+iface+"\r\n"), 0644)
	os.Mkdir(filepath.Join(d, "29"), 0755)
	p := `{"storage":{".TestModeParam.gameServerPcb.useSettingFile":{"value":0,"type":1}`
	for g := 0; g < 3; g++ {
		p += `,".TestModeParam.gameServerPcb.settingFile.[0].group.[` + string(rune('0'+g)) + `].battleMode":{"value":` + string(rune('0'+mode)) + `,"type":1}`
	}
	p += `},"format_version":1}`
	writeSvd(t, filepath.Join(d, "29", "testmode_pcb.json"), p)
	writeSvd(t, filepath.Join(d, "29", "testmode_lm.json"), p)
	return d
}

func fakeChecker(gameDir string, cfg Config) *checker {
	c := newChecker(gameDir, cfg, true)
	c.ifaces = func() ([]net.Interface, error) {
		return []net.Interface{{Index: 1, Name: "Ethernet", Flags: net.FlagUp}, {Index: 2, Name: "ZeroTier One [abc]", Flags: net.FlagUp}}, nil
	}
	c.addrs = func(i net.Interface) ([]net.Addr, error) {
		ip := map[string]string{"Ethernet": "192.168.1.10", "ZeroTier One [abc]": "10.147.17.5"}[i.Name]
		return []net.Addr{&net.IPNet{IP: net.ParseIP(ip).To4(), Mask: net.CIDRMask(24, 32)}}, nil
	}
	c.systemDir = gameDir
	c.firewall = func(p []string) (map[string]string, error) {
		return map[string]string{p[0]: "allow", p[1]: "allow", p[2]: "allow"}, nil
	}
	c.stunMapped = func(local, server string) (string, error) { return "1.2.3.4:5000", nil }
	c.httpGet = func(string) (time.Duration, error) { return 40 * time.Millisecond, nil }
	c.procs = func() []string { return nil }
	c.bugchecks = func() []string { return nil }
	return c
}

func status(r *CheckReport, id string) CheckItem {
	for _, it := range r.Items {
		if it.ID == id {
			return it
		}
	}
	return CheckItem{}
}

func TestCheckHappyFriend(t *testing.T) {
	d := fakeGame(t, "Ethernet", 0)
	cfg := defaultConfig()
	cfg.Mode = "proxy"
	cfg.Proxy.MatchServer = "http://10.147.17.1:28090"
	r := fakeChecker(d, cfg).Run()
	for _, id := range []string{"game", "adapter", "online", "firewall", "nat", "host", "vpn"} {
		if s := status(r, id).Status; s != "ok" {
			t.Errorf("%s = %s (%s)", id, s, status(r, id).Detail)
		}
	}
	if r.failed() || r.CanFixFirewall {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestCheckFindsProblems(t *testing.T) {
	d := fakeGame(t, "Radmin VPN", 1)
	cfg := defaultConfig()
	cfg.Mode = "proxy"
	cfg.Proxy.MatchServer = "http://10.147.17.1:28090"
	c := fakeChecker(d, cfg)
	c.firewall = func(p []string) (map[string]string, error) {
		return map[string]string{p[0]: "block", p[1]: "allow"}, nil
	}
	n := 0
	c.stunMapped = func(local, server string) (string, error) { n++; return "1.2.3.4:" + string(rune('0'+n)) + "000", nil }
	c.httpGet = func(string) (time.Duration, error) { return 0, errors.New("connection refused") }
	r := c.Run()
	want := map[string]string{"adapter": "fail", "online": "warn", "firewall": "fail", "nat": "warn", "host": "fail"}
	for id, s := range want {
		if got := status(r, id).Status; got != s {
			t.Errorf("%s = %s, want %s (%s)", id, got, s, status(r, id).Detail)
		}
	}
	if !r.CanFixFirewall || !r.failed() {
		t.Fatal("expected fixable firewall and a failure")
	}
	if txt := printReport(r); len(txt) < 100 {
		t.Fatal(txt)
	}
}

func TestBattleModesRealFormat(t *testing.T) {
	d := fakeGame(t, "Ethernet", 1)
	v, err := battleModes(filepath.Join(d, "29", "testmode_pcb.json"))
	if err != nil || len(v) != 3 || v[0] != 1 {
		t.Fatal(v, err)
	}
}

func TestPackSerialAndSnowLauncher(t *testing.T) {
	c := fakeChecker(fakeGame(t, "Ethernet", 0), defaultConfig())
	c.procs = func() []string { return []string{"explorer.exe", "SnowLauncher_English.exe"} }
	r := c.Run()
	if status(r, "serial").Status != "warn" {
		t.Errorf("pack serial: %+v", status(r, "serial"))
	}
	if status(r, "snowlauncher").Status != "warn" {
		t.Errorf("snowlauncher: %+v", status(r, "snowlauncher"))
	}
	c.procs = func() []string { return []string{"explorer.exe"} }
	if status(c.Run(), "snowlauncher").Status != "" {
		t.Error("warned about SnowLauncher that isn't running")
	}
}

func TestCheckGraphics(t *testing.T) {
	d := t.TempDir()
	c := fakeChecker(d, Config{})
	run := func() CheckItem { r := &CheckReport{}; c.checkGraphics(r); return status(r, "graphics") }

	if it := run(); it.Status != "info" || !strings.Contains(it.Detail, "DirectX 12") {
		t.Fatalf("off: %+v", it)
	}
	c.bugchecks = func() []string { return []string{"2026-11-20"} }
	if it := run(); it.Status != "warn" || !strings.Contains(it.Fix, "Vulkan mode") {
		t.Fatalf("off with crashes: %+v", it)
	}
	c.bugchecks = nil
	os.WriteFile(filepath.Join(d, "dxgi.dll"), []byte("x"), 0644)
	if it := run(); it.Status != "warn" {
		t.Fatalf("partial: %+v", it)
	}
	os.WriteFile(filepath.Join(d, "d3d12.dll"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(d, "d3d12core.dll"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(d, "vulkan-1.dll"), []byte("x"), 0644) // systemDir is the game dir in tests
	if it := run(); it.Status != "info" || !strings.Contains(it.Detail, "didn't install") {
		t.Fatalf("other files: %+v", it)
	}
	os.MkdirAll(filepath.Join(d, "exvs-launcher"), 0755)
	os.WriteFile(filepath.Join(d, "exvs-launcher", "vulkan.json"), []byte(`{"on":true,"versions":{"vkd3d-proton":"3.1","DXVK":"3.1.1"}}`), 0644)
	if it := run(); it.Status != "ok" || !strings.Contains(it.Detail, "3.1.1") {
		t.Fatalf("on: %+v", it)
	}
	if runtime.GOOS == "windows" {
		os.Remove(filepath.Join(d, "vulkan-1.dll"))
		if it := run(); it.Status != "fail" {
			t.Fatalf("no driver: %+v", it)
		}
	}
}
