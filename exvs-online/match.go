package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

// The game's matchmaking protocol (nue.protocol.mms), as embedded in vsac29_Release.exe:
//   ISSUE_NODE_ID   -> hand out node ids
//   ENTRY_MATCHING  -> join the queue (one entry = one cabinet line: a solo player, or a leader plus partner in groups)
//   CHECK_MATCHING  -> polled every poling_interval seconds until a room is ready
//   CANCEL_MATCHING -> leave the queue
//   MATCHING_SETTING-> per-user conditions (acknowledged, not used yet)

const protobufRevision = 6 // mms PROTOBUF_MESSAGE_REVISION

type entry struct {
	nodeID   uint32
	applyID  uint32
	world    string
	clients  []*mms.Client // myclient first, then group members
	players  int
	entered  time.Time
	lastSeen time.Time
	timeout  time.Duration
	ip       string // address that queued it; only it may check or cancel
	roomID   uint32
	room     []*mms.Response_CheckMatching_MatchedClient
	bot      bool
	team     string // Boost Link team code: entries sharing one play on the same side
}

// TeamHeader carries a player's Boost Link team code from their proxy to the match server.
const TeamHeader = "X-Boost-Link-Team"

// normTeam cleans a team code: trimmed, lower case, letters/digits/-/_ only, at most 24.
func normTeam(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if b.Len() >= 24 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type Matcher struct {
	cfg      MatchConfig
	mu       sync.Mutex
	nextNode uint32
	nextApp  uint32
	nextRoom uint32
	entries  map[uint32]*entry // by node id
	queue    []*entry          // waiting, oldest first
	recent   []roomRecord      // last rooms formed, newest last (status page)
	results  []ConnResult      // last connection reports, newest last (status page)

	applyRoom    map[uint32][]string  // apply id -> serials it was matched with
	failedPairs  map[string]time.Time // serial pair -> when it last failed to connect
	lastAnnounce time.Time            // last Discord post
	started      time.Time
	stats        *statBook // leaderboard (match and both modes)
}

type roomRecord struct {
	ID      uint32
	At      time.Time
	Players int
	Bots    bool
	ips     []string // who queued for it, so only they can send its result
}

func NewMatcher(cfg MatchConfig) *Matcher {
	m := &Matcher{cfg: cfg, nextNode: 1000, nextApp: 1, nextRoom: 1, entries: map[uint32]*entry{}, started: time.Now(), stats: newStatBook("")}
	go func() {
		for range time.Tick(5 * time.Second) {
			m.expire()
		}
	}()
	return m
}

func (m *Matcher) Handler(tl *trafficLog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxMMSBody+1))
		if err != nil || len(body) > maxMMSBody {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		req := &mms.Request{}
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(body, req); err != nil {
			tl.Line("MMS>", fmt.Sprintf("<undecodable %d bytes: %v>", len(body), err))
			http.Error(w, "bad protobuf", http.StatusBadRequest)
			return
		}
		tl.Msg("MMS>", req)
		resp := m.handleTeam(req, clientIP(r), r.Header.Get(TeamHeader))
		tl.Msg("MMS<", resp)
		b, err := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Header().Set("X-Nue-Protobuf-Revision", fmt.Sprint(protobufRevision))
		w.Write(b)
	})
}

// clientIP is the address a request came from. X-Forwarded-For is only
// believed from this machine itself (a reverse proxy in front of the server);
// from anywhere else it is a header any stranger can write.
func clientIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if rp := net.ParseIP(ip); rp == nil || !rp.IsLoopback() {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		f := strings.TrimSpace(strings.Split(xff, ",")[0])
		// The local proxy reports 127.0.0.1; only use public-looking addresses.
		if p := net.ParseIP(f); p != nil && !p.IsLoopback() {
			return f
		}
	}
	return ip
}

func (m *Matcher) handle(req *mms.Request, remoteIP string) *mms.Response {
	return m.handleTeam(req, remoteIP, "")
}

