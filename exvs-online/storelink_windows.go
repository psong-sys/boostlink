//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// wintun.dll 0.14.1 (amd64), signed by WireGuard LLC, from https://www.wintun.net.
// It is written next to exvs-online.exe the first time Store Team mode runs.
//
//go:embed wintun/wintun.dll
var wintunDLL []byte

const slAdapterName = "Boost Link"

var slTunnelType = "Boost Link"

func ensureWintunDLL() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, wintunDLL) {
		return nil
	}
	return os.WriteFile(p, wintunDLL, 0644)
}

type wintunDevice struct {
	adapter *wintun.Adapter
	session wintun.Session
	closed  chan struct{}
	once    sync.Once
	wmu     sync.Mutex
	rmu     sync.Mutex
}

func openWintun() (*wintunDevice, error) {
	if err := ensureWintunDLL(); err != nil {
		return nil, fmt.Errorf("writing wintun.dll: %w", err)
	}
	// A leftover adapter from a crash: reuse its name by closing it first.
	if old, err := wintun.OpenAdapter(slAdapterName); err == nil {
		_ = old.Close()
	}
	guid := windows.GUID{Data1: 0xb0057111, Data2: 0x4e6b, Data3: 0x5701, Data4: [8]byte{0xb1, 0x5e, 0x57, 0x0f, 0xe7, 0x3a, 0x11, 0x01}}
	a, err := wintun.CreateAdapter(slAdapterName, slTunnelType, &guid)
	if err != nil {
		return nil, fmt.Errorf("creating the Boost Link network adapter (needs admin): %w", err)
	}
	s, err := a.StartSession(0x400000)
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	return &wintunDevice{adapter: a, session: s, closed: make(chan struct{})}, nil
}

func (d *wintunDevice) Read() ([]byte, error) {
	for {
		select {
		case <-d.closed:
			return nil, net.ErrClosed
		default:
		}
		d.rmu.Lock()
		select {
		case <-d.closed:
			d.rmu.Unlock()
			return nil, net.ErrClosed
		default:
		}
		p, err := d.session.ReceivePacket()
		if err == nil {
			out := append([]byte(nil), p...)
			d.session.ReleaseReceivePacket(p)
			d.rmu.Unlock()
			return out, nil
		}
		d.rmu.Unlock()
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			_, _ = windows.WaitForSingleObject(d.session.ReadWaitEvent(), 250)
			continue
		}
		if errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return nil, net.ErrClosed
		}
		return nil, err
	}
}

func (d *wintunDevice) Write(p []byte) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	select {
	case <-d.closed:
		return net.ErrClosed
	default:
	}
	b, err := d.session.AllocateSendPacket(len(p))
	if err != nil {
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil // ring full: drop, like a busy network would
		}
		return err
	}
	copy(b, p)
	d.session.SendPacket(b)
	return nil
}

func (d *wintunDevice) Close() error {
	d.once.Do(func() {
		close(d.closed)
		d.rmu.Lock()
		d.wmu.Lock()
		d.session.End()
		d.wmu.Unlock()
		d.rmu.Unlock()
		_ = d.adapter.Close()
	})
	return nil
}

func slRun(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// slSetIP gives the adapter its team address, puts it first for broadcasts (the Live
// Monitor sends its "store hub here" message to 255.255.255.255 from 0.0.0.0, which
// Windows sends out of the adapter with the lowest metric), and lets the game's
// traffic from teammates through Windows Firewall.
func slSetIP(ip netip.Addr) error {
	var err error
	for i := 0; i < 20; i++ { // the adapter can take a moment to show up in netsh
		err = slRun("netsh", "interface", "ipv4", "set", "address", "name="+slAdapterName, "static", ip.String(), "255.255.255.0")
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	_ = slRun("netsh", "interface", "ipv4", "set", "interface", slAdapterName, "metric=1")
	gameDir := findGameDir(func() string { e, _ := os.Executable(); return filepath.Dir(e) }())
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='SilentlyContinue'\n")
	b.WriteString("Get-NetFirewallRule -DisplayName 'Boost Link - Store Team' | Remove-NetFirewallRule\n")
	b.WriteString("New-NetFirewallRule -DisplayName 'Boost Link - Store Team' -Direction Inbound -Action Allow -RemoteAddress 10.77.0.0/24 -Profile Any | Out-Null\n")
	b.WriteString("Set-NetConnectionProfile -InterfaceAlias 'Boost Link' -NetworkCategory Private\n")
	if gameDir != "" {
		for _, p := range []string{filepath.Join(gameDir, "vsac29_Release.exe"), filepath.Join(gameDir, "vsac29_Release_LM.exe")} {
			fmt.Fprintf(&b, "foreach($f in Get-NetFirewallApplicationFilter){ if([Environment]::ExpandEnvironmentVariables([string]$f.Program) -ieq %s){ $f | Get-NetFirewallRule | Where-Object { \"$($_.Action)\" -eq 'Block' } | Disable-NetFirewallRule } }\n", psQuote(p))
		}
	}
	_ = slRun("powershell", "-NoProfile", "-NonInteractive", "-Command", b.String())
	return nil
}

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// runStoreLink is "exvs-online -storelink". It needs admin (the launcher starts it
// with an admin prompt). It stops on POST /stop, or when the -parent process exits.
func runStoreLink(server, code, statusAddr string, parent int) error {
	st := &slStatus{State: "starting", Adapter: slAdapterName}
	stop := make(chan struct{})
	var once sync.Once
	doStop := func() { once.Do(func() { close(stop) }) }
	srv, err := slServeStatus(statusAddr, st, doStop)
	if err != nil {
		return fmt.Errorf("status port %s: %w (is Store Team already running?)", statusAddr, err)
	}
	defer srv.Close()
	fail := func(err error) error {
		st.set(func(s *slStatus) { s.State = "error"; s.Error = err.Error() })
		time.Sleep(3 * time.Second) // let the launcher read the error
		return err
	}
	if !isAdmin() {
		return fail(errors.New("Store Team needs admin rights (to add the Boost Link network adapter)"))
	}
	if parent > 0 {
		go func() {
			h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parent))
			if err != nil {
				doStop()
				return
			}
			defer windows.CloseHandle(h)
			_, _ = windows.WaitForSingleObject(h, windows.INFINITE)
			doStop()
		}()
	}
	dev, err := openWintun()
	if err != nil {
		return fail(err)
	}
	defer dev.Close()
	c := &StoreClient{Server: server, Code: code, Status: st, SetIP: slSetIP}
	err = c.Run(dev, stop)
	if err != nil {
		return fail(err)
	}
	st.set(func(s *slStatus) { s.State = "stopped" })
	return nil
}
