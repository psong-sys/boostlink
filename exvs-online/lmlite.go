package main

// Light Live Monitor: discovery and session engine. What to answer lives in
// lmlite_reply.go; this file only keeps each cab's link up and its messages reliable.

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
)

const (
	lmDiscoveryPort = 49000
	lmDiscoveryN    = 16
	lmSessionPort   = 48000
	lmAnnounceLen   = 276
	lmLinkTick      = time.Second / 60
	lmResendAfter   = 100 * time.Millisecond
	lmCabTimeout    = 30 * time.Second
)

var lmTag = []byte{0x33, 0xa0, 0x01, 0x00}

// lmAnnounce builds the 276-byte discovery datagram. role 1 = cab, 2 = LM.
func lmAnnounce(ip [4]byte, role, index uint16, group uint32, sessionPort uint16, reply bool) []byte {
	b := make([]byte, lmAnnounceLen)
	copy(b[0:], lmTag)
	copy(b[4:], ip[:])
	binary.LittleEndian.PutUint16(b[8:], lmDiscoveryPort)
	if reply {
		put32(b, 12, 1)
	}
	put32(b, 16, lmAnnounceLen)
	put32(b, 20, 1)
	binary.LittleEndian.PutUint16(b[24:], 13)
	binary.LittleEndian.PutUint16(b[26:], 30)
	copy(b[28:], lmTag)
	put32(b, 48, group)
	binary.LittleEndian.PutUint16(b[56:], role)
	binary.LittleEndian.PutUint16(b[58:], index)
	copy(b[64:], ip[:])
	binary.LittleEndian.PutUint16(b[80:], sessionPort)
	return b
}

type lmAnnounceInfo struct {
	IP          [4]byte
	Role, Index uint16
	Group       uint32
	SessionPort uint16
}

func parseLMAnnounce(b []byte) (lmAnnounceInfo, bool) {
	var a lmAnnounceInfo
	if len(b) != lmAnnounceLen || string(b[:4]) != string(lmTag) {
		return a, false
	}
	copy(a.IP[:], b[4:8])
	a.Role = binary.LittleEndian.Uint16(b[56:])
	a.Index = binary.LittleEndian.Uint16(b[58:])
	a.Group = le32(b, 48)
	a.SessionPort = binary.LittleEndian.Uint16(b[80:])
	return a, true
}

// lmOut is one reliable message to send to a cab.
type lmOut struct {
	Kind    byte
	Payload []byte
}

// lmResponder decides what to answer. It is called with the cab's lock held.
type lmResponder interface {
	// Connected runs once when a cab's link is accepted.
	Connected(c *lmCab) []lmOut
	// Message runs for every reliable message from the cab, in order.
	Message(c *lmCab, kind byte, payload []byte) []lmOut
	// Tick runs about once a second.
	Tick(c *lmCab, now time.Time) []lmOut
}

type lmPending struct {
	seq      uint32
	dgrams   [][]byte // one datagram per fragment, or one c4 record
	small    bool     // a c4 record that rides along with the link frame
	lastSent time.Time
}

// lmCab is one connected game.
type lmCab struct {
	Addr     *net.UDPAddr
	Index    uint16 // terminal index (session port - 48001)
	ID       uint32 // the cab's terminal id, learned from its first messages
	Info     map[string]any
	accepted bool

	ch        uint32
	sendSeq   uint32 // last seq we used
	recvSeq   uint32 // highest in-order seq received
	early     map[uint32]lmRecord
	frags     fragAssembler
	pending   []*lmPending
	pingCount uint32
	cabPing   uint32 // the game's latest ping counter, which our pong echoes
	gotPing   bool
	lastHeard time.Time
}

// LightLM is the Live Monitor stand-in.
type LightLM struct {
	IP        net.IP // the adapter's address: announced to games
	Bind      net.IP // what to bind; nil = 0.0.0.0 like the real LM
	Bcast     net.IP // the adapter's directed broadcast address, if known
	Group     uint32
	Verbose   bool // log every message from a game
	Responder lmResponder
	Logf      func(string, ...any)

	mu      sync.Mutex
	sess    *net.UDPConn
	disc    []*net.UDPConn
	cabs    map[string]*lmCab
	stopped chan struct{}
	once    sync.Once
	linked  bool
	seen    map[string]bool
}

// EverLinked: a game has linked up since Start.
func (s *LightLM) EverLinked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linked
}

func (s *LightLM) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	} else {
		log.Printf("light LM: "+f, a...)
	}
}

// lmListen binds like the real LM: all addresses (0.0.0.0), no address sharing. The
// game binds the same ports on its adapter's own address, which Windows allows next
// to a 0.0.0.0 socket; it is how the real LM and the game share 48000/49000 on one PC.
func lmListen(ip net.IP, port int) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) { serr = setBroadcast(fd) })
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("%s:%d", ip, port))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func (s *LightLM) ip4() [4]byte {
	var a [4]byte
	copy(a[:], s.IP.To4())
	return a
}

