package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"exvsonline/pb/exvs"
	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

// fakeCardServer answers RegisterPcb the way the POC server does.
func fakeCardServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := &exvs.Request{}
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, req); err != nil {
			t.Errorf("upstream got bad body: %v", err)
		}
		resp := &exvs.Response{Type: req.Type, RequestId: req.RequestId, Error: exvs.Error_SUCCESS.Enum()}
		if req.GetType() == exvs.MethodType_MTHD_REGISTER_PCB {
			resp.RegisterPcb = &exvs.Response_RegisterPcb{
				SramClear: proto.Bool(true), NextMaintenanceStartAt: proto.Uint64(1), NextMaintenanceEndAt: proto.Uint64(2),
				ServerInfo: []*exvs.Response_RegisterPcb_ServerInfo{{
					ServerType: exvs.ServerType_SRV_MATCH.Enum(), Uri: proto.String(r.Host + "/match"), Port: proto.Uint32(12345)}},
			}
		}
		out, _ := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		w.Write(out)
	}))
}

func post(t *testing.T, url string, m proto.Message, out proto.Message) {
	b, _ := proto.MarshalOptions{AllowPartial: true}.Marshal(m)
	r, err := http.Post(url, "application/x-protobuf", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	rb, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("%s -> %d %s", url, r.StatusCode, rb)
	}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(rb, out); err != nil {
		t.Fatal(err)
	}
}

func newTestProxy(t *testing.T, mc MatchConfig) (*httptest.Server, *httptest.Server) {
	up := fakeCardServer(t)
	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	pc.Stun = &HostPort{Host: "203.0.113.5", Port: 3478}
	pc.Turn = &HostPort{Host: "203.0.113.5", Port: 3478, User: "exvs", Pass: "pw"}
	p, err := NewProxy(pc, NewMatcher(mc), &trafficLog{})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(p), up
}

func TestRegisterPcbPatched(t *testing.T) {
	px, up := newTestProxy(t, defaultConfig().Match)
	defer px.Close()
	defer up.Close()
	resp := &exvs.Response{}
	post(t, px.URL+"/", &exvs.Request{Type: exvs.MethodType_MTHD_REGISTER_PCB.Enum(), RequestId: proto.String("r1"),
		RegisterPcb: &exvs.Request_RegisterPcb{}}, resp)
	got := map[exvs.ServerType]*exvs.Response_RegisterPcb_ServerInfo{}
	for _, s := range resp.GetRegisterPcb().GetServerInfo() {
		got[s.GetServerType()] = s
	}
	if len(got) != 3 {
		t.Fatalf("want STUN, TURN, MATCH, got %v", resp.GetRegisterPcb().GetServerInfo())
	}
	if u := got[exvs.ServerType_SRV_MATCH].GetUri(); u != px.Listener.Addr().String()+"/match" {
		t.Errorf("match uri should point at proxy, got %q", u)
	}
	if got[exvs.ServerType_SRV_TURN].GetAccountName() != "exvs" {
		t.Error("turn credentials missing")
	}
	if len(resp.GetRegisterPcb().GetLmIpAddresses()) != 0 {
		t.Error("lm_ip_addresses should be left alone by default")
	}
}

func client(node uint32, ip string) *mms.Client {
	return &mms.Client{NodeId: proto.Uint32(node), PcbSerial: proto.String("PCB"), LocId: proto.String("L"),
		PrefCode: proto.Uint32(13), GlobalIpaddress: proto.String(ip), GlobalPort: proto.Uint32(50000),
		NatMappingType: mms.NatMappingType_NM_ENDPOINT_INDEPENDENT.Enum(), NatFilteringType: mms.NatFilteringType_NF_ENDPOINT_INDEPENDENT.Enum()}
}

func issue(t *testing.T, url string) uint32 {
	r := &mms.Response{}
	post(t, url, &mms.Request{Type: mms.MethodType_ISSUE_NODE_ID.Enum(), RequestId: proto.String("i"),
		IssueNodeId: &mms.Request_IssueNodeId{PcbSerial: proto.String("PCB"), LocId: proto.String("L"), IssueAt: proto.Uint32(1)}}, r)
	return r.GetIssueNodeId().GetNodeId()[0]
}

