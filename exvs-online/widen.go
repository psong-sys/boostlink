package main

// Matchmaking for a small scene: remember pairs that just failed to connect and try someone
// else first, share the host's ping limit with every player, and ping Discord when someone
// starts searching.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"exvsonline/pb/exvs"

	"google.golang.org/protobuf/proto"
)

const failedPairMemory = 10 * time.Minute

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// entrySerials names the cabinets in an entry for failed-pair memory. The serial is
// paired with the address that queued it, because players who unzipped the same game
// copy can share a serial and must not be mistaken for each other.
func entrySerials(e *entry) []string {
	var s []string
	for _, c := range e.clients {
		s = append(s, c.GetPcbSerial()+"@"+e.ip)
	}
	return s
}

// recordRoomLocked remembers which serials each apply id was matched with, so a failed
// connection report (which carries apply_id) can be tied back to the pair.
func (m *Matcher) recordRoomLocked(es []*entry) {
	if m.applyRoom == nil {
		m.applyRoom = map[uint32][]string{}
	}
	var all []string
	for _, e := range es {
		all = append(all, entrySerials(e)...)
	}
	for _, e := range es {
		m.applyRoom[e.applyID] = all
	}
	if len(m.applyRoom) > 200 { // keep it small; old ids are useless
		for k := range m.applyRoom {
			delete(m.applyRoom, k)
			if len(m.applyRoom) <= 100 {
				break
			}
		}
	}
}

func failedResult(r string) bool {
	switch r {
	case "FAIL_TIMEOUT", "FAIL_TIMEOUT_NO_PACKETS", "FAIL_RTT_OUT_OF_LIMIT", "FAIL_OTHERS":
		return true
	}
	return false
}

// noteResultLocked marks every pair in the reporter's room as failed after a connection failure.
func (m *Matcher) noteResultLocked(r ConnResult) {
	if !failedResult(r.Result) || r.ApplyID == 0 {
		return
	}
	serials := m.applyRoom[r.ApplyID]
	if len(serials) < 2 {
		return
	}
	if m.failedPairs == nil {
		m.failedPairs = map[string]time.Time{}
	}
	for i := range serials {
		for j := i + 1; j < len(serials); j++ {
			m.failedPairs[pairKey(serials[i], serials[j])] = time.Now()
		}
	}
	log.Printf("pairing: room for apply %d failed (%s); trying other opponents first for %v", r.ApplyID, r.Result, failedPairMemory)
}

// recentlyFailedLocked says whether e can't connect to anyone already picked.
func (m *Matcher) recentlyFailedLocked(pick []*entry, e *entry) bool {
	if len(m.failedPairs) == 0 {
		return false
	}
	for _, p := range pick {
		for _, a := range entrySerials(p) {
			for _, b := range entrySerials(e) {
				if t, ok := m.failedPairs[pairKey(a, b)]; ok && time.Since(t) < failedPairMemory {
					return true
				}
			}
		}
	}
	return false
}

// widenAfter is how long the oldest player waits before pairs that failed are allowed again
// (if they're the only ones searching, retrying beats waiting forever).
func (m *Matcher) widenAfter() time.Duration {
	if m.cfg.RetryFailedPairAfterSeconds > 0 {
		return time.Duration(m.cfg.RetryFailedPairAfterSeconds) * time.Second
	}
	return 45 * time.Second
}

// ---- Shared ping limit -------------------------------------------------------------------

// MatchSettings is what the host tells every player's proxy (GET /settings).
type MatchSettings struct {
	RTTLimitMs      uint32 `json:"rttLimitMs,omitempty"`      // 0 = leave the card server's value (300)
	DelayFrameLimit uint32 `json:"delayFrameLimit,omitempty"` // 0 = leave the card server's value (5)
}

func (m *Matcher) Settings() MatchSettings {
	return MatchSettings{RTTLimitMs: m.cfg.RTTLimitMs, DelayFrameLimit: m.cfg.DelayFrameLimit}
}

func (m *Matcher) SettingsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m.Settings())
	})
}

