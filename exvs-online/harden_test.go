package main

import (
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

func TestClientIPIgnoresForwardedFromStrangers(t *testing.T) {
	r := httptest.NewRequest("POST", "/match", nil)
	r.RemoteAddr = "203.0.113.7:5000"
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Errorf("stranger's X-Forwarded-For was believed: %s", got)
	}
	r.RemoteAddr = "127.0.0.1:5000"
	if got := clientIP(r); got != "198.51.100.9" {
		t.Errorf("local reverse proxy's X-Forwarded-For ignored: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if got := clientIP(r); got != "127.0.0.1" {
		t.Errorf("got %s", got)
	}
}

func TestSanitizeClient(t *testing.T) {
	long := strings.Repeat("x", 5000)
	c := &mms.Client{PcbSerial: proto.String(long), LocId: proto.String("LOC1")}
	for i := 0; i < 100; i++ {
		c.Property = append(c.Property, &mms.Client_Property{PropertyId: proto.Uint32(1), PropertyValue: proto.String(long)})
	}
	sanitizeClient(c)
	if len(c.GetPcbSerial()) != maxStrLen || c.GetLocId() != "LOC1" {
		t.Errorf("serial %d, loc %q", len(c.GetPcbSerial()), c.GetLocId())
	}
	if len(c.Property) != maxProps || len(c.Property[0].GetPropertyValue()) != maxPropValLen {
		t.Errorf("properties not trimmed")
	}
}

func TestEntryLimits(t *testing.T) {
	m := NewMatcher(MatchConfig{RoomSize: 99, RequireSameWorld: true, WaitCode: "SUCCESS"})
	entry := func(node uint32, groups int) *mms.Response {
		em := &mms.Request_EntryMatching{Myclient: &mms.Client{NodeId: proto.Uint32(node), PcbSerial: proto.String("S"), LocId: proto.String("L")}}
		for i := 0; i < groups; i++ {
			em.Groups = append(em.Groups, &mms.Request_EntryMatching_Group{Client: &mms.Client{NodeId: proto.Uint32(node + 1)}})
		}
		return m.handle(&mms.Request{Type: mms.MethodType_ENTRY_MATCHING.Enum(), RequestId: proto.String("1"), EntryMatching: em}, "203.0.113.1")
	}
	entry(1, 50)
	if n := m.entries[1].players; n != 1+maxGroups {
		t.Errorf("entry kept %d players", n)
	}
	for i := uint32(2); i < maxEntries+10; i++ {
		entry(i, 0)
	}
	if len(m.entries) != maxEntries {
		t.Errorf("%d entries, want cap %d", len(m.entries), maxEntries)
	}
	if r := entry(1, 0); r.GetCode() != mms.ErrorCode_SUCCESS {
		t.Errorf("re-entry of a queued node refused at the cap: %v", r.GetCode())
	}
}

func TestTurnPeerAllowed(t *testing.T) {
	pub := turnPeerAllowed(net.ParseIP("149.28.115.108"))
	for ip, want := range map[string]bool{"203.0.113.5": true, "149.28.115.108": true, "26.1.2.3": true,
		"127.0.0.1": false, "169.254.169.254": false, "10.0.0.5": false, "192.168.1.10": false, "100.64.1.1": false, "224.0.0.1": false, "0.0.0.0": false} {
		if got := pub(nil, net.ParseIP(ip)); got != want {
			t.Errorf("public relay, peer %s: got %v", ip, got)
		}
	}
	lan := turnPeerAllowed(net.ParseIP("10.147.17.1"))
	if !lan(nil, net.ParseIP("10.147.17.5")) || lan(nil, net.ParseIP("127.0.0.1")) || lan(nil, net.ParseIP("169.254.169.254")) {
		t.Error("private relay rules wrong")
	}
}

func TestQueueTiedToIP(t *testing.T) {
	m := NewMatcher(MatchConfig{RoomSize: 2, RequireSameWorld: true, WaitCode: "SUCCESS"})
	req := func(typ mms.MethodType, node uint32, ip string) *mms.Response {
		r := &mms.Request{Type: typ.Enum(), RequestId: proto.String("1")}
		switch typ {
		case mms.MethodType_ENTRY_MATCHING:
			r.EntryMatching = &mms.Request_EntryMatching{Myclient: &mms.Client{NodeId: proto.Uint32(node), PcbSerial: proto.String("S"), LocId: proto.String("L")}}
		case mms.MethodType_CHECK_MATCHING:
			r.CheckMatching = &mms.Request_CheckMatching{NodeId: proto.Uint32(node)}
		case mms.MethodType_CANCEL_MATCHING:
			r.CancelMatching = &mms.Request_CancelMatching{NodeId: proto.Uint32(node)}
		}
		return m.handle(r, ip)
	}
	owner, stranger := "203.0.113.1", "198.51.100.66"
	req(mms.MethodType_ENTRY_MATCHING, 7, owner)
	if r := req(mms.MethodType_CHECK_MATCHING, 7, stranger); r.GetCode() != mms.ErrorCode_ERR_MATCH_NOENTRY {
		t.Errorf("stranger could check someone else's entry: %v", r.GetCode())
	}
	req(mms.MethodType_CANCEL_MATCHING, 7, stranger)
	if req(mms.MethodType_ENTRY_MATCHING, 7, stranger).GetCode() == mms.ErrorCode_SUCCESS {
		t.Error("stranger could take over a queued node id")
	}
	if _, ok := m.entries[7]; !ok {
		t.Fatal("stranger removed someone else's entry")
	}
	if r := req(mms.MethodType_CHECK_MATCHING, 7, owner); r.GetCode() != mms.ErrorCode_SUCCESS {
		t.Errorf("owner's check refused: %v", r.GetCode())
	}
	req(mms.MethodType_CANCEL_MATCHING, 7, owner)
	if _, ok := m.entries[7]; ok {
		t.Error("owner couldn't cancel")
	}
}
