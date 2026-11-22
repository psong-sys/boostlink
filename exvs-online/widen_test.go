package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"exvsonline/pb/exvs"
	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

func queueSerial(m *Matcher, node uint32, serial string) {
	m.handle(&mms.Request{Type: mms.MethodType_ENTRY_MATCHING.Enum(), RequestId: proto.String("e"),
		EntryMatching: &mms.Request_EntryMatching{Myclient: &mms.Client{NodeId: proto.Uint32(node), PcbSerial: proto.String(serial),
			LocId: proto.String("L"), PrefCode: proto.Uint32(1)}}}, "203.0.113.1")
}

func TestFailedPairTriesSomeoneElse(t *testing.T) {
	cfg := defaultConfig().Match
	cfg.RoomSize = 2
	m := NewMatcher(cfg)
	queueSerial(m, 1, "AAAA")
	queueSerial(m, 2, "BBBB")
	a := m.entries[1]
	if a.roomID == 0 {
		t.Fatal("first pair should match")
	}
	// A reports that the connection timed out.
	m.addResult(ConnResult{Result: "FAIL_TIMEOUT", ApplyID: a.applyID, Found: true})
	if l := m.failedPairList(); len(l) != 1 || l[0] != "AAAA|BBBB" {
		t.Fatalf("failed pairs %v", l)
	}
	// C is already searching when A and B come back: A should get C, not B again.
	queueSerial(m, 13, "CCCC")
	queueSerial(m, 11, "AAAA")
	if m.entries[11].roomID == 0 || m.entries[11].roomID != m.entries[13].roomID {
		t.Fatal("A should be matched with C")
	}
	// With nobody else searching, the failed pair is retried straight away.
	queueSerial(m, 12, "BBBB")
	queueSerial(m, 21, "AAAA")
	if m.entries[12].roomID == 0 || m.entries[12].roomID != m.entries[21].roomID {
		t.Fatal("A and B should be retried when they are the only two searching")
	}
}

func TestRTTLimitFromHost(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := &exvs.Response{Type: exvs.MethodType_MTHD_LOAD_BLACK_LIST.Enum(), RequestId: proto.String("b"),
			LoadBlackList: &exvs.Response_LoadBlackList{ThresholdDelayedRtt: proto.Uint32(300), MaxBlacklistNum: proto.Uint32(1),
				DelayRestrictCondition: proto.Uint32(300), DelayPermitCondition: proto.Uint32(300), ThresholdDelayedFrame: proto.Uint32(5)}}
		b, _ := proto.Marshal(resp)
		w.Write(b)
	}))
	defer up.Close()
	mc := defaultConfig().Match
	mc.RTTLimitMs = 450
	hc := defaultConfig().Proxy
	hc.Upstream = up.URL
	hp, _ := NewProxy(hc, NewMatcher(mc), &trafficLog{})
	host := httptest.NewServer(hp)
	defer host.Close()

	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	pc.MatchServer = host.URL
	pl, _ := NewProxy(pc, nil, &trafficLog{})
	px := httptest.NewServer(pl)
	defer px.Close()

	for _, u := range []string{host.URL, px.URL} {
		resp := &exvs.Response{}
		post(t, u+"/", &exvs.Request{Type: exvs.MethodType_MTHD_LOAD_BLACK_LIST.Enum(), RequestId: proto.String("b")}, resp)
		bl := resp.GetLoadBlackList()
		if bl.GetThresholdDelayedRtt() != 450 || bl.GetDelayPermitCondition() != 450 || bl.GetThresholdDelayedFrame() != 5 {
			t.Fatalf("%s: %v", u, bl)
		}
	}
	if rttLimitNow() != 450 {
		t.Fatal(rttLimitNow())
	}
}

func TestDiscordPingOncePerQuietQueue(t *testing.T) {
	var n int32
	var body string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(204)
	}))
	defer hook.Close()
	cfg := defaultConfig().Match
	cfg.RoomSize = 4
	cfg.DiscordWebhook = hook.URL
	m := NewMatcher(cfg)
	queueSerial(m, 1, "AAAA")
	queueSerial(m, 2, "BBBB") // queue not empty: no second post
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&n) != 1 {
		t.Fatalf("posts = %d", n)
	}
	var msg map[string]any
	if json.Unmarshal([]byte(body), &msg) != nil || msg["username"] != "Boost Link" {
		t.Fatal(body)
	}
	for _, s := range []string{"AAAA", "203.0.113"} {
		if contains(body, s) {
			t.Fatalf("discord post leaks %s", s)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestStunMatchHostAdvertised(t *testing.T) {
	up := fakeCardServer(t)
	defer up.Close()
	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	pc.MatchServer = "http://203.0.113.7:28090"
	pc.Stun = &HostPort{Host: "match", Port: 3478}
	pl, _ := NewProxy(pc, nil, &trafficLog{})
	px := httptest.NewServer(pl)
	defer px.Close()
	resp := &exvs.Response{}
	post(t, px.URL+"/", &exvs.Request{Type: exvs.MethodType_MTHD_REGISTER_PCB.Enum(), RequestId: proto.String("r"),
		RegisterPcb: &exvs.Request_RegisterPcb{}}, resp)
	ok := false
	for _, s := range resp.GetRegisterPcb().GetServerInfo() {
		if s.GetServerType() == exvs.ServerType_SRV_STUN && s.GetUri() == "203.0.113.7" && s.GetPort() == 3478 {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("no STUN entry for the match host: %v", resp.GetRegisterPcb().GetServerInfo())
	}
}
