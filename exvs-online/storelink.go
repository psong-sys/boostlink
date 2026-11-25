package main

// Store Link: a tiny VPN for the game's "Store Team" (Route Battle co-op).
//
// In an arcade, cabinets in one store find each other on the shop LAN: the Live
// Monitor broadcasts "store hub at <ip>:48000" to UDP 255.255.255.255:49000-49015,
// each cabinet broadcasts its own "<ip>:4800N" once at boot, and after that they
// talk unicast. The private IPs are written inside the messages, so forwarding
// ports is not enough: the PCs have to share one network.
//
// Each player's exvs-online (run with -storelink, as admin) makes a virtual network
// card called "Boost Link" (Wintun) and sends every IPv4 packet for 10.77.0.0/24,
// plus broadcasts, to the match server over UDP. The match server relays them
// between players who typed the same team code, and to nobody else. Every team gets
// its own copy of 10.77.0.0/24: the first player is 10.77.0.1, the next .2, ...
// Only one player in a team runs the Live Monitor (the "store").
//
// Wire format (UDP, little endian, every packet):
//
//	"BL" version(1) type(1) body
//	type 1 HELLO   client -> server: id[16] codeLen(1) code
//	type 2 WELCOME server -> client: ip[4] members(1)
//	type 3 DATA    both ways:        an IPv4 packet
//	type 4 BYE     client -> server
//	type 5 FULL    server -> client: the team has no free address
//
// Clients send HELLO every few seconds; it is also the keepalive.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	slVersion   = 1
	slHello     = 1
	slWelcome   = 2
	slData      = 3
	slBye       = 4
	slFull      = 5
	slMaxTeam   = 8
	slTimeout   = 20 * time.Second
	slKeepalive = 3 * time.Second
	slMTU       = 1400
	// slMaxRate is the most packets per second one player may send through the relay.
	slMaxRate = 3000
)

var slNet = netip.MustParsePrefix("10.77.0.0/24")

func slHeader(t byte) []byte { return []byte{'B', 'L', slVersion, t} }

func slParse(b []byte) (byte, []byte, bool) {
	if len(b) < 4 || b[0] != 'B' || b[1] != 'L' || b[2] != slVersion {
		return 0, nil, false
	}
	return b[3], b[4:], true
}

// slTeamKey turns a team code into the relay's team key. Codes ignore case and
// spaces at the ends, like the launcher's TEAM CODE box.
func slTeamKey(code string) string {
	s := sha256.Sum256([]byte("boost-link-store/" + strings.ToLower(strings.TrimSpace(code))))
	return string(s[:16])
}

// slIPv4Dst returns the destination of an IPv4 packet.
func slIPv4Dst(p []byte) (netip.Addr, bool) {
	if len(p) < 20 || p[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(p[16:20])), true
}

func slIPv4Src(p []byte) (netip.Addr, bool) {
	if len(p) < 20 || p[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(p[12:16])), true
}

// slIsBroadcast: limited broadcast, the team subnet's broadcast, or multicast.
func slIsBroadcast(a netip.Addr) bool {
	if a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) || a == netip.AddrFrom4([4]byte{10, 77, 0, 255}) {
		return true
	}
	return a.IsMulticast()
}

// slWanted says whether the client should send this packet through the relay.
func slWanted(p []byte) bool {
	d, ok := slIPv4Dst(p)
	if !ok {
		return false
	}
	return slIsBroadcast(d) || slNet.Contains(d)
}

// ---- relay (runs on the match server) ----

type slMember struct {
	id     [16]byte
	addr   *net.UDPAddr
	ip     netip.Addr
	seen   time.Time
	team   *slTeam
	window time.Time
	count  int
}

type slTeam struct {
	key     string
	members map[netip.Addr]*slMember
	// last broadcast each member sent to each port, replayed to players who join
	// later (the Live Monitor only announces itself for a few seconds after it starts).
	bcast map[slBcastKey]slBcast
}

type slBcastKey struct {
	src  netip.Addr
	port uint16
}

type slBcast struct {
	pkt []byte
	at  time.Time
}

type StoreRelay struct {
	mu     sync.Mutex
	conn   *net.UDPConn
	teams  map[string]*slTeam
	byAddr map[string]*slMember
	// counters for the status page
	packets uint64
}

func NewStoreRelay() *StoreRelay {
	return &StoreRelay{teams: map[string]*slTeam{}, byAddr: map[string]*slMember{}}
}

// Listen runs the relay on a UDP address like ":28090".
func (r *StoreRelay) Listen(addr string) error {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	c, err := net.ListenUDP("udp", ua)
	if err != nil {
		return err
	}
	r.conn = c
	go r.sweep()
	go r.serve()
	return nil
}

func (r *StoreRelay) serve() {
	buf := make([]byte, 65535)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		r.handle(buf[:n], from)
	}
}

func (r *StoreRelay) send(to *net.UDPAddr, b []byte) {
	_, _ = r.conn.WriteToUDP(b, to)
}