func entry2(t *testing.T, url string, node uint32, partner *mms.Client) {
	em := &mms.Request_EntryMatching{ApplyId: proto.Uint32(0), Myclient: client(node, "198.51.100.1"),
		Myplayer: &mms.Player{BanapassportAmId: proto.Uint32(node)}, MatchingTimeout: proto.Uint32(60), EntryAt: proto.Uint32(1),
		World: &mms.World{MuchaGameId: proto.String("SBUZ"), Version: proto.Uint32(1), WorldId: proto.Uint32(1)}}
	if partner != nil {
		em.Groups = []*mms.Request_EntryMatching_Group{{Client: partner, Player: &mms.Player{BanapassportAmId: proto.Uint32(9)}}}
	}
	r := &mms.Response{}
	post(t, url, &mms.Request{Type: mms.MethodType_ENTRY_MATCHING.Enum(), RequestId: proto.String("e"), EntryMatching: em}, r)
	if r.GetCode() != mms.ErrorCode_SUCCESS || r.GetEntryMatching().GetPolingInterval() != 3 {
		t.Fatalf("entry failed: %v", r)
	}
}

func check(t *testing.T, url string, node uint32) *mms.Response {
	r := &mms.Response{}
	post(t, url, &mms.Request{Type: mms.MethodType_CHECK_MATCHING.Enum(), RequestId: proto.String("c"),
		CheckMatching: &mms.Request_CheckMatching{NodeId: proto.Uint32(node), CheckAt: proto.Uint32(2)}}, r)
	return r
}

func TestFourSolosMakeOneRoom(t *testing.T) {
	px, up := newTestProxy(t, defaultConfig().Match)
	defer px.Close()
	defer up.Close()
	url := px.URL + "/match"
	var nodes []uint32
	for i := 0; i < 4; i++ {
		n := issue(t, url)
		nodes = append(nodes, n)
		entry2(t, url, n, nil)
		if i < 3 {
			if r := check(t, url, n); r.GetCode() != mms.ErrorCode_SUCCESS || len(r.GetCheckMatching().GetClients()) != 0 {
				t.Fatalf("should still wait, got %v", r)
			}
		}
	}
	var room uint32
	for _, n := range nodes {
		r := check(t, url, n)
		cm := r.GetCheckMatching()
		if r.GetCode() != mms.ErrorCode_SUCCESS || cm.GetRoomId() == 0 || len(cm.GetClients()) != 4 {
			t.Fatalf("node %d not matched: %v", n, r)
		}
		if room == 0 {
			room = cm.GetRoomId()
		} else if cm.GetRoomId() != room {
			t.Fatal("different rooms")
		}
		teams := map[uint32]int{}
		for _, c := range cm.GetClients() {
			teams[c.GetGroupId()]++
		}
		if teams[0] != 2 || teams[1] != 2 {
			t.Fatalf("teams not 2v2: %v", teams)
		}
	}
}

func TestPairsStayTogether(t *testing.T) {
	px, up := newTestProxy(t, defaultConfig().Match)
	defer px.Close()
	defer up.Close()
	url := px.URL + "/match"
	a, b := issue(t, url), issue(t, url)
	entry2(t, url, a, client(a+500, "198.51.100.2"))
	entry2(t, url, b, client(b+500, "198.51.100.3"))
	cm := check(t, url, a).GetCheckMatching()
	if len(cm.GetClients()) != 4 {
		t.Fatalf("want 4 clients, got %v", cm)
	}
	team := map[uint32]uint32{}
	for _, c := range cm.GetClients() {
		team[c.GetClient().GetNodeId()] = c.GetGroupId()
	}
	if team[a] != team[a+500] || team[b] != team[b+500] || team[a] == team[b] {
		t.Fatalf("pairs split wrongly: %v", team)
	}
}