func (m *Matcher) handleTeam(req *mms.Request, remoteIP, team string) *mms.Response {
	resp := &mms.Response{Type: req.Type, RequestId: req.RequestId, Code: mms.ErrorCode_SUCCESS.Enum()}
	m.mu.Lock()
	defer m.mu.Unlock()

	switch req.GetType() {
	case mms.MethodType_PING:
		resp.Ping = &mms.Response_Ping{}

	case mms.MethodType_ISSUE_NODE_ID:
		n := uint32(1)
		if c := req.GetIssueNodeId().GetIssueCount(); c > 0 && c < 16 {
			n = c
		}
		ids := make([]uint32, n)
		for i := range ids {
			ids[i] = m.nextNode
			m.nextNode++
		}
		resp.IssueNodeId = &mms.Response_IssueNodeId{NodeId: ids}

	case mms.MethodType_ENTRY_MATCHING:
		em := req.GetEntryMatching()
		if em == nil || em.Myclient == nil {
			resp.Code = mms.ErrorCode_ERR_REQUEST.Enum()
			break
		}
		node := em.Myclient.GetNodeId()
		if old, ok := m.entries[node]; ok {
			if !old.ownedBy(remoteIP) {
				resp.Code = mms.ErrorCode_ERR_REQUEST.Enum()
				break
			}
			m.removeLocked(old)
		} else if len(m.entries) >= maxEntries {
			resp.Code = mms.ErrorCode_ERR_REQUEST.Enum()
			break
		}
		applyID := em.GetApplyId()
		if applyID == 0 {
			applyID = m.nextApp
			m.nextApp++
		}
		e := &entry{nodeID: node, applyID: applyID, world: worldKey(em.World), ip: remoteIP,
			entered: time.Now(), lastSeen: time.Now(), team: normTeam(team),
			timeout: time.Duration(em.GetMatchingTimeout()) * time.Second}
		if e.timeout <= 0 || e.timeout > 10*time.Minute {
			e.timeout = 2 * time.Minute
		}
		me := proto.Clone(em.Myclient).(*mms.Client)
		sanitizeClient(me)
		if me.GetGlobalIpaddress() == "" && remoteIP != "" {
			me.GlobalIpaddress = proto.String(remoteIP)
		}
		e.clients = append(e.clients, me)
		for _, g := range em.Groups {
			if g.Client != nil && len(e.clients) <= maxGroups {
				gc := proto.Clone(g.Client).(*mms.Client)
				sanitizeClient(gc)
				e.clients = append(e.clients, gc)
			}
		}
		e.players = len(e.clients)
		realBefore := m.realQueuedLocked()
		m.entries[node] = e
		m.queue = append(m.queue, e)
		m.announceLocked(realBefore)
		m.addBotsLocked(e)
		teamNote := ""
		if e.team != "" {
			teamNote = " with a team code"
		}
		log.Printf("entry: node %d apply %d players %d%s from %s (queue %d)", node, applyID, e.players, teamNote, remoteIP, len(m.queue))
		m.tryMatchLocked()
		resp.EntryMatching = &mms.Response_EntryMatching{
			PolingInterval: proto.Uint32(uint32(mms.SystemConstant_PROCESS_WAIT_POLING_INTERVAL)),
			ApplyId:        proto.Uint32(applyID),
		}

	case mms.MethodType_CHECK_MATCHING:
		node := req.GetCheckMatching().GetNodeId()
		e, ok := m.entries[node]
		if !ok || !e.ownedBy(remoteIP) {
			resp.Code = mms.ErrorCode_ERR_MATCH_NOENTRY.Enum()
			break
		}
		e.lastSeen = time.Now()
		m.tryMatchLocked()
		if e.roomID == 0 {
			if m.cfg.WaitCode != "SUCCESS" {
				resp.Code = mms.ErrorCode_PROCESS_WAIT.Enum()
			}
			resp.CheckMatching = &mms.Response_CheckMatching{ApplyId: proto.Uint32(e.applyID)}
			break
		}
		resp.CheckMatching = &mms.Response_CheckMatching{
			RoomId: proto.Uint32(e.roomID), ApplyId: proto.Uint32(e.applyID), Clients: e.room,
		}

	case mms.MethodType_CANCEL_MATCHING:
		if e, ok := m.entries[req.GetCancelMatching().GetNodeId()]; ok && e.ownedBy(remoteIP) {
			m.removeLocked(e)
			log.Printf("cancel: node %d", e.nodeID)
		}
		resp.CancelMatching = &mms.Response_CancelMatching{}

	case mms.MethodType_MATCHING_SETTING:
		resp.MatchingSetting = &mms.Response_MatchingSetting{}

	default:
		resp.Code = mms.ErrorCode_ERR_REQUEST.Enum()
	}
	return resp
}