// Start binds the discovery and session ports and runs until Stop.
func (s *LightLM) Start() error {
	if s.IP.To4() == nil {
		return fmt.Errorf("light LM needs an IPv4 address, got %v", s.IP)
	}
	if s.Group == 0 {
		s.Group = 1 // what the real LM announces after a fresh boot; a game's announce overrides it
	}
	s.cabs = map[string]*lmCab{}
	s.stopped = make(chan struct{})
	bind := s.Bind
	if bind == nil {
		bind = net.IPv4zero
	}
	sess, err := lmListen(bind, lmSessionPort)
	if err != nil {
		return fmt.Errorf("session port %d (is the normal Live Monitor still running?): %w", lmSessionPort, err)
	}
	s.sess = sess
	c, err := lmListen(bind, lmDiscoveryPort)
	if err != nil {
		s.Stop()
		return fmt.Errorf("discovery port %d (is the normal Live Monitor still running?): %w", lmDiscoveryPort, err)
	}
	s.disc = append(s.disc, c)
	go s.discoveryLoop(c)
	go s.sessionLoop()
	go s.tickLoop()
	s.logf("listening on %s:%d and :%d, announcing as %s", bind, lmDiscoveryPort, lmSessionPort, s.IP)
	return nil
}

func (s *LightLM) Stop() {
	s.once.Do(func() {
		if s.stopped != nil {
			close(s.stopped)
		}
		if s.sess != nil {
			s.sess.Close()
		}
		for _, c := range s.disc {
			c.Close()
		}
	})
}

// Cabs reports how many games are linked.
func (s *LightLM) Cabs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.cabs {
		if c.accepted {
			n++
		}
	}
	return n
}

func (s *LightLM) discoveryLoop(c *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		a, ok := parseLMAnnounce(buf[:n])
		if !ok {
			continue
		}
		if a.Role != 1 {
			if a.Role == 2 && a.IP != s.ip4() {
				s.logf("another Live Monitor is announcing from %v: two in one store give error 21-11", net.IP(a.IP[:]))
			}
			continue // our own echo, or noise
		}
		s.mu.Lock()
		first := !s.seen[from.String()]
		if s.seen == nil {
			s.seen = map[string]bool{}
		}
		s.seen[from.String()] = true
		if a.Group != 0 {
			s.Group = a.Group
		}
		s.mu.Unlock()
		if first {
			s.logf("game %d is looking for a Live Monitor (from %s, group %d, session port %d)", a.Index, from, a.Group, a.SessionPort)
		}
		// like the real LM: announce to everyone at once, then answer the game directly
		s.broadcastAnnounce()
		reply := lmAnnounce(s.ip4(), 2, 0, s.Group, lmSessionPort, true)
		to := &net.UDPAddr{IP: net.IP(a.IP[:]).To4(), Port: lmDiscoveryPort}
		s.disc[0].WriteToUDP(reply, to)
	}
}

func (s *LightLM) broadcastAnnounce() {
	s.mu.Lock()
	g := s.Group
	s.mu.Unlock()
	a := lmAnnounce(s.ip4(), 2, 0, g, lmSessionPort, false)
	targets := []net.IP{net.IPv4bcast}
	if s.Bcast != nil { // the adapter's own broadcast, so it goes out the right card
		targets = append(targets, s.Bcast)
	}
	for _, bc := range targets {
		for i := 0; i < lmDiscoveryN; i++ {
			s.disc[0].WriteToUDP(a, &net.UDPAddr{IP: bc, Port: lmDiscoveryPort + i})
		}
	}
}

func (s *LightLM) sessionLoop() {
	buf := make([]byte, 65536)
	for {
		n, from, err := s.sess.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.handleDatagram(append([]byte(nil), buf[:n]...), from)
	}
}

