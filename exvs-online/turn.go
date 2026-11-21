package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/pion/stun/v3"
	"github.com/pion/turn/v4"
)

// LocalTurnConfig runs a TURN relay inside exvs-online (host side). The game falls back to
// TURN when two players can't reach each other directly (strict NAT). Players reach it over
// the internet, so on a home PC the router must forward UDP Listen's port and MinPort..MaxPort.
type LocalTurnConfig struct {
	Listen  string `json:"listen"`  // UDP, default 0.0.0.0:3480
	RelayIP string `json:"relayIP"` // address players use: "auto" = public IP found via STUN
	MinPort int    `json:"minPort"` // relay port range, default 49160..49200
	MaxPort int    `json:"maxPort"`
	User    string `json:"user"` // default "exvs"
	Pass    string `json:"pass"` // empty = random per run (shared with players via /turninfo)
}

// turnInfo is what goes into RegisterPcb's SRV_TURN entry, and what /turninfo serves.
type turnInfo struct {
	Host string `json:"host"`
	Port uint32 `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

var localTurn *turnInfo // set when this process runs the relay

func startTurn(cfg *LocalTurnConfig, stunHost *HostPort, tl *trafficLog) error {
	if cfg == nil || cfg.Listen == "" {
		return nil
	}
	if cfg.MinPort == 0 {
		cfg.MinPort, cfg.MaxPort = 49160, 49200
	}
	if cfg.User == "" {
		cfg.User = "exvs"
	}
	if cfg.Pass == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		cfg.Pass = hex.EncodeToString(b)
	}
	relay := cfg.RelayIP
	if relay == "" || relay == "auto" {
		srv := "stun.l.google.com:19302"
		if stunHost != nil && stunHost.Host != "" && stunHost.Host != "auto" {
			srv = net.JoinHostPort(stunHost.Host, fmt.Sprint(stunHost.Port))
		}
		ip, err := publicIP(srv)
		if err != nil {
			return fmt.Errorf("turn: can't find this PC's public IP via %s (set localTurn.relayIP): %w", srv, err)
		}
		relay = ip
	}
	relayIP := net.ParseIP(relay)
	if relayIP == nil {
		return fmt.Errorf("turn: bad relayIP %q", relay)
	}
	conn, err := net.ListenPacket("udp4", cfg.Listen)
	if err != nil {
		return fmt.Errorf("turn listen %s: %w", cfg.Listen, err)
	}
	key := turn.GenerateAuthKey(cfg.User, "exvs", cfg.Pass)
	var seen sync.Map
	_, err = turn.NewServer(turn.ServerConfig{
		Realm: "exvs",
		AuthHandler: func(user, realm string, src net.Addr) ([]byte, bool) {
			if _, dup := seen.LoadOrStore(src.String(), true); !dup {
				tl.Line("TURN>", fmt.Sprintf("client %s user %q", src, user))
			}
			if user != cfg.User {
				tl.Line("TURN<", fmt.Sprintf("rejected %s: unknown user %q", src, user))
				return nil, false
			}
			return key, true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: relayIP, Address: "0.0.0.0",
				MinPort: uint16(cfg.MinPort), MaxPort: uint16(cfg.MaxPort),
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("turn: %w", err)
	}
	_, port, _ := net.SplitHostPort(cfg.Listen)
	var p uint32
	fmt.Sscan(port, &p)
	localTurn = &turnInfo{Host: relay, Port: p, User: cfg.User, Pass: cfg.Pass}
	log.Printf("TURN relay on udp %s, players use %s:%d, relay ports %d-%d (forward these on your router if you host from home)",
		cfg.Listen, relay, p, cfg.MinPort, cfg.MaxPort)
	return nil
}

// publicIP asks a STUN server what address this PC's packets come from.
func publicIP(server string) (string, error) {
	c, err := net.DialTimeout("udp4", server, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	for try := 0; try < 3; try++ {
		if _, err := c.Write(msg.Raw); err != nil {
			return "", err
		}
		buf := make([]byte, 1500)
		n, err := c.Read(buf)
		if err != nil {
			continue
		}
		res := &stun.Message{Raw: buf[:n]}
		if res.Decode() != nil {
			continue
		}
		var xor stun.XORMappedAddress
		if xor.GetFrom(res) == nil {
			return xor.IP.String(), nil
		}
		var mapped stun.MappedAddress
		if mapped.GetFrom(res) == nil {
			return mapped.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no STUN reply")
}

// turnInfoHandler serves the host's relay details to players' proxies.
func turnInfoHandler(w http.ResponseWriter, r *http.Request) {
	if localTurn == nil {
		http.Error(w, "no relay on this host", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(localTurn)
}

// remoteTurn caches the match host's /turninfo for players in proxy mode.
type remoteTurn struct {
	mu      sync.Mutex
	info    *turnInfo
	fetched time.Time
}

func (rt *remoteTurn) get(c *http.Client, base string) *turnInfo {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if time.Since(rt.fetched) < 2*time.Minute {
		return rt.info
	}
	rt.fetched = time.Now()
	rt.info = nil
	resp, err := c.Get(base + "/turninfo")
	if err != nil {
		log.Printf("turninfo: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var ti turnInfo
	if json.NewDecoder(resp.Body).Decode(&ti) == nil && ti.Host != "" {
		rt.info = &ti
	}
	return rt.info
}
