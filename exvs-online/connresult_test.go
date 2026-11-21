package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exvsonline/pb/exvs"

	"google.golang.org/protobuf/proto"
)

func checkReq(hp exvs.UdpHpResultType) *exvs.Request {
	return &exvs.Request{Type: exvs.MethodType_MTHD_CHECK_COMMUNICATION.Enum(), RequestId: proto.String("c"),
		CheckCommunication: &exvs.Request_CheckCommunication{
			MatchingTryNum: proto.Uint32(2), FoundPlayerFlag: proto.Bool(true), UdpHpResult: hp.Enum(),
			StunResult: &exvs.Request_CheckCommunication_StunResult{
				NatMappingType:   exvs.Request_CheckCommunication_StunResult_NM_ENDPOINT_INDEPENDENT.Enum(),
				NatFilteringType: exvs.Request_CheckCommunication_StunResult_NF_ADDRESS_AND_PORT_DEPENDENT.Enum()},
			Players: []*exvs.Request_CheckCommunication_OtherPlayerInfo{{LocId: proto.String("SECRETLOC"), PcbSerial: proto.String("SECRETSERIAL"), Rtt: proto.Uint32(87)}},
		}}
}

func TestCheckCommunicationReachesHostStatus(t *testing.T) {
	up := fakeCardServer(t)
	defer up.Close()
	hc := defaultConfig().Proxy
	hc.Upstream = up.URL
	hm := NewMatcher(defaultConfig().Match)
	hp, _ := NewProxy(hc, hm, &trafficLog{})
	host := httptest.NewServer(hp)
	defer host.Close()

	pc := defaultConfig().Proxy
	pc.Upstream = up.URL
	pc.MatchServer = host.URL
	pl, err := NewProxy(pc, nil, &trafficLog{})
	if err != nil {
		t.Fatal(err)
	}
	px := httptest.NewServer(pl)
	defer px.Close()

	post(t, px.URL+"/", checkReq(exvs.UdpHpResultType_UDPHP_FAIL_TIMEOUT_NO_PACKETS), &exvs.Response{})
	post(t, host.URL+"/", checkReq(exvs.UdpHpResultType_UDPHP_SUCCESS), &exvs.Response{})

	var st Status
	for i := 0; i < 50; i++ {
		resp, err := http.Get(host.URL + "/status.json")
		if err != nil {
			t.Fatal(err)
		}
		st = Status{}
		_ = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if len(st.Results) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(st.Results) != 2 {
		t.Fatalf("want 2 results, got %+v", st.Results)
	}
	byFrom := map[string]ConnResult{}
	for _, r := range st.Results {
		byFrom[r.From] = r
	}
	p := byFrom["player PC"]
	if p.Result != "FAIL_TIMEOUT_NO_PACKETS" || p.Connected || !strings.Contains(p.Fix, "Firewall") || len(p.RTTs) != 1 || p.RTTs[0] != 87 {
		t.Fatalf("player result wrong: %+v", p)
	}
	if h := byFrom["host PC"]; h.Result != "SUCCESS" || !h.Connected || h.NAT != "easy NAT, filters by address and port (normal home router)" {
		t.Fatalf("host result wrong: %+v", h)
	}
	resp, _ := http.Get(host.URL + "/status")
	html, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"Connection results", "FAIL_TIMEOUT_NO_PACKETS", "87 ms"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("status page missing %q", want)
		}
	}
	for _, secret := range []string{"SECRETLOC", "SECRETSERIAL"} {
		if strings.Contains(string(html), secret) {
			t.Errorf("status page leaks %s", secret)
		}
	}
}

func TestNoOpponentLine(t *testing.T) {
	c := checkReq(exvs.UdpHpResultType_UDPHP_NONE).CheckCommunication
	c.FoundPlayerFlag = proto.Bool(false)
	c.Players = nil
	l := summarizeCheck(c, "host PC").Line()
	if !strings.Contains(l, "Nobody was matched") || !strings.Contains(l, "no opponent") {
		t.Fatal(l)
	}
}