func TestCancelAndNoEntry(t *testing.T) {
	px, up := newTestProxy(t, defaultConfig().Match)
	defer px.Close()
	defer up.Close()
	url := px.URL + "/match"
	n := issue(t, url)
	entry2(t, url, n, nil)
	r := &mms.Response{}
	post(t, url, &mms.Request{Type: mms.MethodType_CANCEL_MATCHING.Enum(), RequestId: proto.String("x"),
		CancelMatching: &mms.Request_CancelMatching{NodeId: proto.Uint32(n), ApplyId: proto.Uint32(1), CancelAt: proto.Uint32(3)}}, r)
	if r.GetCode() != mms.ErrorCode_SUCCESS {
		t.Fatal(r)
	}
	if check(t, url, n).GetCode() != mms.ErrorCode_ERR_MATCH_NOENTRY {
		t.Fatal("cancelled entry still present")
	}
}

func TestOtherTrafficPassesThrough(t *testing.T) {
	px, up := newTestProxy(t, defaultConfig().Match)
	defer px.Close()
	defer up.Close()
	resp := &exvs.Response{}
	post(t, px.URL+"/", &exvs.Request{Type: exvs.MethodType_MTHD_PING.Enum(), RequestId: proto.String("p")}, resp)
	if resp.GetRequestId() != "p" || resp.GetType() != exvs.MethodType_MTHD_PING {
		t.Fatalf("passthrough broken: %v", resp)
	}
}

func TestBotsFillRoom(t *testing.T) {
	mc := defaultConfig().Match
	mc.TestBots = 3
	px, up := newTestProxy(t, mc)
	defer px.Close()
	defer up.Close()
	url := px.URL + "/match"
	n := issue(t, url)
	entry2(t, url, n, nil)
	cm := check(t, url, n).GetCheckMatching()
	if cm.GetRoomId() == 0 || len(cm.GetClients()) != 4 {
		t.Fatalf("bots didn't fill the room: %v", cm)
	}
}

func TestLoadGameDataGetsOnVsInfo(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := &exvs.Response{Type: exvs.MethodType_MTHD_LOAD_GAME_DATA.Enum(), RequestId: proto.String("g"),
			Error: exvs.Error_SUCCESS.Enum(), LoadGameData: &exvs.Response_LoadGameData{TrainingTimeLimit: proto.Uint32(12)}}
		b, _ := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		w.Write(b)
	}))
	defer up.Close()
	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	p, _ := NewProxy(pc, NewMatcher(defaultConfig().Match), &trafficLog{})
	px := httptest.NewServer(p)
	defer px.Close()
	resp := &exvs.Response{}
	post(t, px.URL+"/", &exvs.Request{Type: exvs.MethodType_MTHD_LOAD_GAME_DATA.Enum(), RequestId: proto.String("g")}, resp)
	ov := resp.GetLoadGameData().GetOnVsInfo()
	if ov.GetRuleTimeLimitFree() != 230 || ov.GetRuleDamageLevelTeam() != 2 || resp.GetLoadGameData().GetTrainingTimeLimit() != 12 {
		t.Fatalf("on_vs_info not added correctly: %v", resp.GetLoadGameData())
	}
	if err := proto.CheckInitialized(resp); err != nil {
		t.Fatalf("required fields still missing: %v", err)
	}
}

func TestStunBindingResponse(t *testing.T) {
	tl := &trafficLog{}
	cfg := &LocalStunConfig{Listen: "127.0.0.1:0"}
	main, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	alt, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	go stunServe(main, main, alt, tl, "main")
	_ = cfg
	c, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer c.Close()
	// RFC 5389 binding request with CHANGE-REQUEST (change port).
	req := []byte{0, 1, 0, 8, 0x21, 0x12, 0xA4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0, 3, 0, 4, 0, 0, 0, 2}
	c.WriteTo(req, main.LocalAddr())
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, from, err := c.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if from.String() != alt.LocalAddr().String() {
		t.Errorf("change-port answer should come from alt port, came from %s", from)
	}
	if buf[0] != 1 || buf[1] != 1 || n < 32 || !bytes.Equal(buf[8:20], req[8:20]) {
		t.Fatalf("bad response %x", buf[:n])
	}
}
