package main

// A small STUN server (RFC 3489 classic and RFC 5389/5780) so the game's CheckStun step
// has something that answers every test, and so we can see in traffic.log what it asks.
// With one IP address it can't truly "change IP"; requests for that are answered from the
// alternate port instead, which makes the NAT look as friendly as possible.

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
)

type LocalStunConfig struct {
	Listen  string `json:"listen"`  // e.g. "0.0.0.0:3478"
	AltPort int    `json:"altPort"` // second port for CHANGE-REQUEST answers, e.g. 3479
}

const stunMagic = 0x2112A442

func startStun(cfg *LocalStunConfig, tl *trafficLog) error {
	if cfg == nil || cfg.Listen == "" {
		return nil
	}
	main, err := net.ListenPacket("udp4", cfg.Listen)
	if err != nil {
		return fmt.Errorf("stun listen %s: %w", cfg.Listen, err)
	}
	host, _, _ := net.SplitHostPort(cfg.Listen)
	alt := main
	if cfg.AltPort > 0 {
		if a, err := net.ListenPacket("udp4", net.JoinHostPort(host, fmt.Sprint(cfg.AltPort))); err == nil {
			alt = a
			go stunServe(a, main, a, tl, "alt")
		} else {
			log.Printf("stun alt port %d unavailable: %v", cfg.AltPort, err)
		}
	}
	go stunServe(main, main, alt, tl, "main")
	log.Printf("STUN server listening on %s (alt port %d)", cfg.Listen, cfg.AltPort)
	return nil
}

func stunServe(in, mainConn, altConn net.PacketConn, tl *trafficLog, name string) {
	buf := make([]byte, 2048)
	for {
		n, from, err := in.ReadFrom(buf)
		if err != nil {
			log.Printf("stun %s: %v", name, err)
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		resp, change, desc := stunHandle(pkt, from.(*net.UDPAddr), in, altConn)
		tl.Line("STUN>", fmt.Sprintf("[%s] from %s %s", name, from, desc))
		if resp == nil {
			continue
		}
		out := in
		if change != 0 && altConn != in {
			out = altConn
		} else if change != 0 && altConn == in {
			out = mainConn
		}
		if _, err := out.WriteTo(resp, from); err != nil {
			tl.Line("STUN<", fmt.Sprintf("send error: %v", err))
		} else {
			tl.Line("STUN<", fmt.Sprintf("binding response to %s via %s", from, out.LocalAddr()))
		}
	}
}

func stunHandle(p []byte, from *net.UDPAddr, in, alt net.PacketConn) ([]byte, byte, string) {
	if len(p) < 20 {
		return nil, 0, fmt.Sprintf("short packet %d bytes: %x", len(p), p)
	}
	mtype := binary.BigEndian.Uint16(p[0:2])
	mlen := int(binary.BigEndian.Uint16(p[2:4]))
	cookie := binary.BigEndian.Uint32(p[4:8])
	modern := cookie == stunMagic
	if 20+mlen > len(p) {
		mlen = len(p) - 20
	}
	var change byte
	var attrs []string
	for off := 20; off+4 <= 20+mlen; {
		at := binary.BigEndian.Uint16(p[off:])
		al := int(binary.BigEndian.Uint16(p[off+2:]))
		if off+4+al > len(p) {
			break
		}
		v := p[off+4 : off+4+al]
		attrs = append(attrs, fmt.Sprintf("0x%04x(%d)", at, al))
		if at == 0x0003 && al >= 4 {
			change = v[3] & 0x06
		}
		off += 4 + ((al + 3) &^ 3)
	}
	desc := fmt.Sprintf("type=0x%04x modern=%v change=%d attrs=[%s]", mtype, modern, change, strings.Join(attrs, " "))
	if mtype != 0x0001 { // only Binding Request
		return nil, 0, desc + " (ignored)"
	}

	localMain := in.LocalAddr().(*net.UDPAddr)
	altAddr := alt.LocalAddr().(*net.UDPAddr)
	ip4 := from.IP.To4()

	var b []byte
	addAttr := func(t uint16, v []byte) {
		h := make([]byte, 4)
		binary.BigEndian.PutUint16(h, t)
		binary.BigEndian.PutUint16(h[2:], uint16(len(v)))
		b = append(b, h...)
		b = append(b, v...)
		for len(v)%4 != 0 {
			b = append(b, 0)
			v = append(v, 0)
		}
	}
	addr := func(ip net.IP, port int) []byte {
		v := []byte{0, 1, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint16(v[2:], uint16(port))
		copy(v[4:], ip.To4())
		return v
	}
	// The address the server "is" for SOURCE/OTHER: use the interface the packet came in on.
	srvIP := localMain.IP
	if srvIP == nil || srvIP.IsUnspecified() {
		srvIP = localIPFor(from.IP)
	}
	addAttr(0x0001, addr(ip4, from.Port)) // MAPPED-ADDRESS
	if modern {
		x := addr(ip4, from.Port)
		binary.BigEndian.PutUint16(x[2:], uint16(from.Port)^uint16(stunMagic>>16))
		mc := make([]byte, 4)
		binary.BigEndian.PutUint32(mc, stunMagic)
		for i := 0; i < 4; i++ {
			x[4+i] ^= mc[i]
		}
		addAttr(0x0020, x)                           // XOR-MAPPED-ADDRESS
		addAttr(0x802b, addr(srvIP, localMain.Port)) // RESPONSE-ORIGIN
		addAttr(0x802c, addr(srvIP, altAddr.Port))   // OTHER-ADDRESS
	} else {
		addAttr(0x0004, addr(srvIP, localMain.Port)) // SOURCE-ADDRESS
		addAttr(0x0005, addr(srvIP, altAddr.Port))   // CHANGED-ADDRESS
	}
	h := make([]byte, 20)
	binary.BigEndian.PutUint16(h, 0x0101)
	binary.BigEndian.PutUint16(h[2:], uint16(len(b)))
	copy(h[4:20], p[4:20]) // cookie + transaction id (or 16-byte classic id)
	return append(h, b...), change, desc
}

// localIPFor picks the local address that would be used to reach ip.
func localIPFor(ip net.IP) net.IP {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: 9})
	if err != nil {
		return net.IPv4(127, 0, 0, 1)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP
}

// firstLANIP returns the first non-loopback IPv4 address, for "auto" STUN hosts.
func firstLANIP() string {
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return n.IP.String()
			}
		}
	}
	return "127.0.0.1"
}
