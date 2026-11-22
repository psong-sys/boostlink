package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatBook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	b := newStatBook(path)
	now := time.Now()
	if !b.add(MatchReport{Name: "Paul", Win: true, Room: 7, Given: 900}, now) {
		t.Fatal("first report not counted")
	}
	if b.add(MatchReport{Name: "paul", Win: true, Room: 7}, now) {
		t.Fatal("repeat report for the same room counted twice")
	}
	b.add(MatchReport{Name: "Friend", Win: false, Room: 7, Given: 300}, now)
	b.add(MatchReport{Name: "Paul", Win: true, Room: 8, Perfect: true, Given: 1100}, now)
	b.add(MatchReport{Name: "Paul", Win: false, Room: 9}, now)
	b.add(MatchReport{Name: "  ", Win: true, Room: 10}, now)

	b2 := newStatBook(path) // reload from disk
	ps, fs := b2.board(20)
	if len(ps) != 2 || ps[0].Name != "Paul" {
		t.Fatalf("leaderboard = %+v", ps)
	}
	p := ps[0]
	if p.Wins != 2 || p.Losses != 1 || p.Best != 2 || p.Streak != 0 || p.Perfects != 1 || p.WinPct() != 67 || p.AvgDamage() != 666 {
		t.Fatalf("Paul = %+v", p)
	}
	if len(fs) != 3 || fs[2].Room != 7 || len(fs[2].Winners) != 1 || fs[2].Losers[0] != "Friend" {
		t.Fatalf("fights = %+v", fs)
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName(" ab\x00c\n "); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := cleanName(strings.Repeat("ア", 30)); len([]rune(got)) != 16 {
		t.Fatalf("not clipped: %q", got)
	}
}

func TestResultHandlerNeedsRecentRoom(t *testing.T) {
	m := NewMatcher(MatchConfig{RoomSize: 2})
	post := func() int {
		r := httptest.NewRequest(http.MethodPost, "/result", bytes.NewBufferString(`{"name":"Paul","win":true,"room":1}`))
		r.RemoteAddr = "203.0.113.5:4000"
		w := httptest.NewRecorder()
		m.ResultHandler().ServeHTTP(w, r)
		return w.Code
	}
	if c := post(); c != http.StatusForbidden {
		t.Fatalf("stranger got %d", c)
	}
	m.mu.Lock()
	m.recent = append(m.recent, roomRecord{ID: 1, At: time.Now(), Players: 2, ips: []string{"203.0.113.5"}})
	m.mu.Unlock()
	if c := post(); c != http.StatusNoContent {
		t.Fatalf("player got %d", c)
	}
	w := httptest.NewRecorder()
	m.StatusHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if !strings.Contains(w.Body.String(), ">Paul<") || !strings.Contains(w.Body.String(), "Leaderboard") {
		t.Fatal("status page has no leaderboard row")
	}
}
