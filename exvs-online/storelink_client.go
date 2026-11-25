package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// slDevice is the virtual network card (Wintun on Windows, a fake in tests).
type slDevice interface {
	Read() ([]byte, error) // one IPv4/IPv6 packet from the PC
	Write(p []byte) error  // hand one packet to the PC
	Close() error
}

type slStatus struct {
	mu      sync.Mutex
	State   string `json:"state"` // starting | connecting | connected | error | stopped
	IP      string `json:"ip,omitempty"`
	Members int    `json:"members"`
	Error   string `json:"error,omitempty"`
	Sent    uint64 `json:"sent"`
	Recv    uint64 `json:"recv"`
	Adapter string `json:"adapter"`
}

func (s *slStatus) set(f func(*slStatus)) {
	s.mu.Lock()
	f(s)
	s.mu.Unlock()
}

func (s *slStatus) snapshot() slStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slStatus{State: s.State, IP: s.IP, Members: s.Members, Error: s.Error, Sent: s.Sent, Recv: s.Recv, Adapter: s.Adapter}
}

// slServeStatus answers the launcher on 127.0.0.1: GET /status, POST /stop.
func slServeStatus(addr string, st *slStatus, stop func()) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		s := st.snapshot()
		_ = json.NewEncoder(w).Encode(&s)
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		fmt.Fprintln(w, "stopping")
		go stop()
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return srv, nil
}

// slServerAddr adds the default port (28090, UDP) to a host.
func slServerAddr(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		s = net.JoinHostPort(s, "28090")
	}
	return s
}

// StoreClient joins a store-link team and moves packets between the device and the relay.
type StoreClient struct {
	Server string
	Code   string
	Status *slStatus
	// SetIP gives the device its address (netsh on Windows). Called again if it changes.
	SetIP func(ip netip.Addr) error

	id   [16]byte
	conn *net.UDPConn
	ip   atomic.Value // netip.Addr

	pmu     sync.Mutex
	pending [][]byte // packets that arrived before the address was set
}

// Run blocks until stop is closed or something fails for good.
func (c *StoreClient) Run(dev slDevice, stop <-chan struct{}) error {
	if strings.TrimSpace(c.Code) == "" {
		return errors.New("a team code is needed for Store Team")
	}
	if len(c.Code) > 64 {
		c.Code = c.Code[:64]
	}
	_, _ = rand.Read(c.id[:])
	ua, err := net.ResolveUDPAddr("udp4", slServerAddr(c.Server))
	if err != nil {
		return fmt.Errorf("server address: %w", err)
	}
	conn, err := net.DialUDP("udp4", nil, ua)
	if err != nil {
		return err
	}
	c.conn = conn
	defer conn.Close()
	c.Status.set(func(s *slStatus) { s.State = "connecting" })

	welcome := make(chan netip.Addr, 1)
	full := make(chan struct{}, 1)
	errc := make(chan error, 2)
	go func() { errc <- c.fromServer(dev, welcome, full) }()
	go func() { errc <- c.toServer(dev) }()

	hello := append(slHeader(slHello), c.id[:]...)
	hello = append(hello, byte(len(c.Code)))
	hello = append(hello, c.Code...)
	tick := time.NewTicker(slKeepalive)
	defer tick.Stop()
	fast := time.NewTicker(500 * time.Millisecond) // until the first WELCOME
	defer fast.Stop()
	_, _ = conn.Write(hello)
	lastWelcome := time.Time{}
	started := time.Now()
	for {
		select {
		case <-stop:
			_, _ = conn.Write(slHeader(slBye))
			return nil
		case err := <-errc:
			return err
		case <-full:
			return errors.New("this team code already has 8 players")
		case ip := <-welcome:
			lastWelcome = time.Now()
			fast.Stop()
			old, _ := c.ip.Load().(netip.Addr)
			if old != ip {
				log.Printf("store link: this PC is %s on the team network", ip)
				if c.SetIP != nil {
					if err := c.SetIP(ip); err != nil {
						return fmt.Errorf("setting the Boost Link adapter address: %w", err)
					}
				}
				c.pmu.Lock()
				c.ip.Store(ip)
				held := c.pending
				c.pending = nil
				c.pmu.Unlock()
				for _, p := range held {
					_ = dev.Write(p)
				}
				c.Status.set(func(s *slStatus) { s.State = "connected"; s.IP = ip.String(); s.Error = "" })
			}
		case <-fast.C:
			if lastWelcome.IsZero() && time.Since(started) > 20*time.Second {
				return fmt.Errorf("no answer from %s on UDP port %d. The server may need the Store Team update, or its firewall is closed", ua.IP, ua.Port)
			}
			_, _ = conn.Write(hello)
		case <-tick.C:
			_, _ = conn.Write(hello)
			if !lastWelcome.IsZero() && time.Since(lastWelcome) > slTimeout {
				c.Status.set(func(s *slStatus) { s.State = "connecting"; s.Error = "lost the server, retrying" })
			}
		}
	}
}

func (c *StoreClient) fromServer(dev slDevice, welcome chan<- netip.Addr, full chan<- struct{}) error {
	buf := make([]byte, 65535)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// ICMP "port unreachable" shows up as a read error on Windows; keep going.
			time.Sleep(200 * time.Millisecond)
			continue
		}
		t, body, ok := slParse(buf[:n])
		if !ok {
			continue
		}
		switch t {
		case slWelcome:
			if len(body) >= 5 {
				ip := netip.AddrFrom4([4]byte(body[:4]))
				members := int(body[4])
				c.Status.set(func(s *slStatus) { s.Members = members })
				select {
				case welcome <- ip:
				default:
				}
			}
		case slFull:
			select {
			case full <- struct{}{}:
			default:
			}
		case slData:
			if _, ok := c.ip.Load().(netip.Addr); !ok {
				c.pmu.Lock()
				if _, ok := c.ip.Load().(netip.Addr); !ok {
					if len(c.pending) < 64 {
						c.pending = append(c.pending, append([]byte(nil), body...))
					}
					c.pmu.Unlock()
					continue
				}
				c.pmu.Unlock()
			}
			if err := dev.Write(body); err != nil {
				return fmt.Errorf("writing to the Boost Link adapter: %w", err)
			}
			c.Status.set(func(s *slStatus) { s.Recv++ })
		}
	}
}

func (c *StoreClient) toServer(dev slDevice) error {
	for {
		p, err := dev.Read()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("reading the Boost Link adapter: %w", err)
		}
		me, ok := c.ip.Load().(netip.Addr)
		if !ok || !slWanted(p) {
			continue
		}
		if src, ok := slIPv4Src(p); !ok || src != me {
			continue
		}
		if _, err := c.conn.Write(append(slHeader(slData), p...)); err == nil {
			c.Status.set(func(s *slStatus) { s.Sent++ })
		}
	}
}