type remoteSettings struct {
	mu      sync.Mutex
	s       MatchSettings
	fetched time.Time
}

func (rs *remoteSettings) get(c *http.Client, base string) MatchSettings {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if !rs.fetched.IsZero() && time.Since(rs.fetched) < 2*time.Minute {
		return rs.s
	}
	rs.fetched = time.Now()
	resp, err := c.Get(base + "/settings")
	if err != nil {
		return rs.s
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		var s MatchSettings
		if json.NewDecoder(resp.Body).Decode(&s) == nil {
			rs.s = s
		}
	}
	return rs.s
}

func (p *Proxy) matchSettings() MatchSettings {
	if p.local != nil {
		return p.local.Settings()
	}
	return p.rset.get(p.client, strings.TrimRight(p.match.String(), "/"))
}

// currentRTTLimit is what the game was last told (for the RESULT lines).
var currentRTTLimit = struct {
	sync.Mutex
	ms uint32
}{ms: 300}

func rttLimitNow() uint32 {
	currentRTTLimit.Lock()
	defer currentRTTLimit.Unlock()
	return currentRTTLimit.ms
}

// patchBlackList applies the host's ping limits to the LOAD_BLACK_LIST reply. Returns true if changed.
func (p *Proxy) patchBlackList(bl *exvs.Response_LoadBlackList) bool {
	s := p.matchSettings()
	orig := bl.GetThresholdDelayedRtt()
	changed := false
	if s.RTTLimitMs > 0 {
		bl.ThresholdDelayedRtt = proto.Uint32(s.RTTLimitMs)
		// The two delay conditions ship equal to the RTT threshold; keep them in step.
		if bl.DelayRestrictCondition == nil || bl.GetDelayRestrictCondition() == orig {
			bl.DelayRestrictCondition = proto.Uint32(s.RTTLimitMs)
		}
		if bl.DelayPermitCondition == nil || bl.GetDelayPermitCondition() == orig {
			bl.DelayPermitCondition = proto.Uint32(s.RTTLimitMs)
		}
		changed = true
	}
	if s.DelayFrameLimit > 0 {
		bl.ThresholdDelayedFrame = proto.Uint32(s.DelayFrameLimit)
		changed = true
	}
	if v := bl.GetThresholdDelayedRtt(); v > 0 {
		currentRTTLimit.Lock()
		currentRTTLimit.ms = v
		currentRTTLimit.Unlock()
	}
	return changed
}

// ---- Discord -------------------------------------------------------------------------

// announceLocked posts to the Discord webhook when the queue goes from nobody to somebody,
// at most once per cooldown. No names, serials or IPs are sent.
func (m *Matcher) announceLocked(realBefore int) {
	if m.cfg.DiscordWebhook == "" || realBefore > 0 {
		return
	}
	cd := time.Duration(m.cfg.DiscordCooldownMinutes) * time.Minute
	if cd <= 0 {
		cd = 10 * time.Minute
	}
	if time.Since(m.lastAnnounce) < cd {
		return
	}
	m.lastAnnounce = time.Now()
	msg := "Someone just started searching for a match on Boost Link. Jump in now to play them!"
	if m.cfg.PublicStatusURL != "" {
		msg += " Live queue: " + m.cfg.PublicStatusURL
	}
	go postDiscord(m.cfg.DiscordWebhook, msg)
}

var discordClient = &http.Client{Timeout: 10 * time.Second}

func postDiscord(webhook, msg string) {
	b, _ := json.Marshal(map[string]any{"username": "Boost Link", "content": msg,
		"allowed_mentions": map[string]any{"parse": []string{}}})
	resp, err := discordClient.Post(webhook, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("discord: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("discord: HTTP %d (check the webhook link)", resp.StatusCode)
	}
}

func (m *Matcher) realQueuedLocked() int {
	n := 0
	for _, e := range m.queue {
		if !e.bot {
			n += e.players
		}
	}
	return n
}

// for tests
func (m *Matcher) failedPairList() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.failedPairs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = fmt.Sprint