func (s *LightLM) handleDatagram(b []byte, from *net.UDPAddr) {
	recs, tail, err := lmDecode(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := from.String()
	c := s.cabs[key]
	if c == nil {
		c = &lmCab{Addr: from, ch: 1, early: map[uint32]lmRecord{}, Info: map[string]any{}}
		if from.Port > 48000 && from.Port <= 48016 {
			c.Index = uint16(from.Port - 48001)
		}
		s.cabs[key] = c
	}
	c.lastHeard = time.Now()
	if err != nil {
		s.logf("cab %d: %v (%x)", c.Index, err, tail)
	}
	var out [][]byte
	for _, r := range recs {
		switch r.Type {
		case recLink:
			out = append(out, s.link(c, r)...)
		case recMsg, recFrag:
			s.reliable(c, r)
		}
	}
	for _, d := range out {
		s.sess.WriteToUDP(d, from)
	}
}

func (s *LightLM) link(c *lmCab, r lmRecord) [][]byte {
	switch r.Sub {
	case linkConnect:
		c.ch = r.Ch
		var ip [4]byte
		copy(ip[:], c.Addr.IP.To4())
		d := append(encAck(c.ch, 0), encAccept(c.ch, ip, uint16(c.Addr.Port), 1)...)
		d = append(d, encPing(c.ch, 0, true, nil)...)
		if !c.accepted {
			c.accepted = true
			s.linked = true
			s.logf("game %d linked (%s)", c.Index, c.Addr)
			s.queue(c, s.Responder.Connected(c))
		}
		return [][]byte{d}
	case linkAck:
		keep := c.pending[:0]
		for _, p := range c.pending {
			if p.seq > r.Seq {
				keep = append(keep, p)
			}
		}
		c.pending = keep
	case linkPing:
		if len(r.Raw) >= 24 && r.Raw[16] == 1 { // a ping: answer with a pong
			c.cabPing, c.gotPing = r.Seq, true
		}
	}
	return nil
}

func (s *LightLM) reliable(c *lmCab, r lmRecord) {
	if r.Seq <= c.recvSeq {
		return // a resend we already have
	}
	if r.Type == recFrag { // fragments of one message share its seq
		whole, done := c.frags.add(r)
		if !done {
			return
		}
		r = lmRecord{Type: recMsg, Seq: r.Seq, Ch: r.Ch, Kind: r.Kind, Payload: whole}
	}
	c.early[r.Seq] = r
	for {
		next, ok := c.early[c.recvSeq+1]
		if !ok {
			return
		}
		delete(c.early, next.Seq)
		c.recvSeq = next.Seq
		out := s.Responder.Message(c, next.Kind, next.Payload)
		if s.Verbose {
			id, flag, _ := ulgHeader(next.Payload)
			s.logf("game %d -> kind %d id %d flag %d (%d B), answering %d", c.Index, next.Kind, id, flag, len(next.Payload), len(out))
		}
		s.queue(c, out)
	}
}

func (s *LightLM) queue(c *lmCab, msgs []lmOut) {
	for _, m := range msgs {
		c.sendSeq++
		p := &lmPending{seq: c.sendSeq}
		if len(m.Payload) > lmFragData {
			p.dgrams = encFrags(p.seq, byte(c.ch), m.Kind, m.Payload)
		} else {
			p.dgrams = [][]byte{encMsg(p.seq, byte(c.ch), m.Kind, m.Payload)}
			p.small = true
		}
		c.pending = append(c.pending, p)
	}
}

// flush sends the link frame (ack + ping + pong), with unacked small messages in front.
func (s *LightLM) flush(c *lmCab, now time.Time) {
	var frame []byte
	for _, p := range c.pending {
		if !p.lastSent.IsZero() && now.Sub(p.lastSent) < lmResendAfter {
			continue
		}
		p.lastSent = now
		if p.small && len(frame)+len(p.dgrams[0]) < 1200 {
			frame = append(frame, p.dgrams[0]...)
			continue
		}
		for _, d := range p.dgrams {
			s.sess.WriteToUDP(d, c.Addr)
		}
	}
	frame = append(frame, encAck(c.ch, c.recvSeq)...)
	c.pingCount++
	// ping: our counter, flag 1. pong: the game's counter, flag 0, the rest zero
	frame = append(frame, encPing(c.ch, c.pingCount, true, nil)...)
	if c.gotPing {
		frame = append(frame, encPing(c.ch, c.cabPing, false, nil)...)
	}
	s.sess.WriteToUDP(frame, c.Addr)
}

func (s *LightLM) tickLoop() {
	link := time.NewTicker(lmLinkTick)
	defer link.Stop()
	lastSlow := time.Now()
	s.broadcastAnnounce()
	for {
		select {
		case <-s.stopped:
			return
		case now := <-link.C:
			s.mu.Lock()
			slow := now.Sub(lastSlow) >= time.Second
			for k, c := range s.cabs {
				if now.Sub(c.lastHeard) > lmCabTimeout {
					if c.accepted {
						s.logf("game %d gone (no packets for %v)", c.Index, lmCabTimeout)
					}
					delete(s.cabs, k)
					continue
				}
				if !c.accepted {
					continue
				}
				if slow {
					s.queue(c, s.Responder.Tick(c, now))
				}
				s.flush(c, now)
			}
			s.mu.Unlock()
			if slow {
				lastSlow = now
				s.broadcastAnnounce()
			}
		}
	}
}

// others lists the other linked games (for a store with more than one). Called with
// s.mu held, from inside a Responder call.
func (s *LightLM) others(c *lmCab) []*lmCab {
	var out []*lmCab
	for _, o := range s.cabs {
		if o != c && o.accepted && o.ID != 0 {
			out = append(out, o)
		}
	}
	return out
}

// sendTo queues a message to another game. Called with s.mu held.
func (s *LightLM) sendTo(c *lmCab, m lmOut) { s.queue(c, []lmOut{m}) }