func (r *StoreRelay) handle(b []byte, from *net.UDPAddr) {
	t, body, ok := slParse(b)
	if !ok {
		return
	}
	switch t {
	case slHello:
		if len(body) < 17 {
			return
		}
		var id [16]byte
		copy(id[:], body[:16])
		n := int(body[16])
		if n == 0 || len(body) < 17+n {
			return
		}
		r.hello(id, string(body[17:17+n]), from)
	case slData:
		r.data(body, from)
	case slBye:
		r.mu.Lock()
		if m := r.byAddr[from.String()]; m != nil {
			r.dropLocked(m)
		}
		r.mu.Unlock()
	}
}

func (r *StoreRelay) hello(id [16]byte, code string, from *net.UDPAddr) {
	key := slTeamKey(code)
	r.mu.Lock()
	defer r.mu.Unlock()
	team := r.teams[key]
	if team == nil {
		team = &slTeam{key: key, members: map[netip.Addr]*slMember{}, bcast: map[slBcastKey]slBcast{}}
		r.teams[key] = team
	}
	var me *slMember
	for _, m := range team.members {
		if m.id == id {
			me = m
			break
		}
	}
	fresh := false
	if me == nil {
		if len(team.members) >= slMaxTeam {
			r.send(from, slHeader(slFull))
			return
		}
		ip := netip.Addr{}
		for i := 1; i < 255; i++ {
			a := netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})
			if team.members[a] == nil {
				ip = a
				break
			}
		}
		me = &slMember{id: id, ip: ip, team: team}
		team.members[ip] = me
		fresh = true
		log.Printf("store link: %s joined a team as %s (%d in team)", from.IP, ip, len(team.members))
	}
	if me.addr == nil || me.addr.String() != from.String() {
		if me.addr != nil {
			delete(r.byAddr, me.addr.String())
		}
		// another member of another team at this address? drop it.
		if old := r.byAddr[from.String()]; old != nil && old != me {
			r.dropLocked(old)
		}
		me.addr = from
		r.byAddr[from.String()] = me
	}
	me.seen = time.Now()
	ip4 := me.ip.As4()
	r.send(from, append(slHeader(slWelcome), ip4[0], ip4[1], ip4[2], ip4[3], byte(len(team.members))))
	if fresh {
		for k, bc := range team.bcast {
			if k.src != me.ip && time.Since(bc.at) < 30*time.Minute {
				r.send(from, append(slHeader(slData), bc.pkt...))
			}
		}
	}
}

func (r *StoreRelay) data(p []byte, from *net.UDPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.byAddr[from.String()]
	if m == nil {
		return
	}
	now := time.Now()
	if now.Sub(m.window) >= time.Second {
		m.window, m.count = now, 0
	}
	m.count++
	if m.count > slMaxRate {
		return
	}
	src, ok := slIPv4Src(p)
	if !ok || src != m.ip {
		return // only packets from the address we gave this player
	}
	dst, _ := slIPv4Dst(p)
	m.seen = now
	r.packets++
	out := append(slHeader(slData), p...)
	if slIsBroadcast(dst) {
		if port, ok := slUDPDstPort(p); ok {
			m.team.bcast[slBcastKey{m.ip, port}] = slBcast{pkt: append([]byte(nil), p...), at: now}
		}
		for _, o := range m.team.members {
			if o != m && o.addr != nil {
				r.send(o.addr, out)
			}
		}
		return
	}
	if o := m.team.members[dst]; o != nil && o != m && o.addr != nil {
		r.send(o.addr, out)
	}
}

func slUDPDstPort(p []byte) (uint16, bool) {
	if len(p) < 20 || p[9] != 17 {
		return 0, false
	}
	ihl := int(p[0]&0x0f) * 4
	if len(p) < ihl+4 {
		return 0, false
	}
	return binary.BigEndian.Uint16(p[ihl+2:]), true
}

func (r *StoreRelay) dropLocked(m *slMember) {
	if m.addr != nil {
		delete(r.byAddr, m.addr.String())
	}
	t := m.team
	delete(t.members, m.ip)
	for k := range t.bcast {
		if k.src == m.ip {
			delete(t.bcast, k)
		}
	}
	if len(t.members) == 0 {
		delete(r.teams, t.key)
	}
	log.Printf("store link: %s left (%d in team)", m.ip, len(t.members))
}

func (r *StoreRelay) sweep() {
	for range time.Tick(5 * time.Second) {
		r.mu.Lock()
		for _, t := range r.teams {
			for _, m := range t.members {
				if time.Since(m.seen) > slTimeout {
					r.dropLocked(m)
				}
			}
		}
		r.mu.Unlock()
	}
}

// Stats: teams with at least two players, and players connected.
func (r *StoreRelay) Stats() (teams, players int) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.teams {
		players += len(t.members)
		if len(t.members) >= 2 {
			teams++
		}
	}
	return
}