// addBotsLocked queues fake players next to a real entry so one PC can get past matching.
// The game will then fail the player-to-player step (nobody answers), which still shows
// whether it accepted our CHECK_MATCHING reply.
func (m *Matcher) addBotsLocked(real *entry) {
	if m.cfg.TestBots <= 0 || real.bot {
		return
	}
	for i := 0; i < m.cfg.TestBots; i++ {
		c := proto.Clone(real.clients[0]).(*mms.Client)
		id := m.nextNode
		m.nextNode++
		c.NodeId = proto.Uint32(id)
		c.PcbSerial = proto.String(fmt.Sprintf("BOT%05d", id))
		c.LocId = proto.String(fmt.Sprintf("BOTLOC%d", id))
		// Unreachable documentation addresses, so the game can't accidentally talk to itself.
		c.GlobalIpaddress = proto.String("192.0.2.10")
		c.LocalIpaddress = proto.String("192.0.2.10")
		b := &entry{nodeID: id, applyID: m.nextApp, world: real.world, clients: []*mms.Client{c}, players: 1,
			entered: time.Now(), lastSeen: time.Now(), timeout: real.timeout, bot: true}
		m.nextApp++
		m.entries[id] = b
		m.queue = append(m.queue, b)
	}
	log.Printf("added %d test bots for node %d", m.cfg.TestBots, real.nodeID)
}

// ownedBy reports whether a request from ip may act on this entry. Strangers
// can't guess a node id to cancel someone's search or read who they matched.
func (e *entry) ownedBy(ip string) bool {
	return e.bot || e.ip == "" || e.ip == ip
}

func worldKey(w *mms.World) string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d/%d", w.GetMuchaGameId(), w.GetVersion(), w.GetWorldId())
}

// tryMatchLocked fills rooms from the queue, oldest entries first. Units stay whole, so a
// pair from one entry, or two players sharing a team code, always land on the same team.
func (m *Matcher) tryMatchLocked() {
	for {
		room := m.pickLocked(m.cfg.RoomSize)
		if room == nil && m.cfg.FallbackAfterSeconds > 0 && len(m.queue) > 0 &&
			time.Since(m.queue[0].entered) > time.Duration(m.cfg.FallbackAfterSeconds)*time.Second {
			room = m.pickLocked(m.cfg.FallbackRoomSize)
		}
		if room == nil {
			return
		}
		m.makeRoomLocked(room)
	}
}

// unit is one or more queued entries that must play on the same side.
type unit struct {
	es      []*entry
	players int
}

// unitsLocked groups the queue into units, in queue order. Entries with the same team code
// (and the same game world) form one unit; a team code nobody else is using yet is a solo.
func (m *Matcher) unitsLocked() []*unit {
	var us []*unit
	byTeam := map[string]*unit{}
	for _, e := range m.queue {
		key := ""
		if e.team != "" {
			key = e.world + "|" + e.team
		}
		if u, ok := byTeam[key]; ok && key != "" && u.players+e.players <= 2 {
			u.es = append(u.es, e)
			u.players += e.players
			continue
		}
		u := &unit{es: []*entry{e}, players: e.players}
		if key != "" {
			byTeam[key] = u
		}
		us = append(us, u)
	}
	return us
}

// pickLocked chooses units whose player counts add up to exactly size and can be split
// into two equal teams without breaking a unit up.
func (m *Matcher) pickLocked(size int) []*unit {
	if size <= 0 || len(m.queue) == 0 {
		return nil
	}
	// Prefer opponents this player hasn't just failed to reach. If nobody else
	// fits, retry the failed pair right away: a small scene can't afford to wait.
	if pick := m.pickWithLocked(size, true); pick != nil {
		return pick
	}
	return m.pickWithLocked(size, false)
}

