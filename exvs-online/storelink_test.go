package main

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

type fakeDev struct {
	in  chan []byte // packets "from the PC"
	out chan []byte // packets delivered to the PC
}

func newFakeDev() *fakeDev { return &fakeDev{in: make(chan []byte, 64), out: make(chan []byte, 64)} }

func (d *fakeDev) Read() ([]byte, error) {
	p, ok := <-d.in
	if !ok {
		return nil, net.ErrClosed
	}
	return p, nil
}
func (d *fakeDev) Write(p []byte) error { d.out <- append([]byte(nil), p...); return nil }
func (d *fakeDev) Close() error         { return nil }

// udpPacket builds a minimal IPv4/UDP packet.
func udpPacket(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	p := make([]byte, 28+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[8] = 64
	p[9] = 17
	s, d := src.As4(), dst.As4()
	copy(p[12:], s[:])
	copy(p[16:], d[:])
	binary.BigEndian.PutUint16(p[20:], sport)
	binary.BigEndian.PutUint16(p[22:], dport)
	binary.BigEndian.PutUint16(p[24:], uint16(8+len(payload)))
	copy(p[28:], payload)
	return p
}

type testPlayer struct {
	dev  *fakeDev
	st   *slStatus
	ip   chan netip.Addr
	stop chan struct{}
}

func startPlayer(t *testing.T, server, code string) *testPlayer {
	t.Helper()
	p := &testPlayer{dev: newFakeDev(), st: &slStatus{}, ip: make(chan netip.Addr, 4), stop: make(chan struct{})}
	c := &StoreClient{Server: server, Code: code, Status: p.st, SetIP: func(ip netip.Addr) error { p.ip <- ip; return nil }}
	go func() { _ = c.Run(p.dev, p.stop) }()
	t.Cleanup(func() { close(p.stop) })
	return p
}

func (p *testPlayer) addr(t *testing.T) netip.Addr {
	t.Helper()
	select {
	case ip := <-p.ip:
		return ip
	case <-time.After(3 * time.Second):
		t.Fatal("no address from the relay")
	}
	return netip.Addr{}
}

func expectPacket(t *testing.T, d *fakeDev, dport uint16) []byte {
	t.Helper()
	select {
	case p := <-d.out:
		if got, _ := slUDPDstPort(p); got != dport {
			t.Fatalf("got a packet for port %d, want %d", got, dport)
		}
		return p
	case <-time.After(3 * time.Second):
		t.Fatalf("no packet for port %d", dport)
	}
	return nil
}

func expectNothing(t *testing.T, d *fakeDev) {
	t.Helper()
	select {
	case p := <-d.out:
		port, _ := slUDPDstPort(p)
		t.Fatalf("unexpected packet for port %d", port)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStoreLinkRelay(t *testing.T) {
	r := NewStoreRelay()
	if err := r.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	server := r.conn.LocalAddr().String()
	bcast := netip.MustParseAddr("255.255.255.255")

	host := startPlayer(t, server, "Paul123")
	hostIP := host.addr(t)
	if hostIP != netip.MustParseAddr("10.77.0.1") {
		t.Fatalf("host got %s", hostIP)
	}
	// The host's Live Monitor announces itself before anyone else has joined.
	host.dev.in <- udpPacket(hostIP, bcast, 49000, 49001, []byte("store hub"))
	time.Sleep(100 * time.Millisecond)

	// Same code with different case and spaces: same team. The cached announcement is replayed.
	guest := startPlayer(t, server, "  paul123 ")
	guestIP := guest.addr(t)
	if guestIP != netip.MustParseAddr("10.77.0.2") {
		t.Fatalf("guest got %s", guestIP)
	}
	expectPacket(t, guest.dev, 49001)

	// Someone with another code is on their own network and hears nothing.
	other := startPlayer(t, server, "someone-else")
	if ip := other.addr(t); ip != netip.MustParseAddr("10.77.0.1") {
		t.Fatalf("other team got %s", ip)
	}

	// The guest's game announces itself: the host hears it.
	guest.dev.in <- udpPacket(guestIP, bcast, 49000, 49000, []byte("cabinet"))
	expectPacket(t, host.dev, 49000)
	expectNothing(t, other.dev)

	// Unicast both ways.
	host.dev.in <- udpPacket(hostIP, guestIP, 48000, 48002, []byte("hi"))
	expectPacket(t, guest.dev, 48002)
	guest.dev.in <- udpPacket(guestIP, hostIP, 48002, 48000, []byte("hi back"))
	expectPacket(t, host.dev, 48000)

	// Spoofed source and traffic outside the team network are dropped.
	guest.dev.in <- udpPacket(netip.MustParseAddr("10.77.0.9"), hostIP, 1, 48000, nil)
	guest.dev.in <- udpPacket(guestIP, netip.MustParseAddr("8.8.8.8"), 1, 53, nil)
	expectNothing(t, host.dev)
	expectNothing(t, other.dev)

	if teams, players := r.Stats(); teams != 1 || players != 3 {
		t.Fatalf("stats: %d teams, %d players", teams, players)
	}
}

func TestStoreLinkAddr(t *testing.T) {
	if got := storeLinkAddr(MatchConfig{Listen: "0.0.0.0:28090"}); got != ":28090" {
		t.Fatal(got)
	}
	if got := storeLinkAddr(MatchConfig{Listen: ":1", StoreLink: "off"}); got != "" {
		t.Fatal(got)
	}
	if got := slServerAddr("149.28.115.108"); got != "149.28.115.108:28090" {
		t.Fatal(got)
	}
	if got := slServerAddr("http://1.2.3.4:5/x"); got != "1.2.3.4:5" {
		t.Fatal(got)
	}
}
