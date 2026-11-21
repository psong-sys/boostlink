package main

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/turn/v4"
	"google.golang.org/protobuf/proto"

	"exvsonline/pb/exvs"
)

func TestTurnRelayAndTurninfo(t *testing.T) {
	localTurn = nil
	defer func() { localTurn = nil }()
	cfg := &LocalTurnConfig{Listen: "127.0.0.1:34780", RelayIP: "127.0.0.1", MinPort: 49300, MaxPort: 49310}
	if err := startTurn(cfg, nil, &trafficLog{}); err != nil {
		t.Fatal(err)
	}
	// A TURN client can allocate a relay with the advertised credentials.
	conn, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer conn.Close()
	c, err := turn.NewClient(&turn.ClientConfig{STUNServerAddr: "127.0.0.1:34780", TURNServerAddr: "127.0.0.1:34780",
		Conn: conn, Username: localTurn.User, Password: localTurn.Pass, Realm: "exvs"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Listen(); err != nil {
		t.Fatal(err)
	}
	relay, err := c.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer relay.Close()
	// Relay round trip: a peer sends to the relayed address and the client receives it.
	peer, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer peer.Close()
	if _, err := relay.WriteTo([]byte("hi"), peer.LocalAddr()); err != nil { // creates the permission
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := peer.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("peer didn't get relayed data: %v", err)
	}
	if _, err := peer.WriteTo([]byte("yo"), from); err != nil {
		t.Fatal(err)
	}
	_ = relay.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _, err = relay.ReadFrom(buf); err != nil || string(buf[:n]) != "yo" {
		t.Fatalf("client didn't get reply via relay: %v", err)
	}

	// Host proxy serves /turninfo; a player proxy with turn "auto" puts it in RegisterPcb.
	hostProxy, _ := NewProxy(defaultConfig().Proxy, NewMatcher(defaultConfig().Match), &trafficLog{})
	host := httptest.NewServer(hostProxy)
	defer host.Close()
	up := fakeCardServer(t)
	defer up.Close()
	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	pc.MatchServer = host.URL
	pc.Turn = &HostPort{Host: "auto"}
	pl, err := NewProxy(pc, nil, &trafficLog{})
	if err != nil {
		t.Fatal(err)
	}
	hostInfo := *localTurn
	ti := pl.rturn.get(pl.client, host.URL) // fetched over HTTP from the host
	if ti == nil || ti.Pass != hostInfo.Pass || ti.Port != 34780 {
		t.Fatalf("turninfo fetch failed: %+v", ti)
	}
	localTurn = nil // from here on, behave like a player's process with no relay of its own
	px := httptest.NewServer(pl)
	defer px.Close()
	resp := &exvs.Response{}
	post(t, px.URL+"/", &exvs.Request{Type: exvs.MethodType_MTHD_REGISTER_PCB.Enum(), RequestId: proto.String("r"),
		RegisterPcb: &exvs.Request_RegisterPcb{}}, resp)
	found := false
	for _, s := range resp.GetRegisterPcb().GetServerInfo() {
		if s.GetServerType() == exvs.ServerType_SRV_TURN && s.GetUri() == "127.0.0.1" && s.GetPort() == 34780 &&
			s.GetAccountName() == "exvs" && s.GetAccountPass() == hostInfo.Pass {
			found = true
		}
	}
	if !found {
		t.Fatalf("SRV_TURN not advertised: %v", resp.GetRegisterPcb().GetServerInfo())
	}
}