func flatten(us []*unit) []*entry {
	var es []*entry
	for _, u := range us {
		es = append(es, u.es...)
	}
	return es
}

func (m *Matcher) pickWithLocked(size int, avoidFailed bool) []*unit {
	head := m.queue[0]
	var pick []*unit
	total := 0
	for _, u := range m.unitsLocked() {
		if m.cfg.RequireSameWorld && u.es[0].world != head.world {
			continue
		}
		if total+u.players > size {
			continue
		}
		// A team code means "2v2 only": never use it to fill a smaller fallback room.
		if size < m.cfg.RoomSize && u.es[0].team != "" {
			continue
		}
		if avoidFailed && len(pick) > 0 && time.Since(head.entered) < m.widenAfter() && m.unitFailedLocked(pick, u) {
			continue
		}
		pick = append(pick, u)
		total += u.players
		if total == size {
			if _, ok := splitTeams(pick, size/2); ok {
				return pick
			}
			pick, total = pick[:len(pick)-1], total-u.players
		}
	}
	return nil
}

func (m *Matcher) unitFailedLocked(pick []*unit, u *unit) bool {
	es := flatten(pick)
	for _, e := range u.es {
		if m.recentlyFailedLocked(es, e) {
			return true
		}
	}
	return false
}

// splitTeams assigns each unit to team 0 or 1 so both teams have half players.
func splitTeams(us []*unit, half int) ([]int, bool) {
	n := len(us)
	for mask := 0; mask < 1<<n; mask++ {
		c := 0
		for i, u := range us {
			if mask&(1<<i) != 0 {
				c += u.players
			}
		}
		if c == half {
			t := make([]int, n)
			for i := range us {
				if mask&(1<<i) != 0 {
					t[i] = 1
				}
			}
			return t, true
		}
	}
	return nil, false
}

func (m *Matcher) makeRoomLocked(us []*unit) {
	total := 0
	for _, u := range us {
		total += u.players
	}
	uteams, _ := splitTeams(us, total/2)
	var es []*entry
	var teams []int
	for i, u := range us {
		for _, e := range u.es {
			es = append(es, e)
			teams = append(teams, uteams[i])
		}
	}
	roomID := m.nextRoom
	m.nextRoom++
	var all []*mms.Response_CheckMatching_MatchedClient
	for i, e := range es {
		for _, c := range e.clients {
			all = append(all, &mms.Response_CheckMatching_MatchedClient{
				GroupId: proto.Uint32(m.cfg.GroupIDBase + uint32(teams[i])),
				Client:  c,
			})
		}
	}
	var nodes []string
	for _, e := range es {
		e.roomID, e.room = roomID, all
		nodes = append(nodes, fmt.Sprint(e.nodeID))
		m.dequeueLocked(e)
	}
	m.recordRoomLocked(es)
	rec := roomRecord{ID: roomID, At: time.Now(), Players: total}
	for _, e := range es {
		rec.Bots = rec.Bots || e.bot
		if e.ip != "" {
			rec.ips = append(rec.ips, e.ip)
		}
	}
	m.recent = append(m.recent, rec)
	if len(m.recent) > 20 {
		m.recent = m.recent[len(m.recent)-20:]
	}
	log.Printf("room %d formed: nodes %s", roomID, strings.Join(nodes, ","))
}

func (m *Matcher) dequeueLocked(e *entry) {
	for i, q := range m.queue {
		if q == e {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

func (m *Matcher) removeLocked(e *entry) {
	m.dequeueLocked(e)
	delete(m.entries, e.nodeID)
}

func (m *Matcher) expire() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, e := range m.entries {
		waiting := e.roomID == 0 && now.Sub(e.entered) > e.timeout
		stale := !e.bot && now.Sub(e.lastSeen) > 2*time.Minute
		if waiting || stale {
			m.removeLocked(e)
		}
	}
}
