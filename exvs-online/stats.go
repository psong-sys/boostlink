package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"exvsonline/pb/exvs"
)

// MatchReport is what a player's proxy sends the match server after each online
// match (POST /result). It comes from the game's SAVE_VSM_ON_RESULT and holds the
// pilot name the game shows, never IPs, card ids or serials.
type MatchReport struct {
	Name    string `json:"name"`
	Win     bool   `json:"win"`
	Room    uint32 `json:"room"`
	Given   uint32 `json:"given"`   // damage dealt
	Taken   uint32 `json:"taken"`   // damage taken
	Perfect bool   `json:"perfect"` // perfect or no-damage win
	Seconds uint32 `json:"seconds"` // match length
}

// PilotStats is one leaderboard row.
type PilotStats struct {
	Name     string    `json:"name"`
	Wins     int       `json:"wins"`
	Losses   int       `json:"losses"`
	Streak   int       `json:"streak"`     // current win streak
	Best     int       `json:"bestStreak"` // longest win streak
	Given    uint64    `json:"given"`      // total damage dealt
	Perfects int       `json:"perfects"`
	Last     time.Time `json:"lastPlayed"`
}

func (s PilotStats) Games() int { return s.Wins + s.Losses }

func (s PilotStats) WinPct() int {
	if s.Games() == 0 {
		return 0
	}
	return (s.Wins*100 + s.Games()/2) / s.Games()
}

func (s PilotStats) AvgDamage() uint64 {
	if s.Games() == 0 {
		return 0
	}
	return s.Given / uint64(s.Games())
}

// Fight is one finished match as the players reported it.
type Fight struct {
	Room    uint32    `json:"room"`
	At      time.Time `json:"at"`
	Winners []string  `json:"winners"`
	Losers  []string  `json:"losers"`
}

// statBook is the match server's record of results, saved to a JSON file so it
// survives restarts.
type statBook struct {
	mu     sync.Mutex
	path   string
	Pilots map[string]*PilotStats `json:"pilots"` // by lower-case name
	Fights []Fight                `json:"fights"` // newest last
	Seen   map[string]time.Time   `json:"seen"`   // room|name already counted
}

const (
	maxPilots = 500
	maxFights = 30
)

func newStatBook(path string) *statBook {
	b := &statBook{path: path, Pilots: map[string]*PilotStats{}, Seen: map[string]time.Time{}}
	if path == "" {
		return b
	}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, b); err != nil {
			log.Printf("stats file %s unreadable, starting fresh: %v", path, err)
		}
		if b.Pilots == nil {
			b.Pilots = map[string]*PilotStats{}
		}
		if b.Seen == nil {
			b.Seen = map[string]time.Time{}
		}
	}
	return b
}

// cleanName keeps a pilot name printable and short.
func cleanName(s string) string {
	var out []rune
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			continue
		}
		out = append(out, r)
		if len(out) == 16 {
			break
		}
	}
	return strings.TrimSpace(string(out))
}

// add counts one report. It returns false for a repeat or a report without a name.
func (b *statBook) add(r MatchReport, now time.Time) bool {
	r.Name = cleanName(r.Name)
	if r.Name == "" {
		return false
	}
	key := strings.ToLower(r.Name)
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, t := range b.Seen {
		if now.Sub(t) > 24*time.Hour {
			delete(b.Seen, k)
		}
	}
	seenKey := u32(r.Room) + "|" + key
	if r.Room != 0 {
		if _, dup := b.Seen[seenKey]; dup {
			return false
		}
		b.Seen[seenKey] = now
	}
	p := b.Pilots[key]
	if p == nil {
		if len(b.Pilots) >= maxPilots {
			return false
		}
		p = &PilotStats{}
		b.Pilots[key] = p
	}
	p.Name, p.Last = r.Name, now
	if r.Win {
		p.Wins++
		p.Streak++
		if p.Streak > p.Best {
			p.Best = p.Streak
		}
		if r.Perfect {
			p.Perfects++
		}
	} else {
		p.Losses++
		p.Streak = 0
	}
	p.Given += uint64(r.Given)

	// Both sides report the same room; join them into one fight.
	var f *Fight
	for i := len(b.Fights) - 1; i >= 0 && r.Room != 0; i-- {
		if b.Fights[i].Room == r.Room && now.Sub(b.Fights[i].At) < 30*time.Minute {
			f = &b.Fights[i]
			break
		}
	}
	if f == nil {
		b.Fights = append(b.Fights, Fight{Room: r.Room, At: now})
		if len(b.Fights) > maxFights {
			b.Fights = b.Fights[len(b.Fights)-maxFights:]
		}
		f = &b.Fights[len(b.Fights)-1]
	}
	if r.Win {
		f.Winners = append(f.Winners, r.Name)
	} else {
		f.Losers = append(f.Losers, r.Name)
	}
	b.saveLocked()
	return true
}

