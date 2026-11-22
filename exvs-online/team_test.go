package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

func queueTeam(m *Matcher, node uint32, serial, team string) {
	m.handleTeam(&mms.Request{Type: mms.MethodType_ENTRY_MATCHING.Enum(), RequestId: proto.String("e"),
		EntryMatching: &mms.Request_EntryMatching{Myclient: &mms.Client{NodeId: proto.Uint32(node), PcbSerial: proto.String(serial),
			LocId: proto.String("L"), PrefCode: proto.Uint32(1)}}}, "203.0.113.1", team)
}

func teamMatcher() *Matcher {
	cfg := defaultConfig().Match
	cfg.RoomSize, cfg.FallbackRoomSize, cfg.FallbackAfterSeconds = 4, 2, 20
	return NewMatcher(cfg)
}

func groupOf(m *Matcher, node uint32) uint32 {
	e := m.entries[node]
	for _, c := range e.room {
		if c.Client.GetNodeId() == node {
			return c.GetGroupId()
		}
	}
	return 99
}

// ageQueue makes everyone look like they've waited past the fallback, then retries.
func ageQueue(m *Matcher) {
	for _, e := range m.queue {
		e.entered = e.entered.Add(-time.Minute)
	}
	m.tryMatchLocked()
}

func TestTeamCodeSameSide(t *testing.T) {
	m := teamMatcher()
	queueTeam(m, 1, "PAUL", "Paul-Duo")
	queueTeam(m, 2, "SOLO1", "")
	queueTeam(m, 3, "SOLO2", "")
	if m.entries[1].roomID != 0 {
		t.Fatal("room formed with only 3 players")
	}
	queueTeam(m, 4, "FRIEND", " paul-duo ")
	for _, n := range []uint32{1, 2, 3, 4} {
		if m.entries[n].roomID == 0 || m.entries[n].roomID != m.entries[1].roomID {
			t.Fatalf("node %d not in the 2v2 room", n)
		}
	}
	if groupOf(m, 1) != groupOf(m, 4) || groupOf(m, 2) != groupOf(m, 3) || groupOf(m, 1) == groupOf(m, 2) {
		t.Fatalf("teams wrong: %d %d %d %d", groupOf(m, 1), groupOf(m, 2), groupOf(m, 3), groupOf(m, 4))
	}
	if len(m.entries[1].room) != 4 {
		t.Fatalf("room has %d clients", len(m.entries[1].room))
	}
}

func TestTwoTeams(t *testing.T) {
	m := teamMatcher()
	queueTeam(m, 1, "A1", "red")
	queueTeam(m, 2, "B1", "blue")
	queueTeam(m, 3, "B2", "blue")
	queueTeam(m, 4, "A2", "red")
	if m.entries[1].roomID == 0 || groupOf(m, 1) != groupOf(m, 4) || groupOf(m, 2) != groupOf(m, 3) || groupOf(m, 1) == groupOf(m, 2) {
		t.Fatal("red vs blue not formed correctly")
	}
}

func TestTeamNeverFacesEachOther(t *testing.T) {
	m := teamMatcher()
	queueTeam(m, 1, "PAUL", "duo")
	queueTeam(m, 2, "FRIEND", "duo")
	ageQueue(m)
	if m.entries[1].roomID != 0 {
		t.Fatal("teammates were put against each other")
	}
}

func TestTeamCodeSkipsOneVsOne(t *testing.T) {
	m := teamMatcher()
	queueTeam(m, 1, "PAUL", "duo")
	queueTeam(m, 2, "SOLO", "")
	ageQueue(m)
	if m.entries[1].roomID != 0 {
		t.Fatal("team-code player got a 1v1")
	}
}

func TestSolosStillGetOneVsOne(t *testing.T) {
	m := teamMatcher()
	queueTeam(m, 1, "S1", "")
	queueTeam(m, 2, "S2", "")
	if m.entries[1].roomID != 0 {
		t.Fatal("1v1 should wait for the fallback")
	}
	ageQueue(m)
	if m.entries[1].roomID == 0 || m.entries[1].roomID != m.entries[2].roomID {
		t.Fatal("solos should get a 1v1 after the wait")
	}
}

func TestNormTeam(t *testing.T) {
	if got := normTeam("  Paul & Friend!! 2v2  "); got != "paulfriend2v2" {
		t.Fatal(got)
	}
	if got := normTeam("abcdefghijklmnopqrstuvwxyz0123"); len(got) != 24 {
		t.Fatal(got)
	}
}

func TestProxySendsOwnTeamCode(t *testing.T) {
	var got []string
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(TeamHeader))
	}))
	defer ms.Close()
	for _, code := range []string{"Duo-1", ""} {
		cfg := defaultConfig().Proxy
		cfg.MatchServer, cfg.TeamCode = ms.URL, code
		p, err := NewProxy(cfg, nil, &trafficLog{})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/match", bytes.NewReader([]byte{8, 1}))
		req.Header.Set(TeamHeader, "spoofed")
		p.ServeHTTP(httptest.NewRecorder(), req)
	}
	if len(got) != 2 || got[0] != "duo-1" || got[1] != "" {
		t.Fatalf("headers %q", got)
	}
}