func u32(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

func (b *statBook) saveLocked() {
	if b.path == "" {
		return
	}
	raw, err := json.MarshalIndent(b, "", " ")
	if err != nil {
		return
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		log.Printf("save stats: %v", err)
		return
	}
	if err := os.Rename(tmp, b.path); err != nil {
		log.Printf("save stats: %v", err)
	}
}

// board is the leaderboard (most wins first) and the recent fights (newest first).
func (b *statBook) board(n int) ([]PilotStats, []Fight) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ps := make([]PilotStats, 0, len(b.Pilots))
	for _, p := range b.Pilots {
		ps = append(ps, *p)
	}
	sort.Slice(ps, func(i, j int) bool {
		a, c := ps[i], ps[j]
		if a.Wins != c.Wins {
			return a.Wins > c.Wins
		}
		if a.WinPct() != c.WinPct() {
			return a.WinPct() > c.WinPct()
		}
		return strings.ToLower(a.Name) < strings.ToLower(c.Name)
	})
	if len(ps) > n {
		ps = ps[:n]
	}
	fs := make([]Fight, 0, len(b.Fights))
	for i := len(b.Fights) - 1; i >= 0 && len(fs) < 10; i-- {
		f := b.Fights[i]
		f.Winners = append([]string{}, f.Winners...)
		f.Losers = append([]string{}, f.Losers...)
		fs = append(fs, f)
	}
	return ps, fs
}

// playedRecently reports whether ip queued for a real (non-test) room in the
// last two hours. Only those players may send results, so strangers can't
// write to the leaderboard.
func (m *Matcher) playedRecently(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.recent {
		if r.Bots || time.Since(r.At) > 2*time.Hour {
			continue
		}
		for _, x := range r.ips {
			if x == ip {
				return true
			}
		}
	}
	return false
}

// ResultHandler takes a MatchReport from a player's proxy (POST /result).
func (m *Matcher) ResultHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var mr MatchReport
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&mr); err != nil {
			http.Error(w, "bad result", http.StatusBadRequest)
			return
		}
		if !m.playedRecently(clientIP(r)) {
			http.Error(w, "no recent match from this address", http.StatusForbidden)
			return
		}
		if m.stats.add(mr, time.Now()) {
			log.Printf("result: %s %s (room %d)", cleanName(mr.Name), map[bool]string{true: "won", false: "lost"}[mr.Win], mr.Room)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// matchReport turns the game's result into what the leaderboard needs.
func matchReport(name string, s *exvs.Request_SaveVsmOnResult) (MatchReport, bool) {
	res := s.GetResult()
	if res == nil {
		return MatchReport{}, false
	}
	return MatchReport{
		Name:    name,
		Win:     res.GetWinFlag(),
		Room:    res.GetRoomId(),
		Given:   res.GetGivenDamage(),
		Taken:   res.GetTakenDamage(),
		Perfect: res.GetWinFlag() && (res.GetPerfectWinFlag() || res.GetNoDamageFlag()),
		Seconds: res.GetVsElapsedTime(),
	}, true
}

// reportResult logs the match result and sends it to the match server's leaderboard.
func (p *Proxy) reportResult(s *exvs.Request_SaveVsmOnResult) {
	p.mu.Lock()
	name := p.pilotName
	p.mu.Unlock()
	if p.cfg.StatsName != "" {
		name = p.cfg.StatsName
	}
	mr, ok := matchReport(name, s)
	if !ok {
		return
	}
	verdict := "LOSS"
	if mr.Win {
		verdict = "WIN"
	}
	p.tl.Line("MATCH", verdict+" as "+cleanName(mr.Name)+", damage dealt "+u32(mr.Given)+", taken "+u32(mr.Taken))
	if p.cfg.NoShareStats || cleanName(mr.Name) == "" {
		return
	}
	if p.local != nil {
		p.local.stats.add(mr, time.Now())
		return
	}
	if p.match == nil {
		return
	}
	go func() {
		b, _ := json.Marshal(mr)
		u := *p.match
		u.Path = "/result"
		resp, err := p.client.Post(u.String(), "application/json", bytes.NewReader(b))
		if err != nil {
			return // older server without /result, or server down
		}
		resp.Body.Close()
	}()
}
