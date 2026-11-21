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
	roomID   uint32
	room     []*mms.Response_CheckMatching_MatchedClient
	bot      bool
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
}

type roomRecord struct {
	ID      uint32
	At      time.Time
	Players int
	Bots    bool
}

func NewMatcher(cfg MatchConfig) *Matcher {
	m := &Matcher{cfg: cfg, nextNode: 1000, nextApp: 1, nextRoom: 1, entries: map[uint32]*entry{}, started: time.Now()}
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
		body, _ := io.ReadAll(r.Body)
		req := &mms.Request{}
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(body, req); err != nil {
			tl.Line("MMS>", fmt.Sprintf("<undecodable %d bytes: %v>", len(body), err))
			http.Error(w, "bad protobuf", http.StatusBadRequest)
			return
		}
		tl.Msg("MMS>", req)
		resp := m.handle(req, clientIP(r))
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

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := strings.TrimSpace(strings.Split(xff, ",")[0])
		// The local proxy reports 127.0.0.1; only trust public-looking addresses from it.
		if p := net.ParseIP(ip); p != nil && !p.IsLoopback() {
			return ip
		}
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func (m *Matcher) handle(req *mms.Request, remoteIP string) *mms.Response {
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
			m.removeLocked(old)
		}
		applyID := em.GetApplyId()
		if applyID == 0 {
			applyID = m.nextApp
			m.nextApp++
		}
		e := &entry{nodeID: node, applyID: applyID, world: worldKey(em.World),
			entered: time.Now(), lastSeen: time.Now(),
			timeout: time.Duration(em.GetMatchingTimeout()) * time.Second}
		if e.timeout <= 0 || e.timeout > 10*time.Minute {
			e.timeout = 2 * time.Minute
		}
		me := proto.Clone(em.Myclient).(*mms.Client)
		if me.GetGlobalIpaddress() == "" && remoteIP != "" {
			me.GlobalIpaddress = proto.String(remoteIP)
		}
		e.clients = append(e.clients, me)
		for _, g := range em.Groups {
			if g.Client != nil {
				e.clients = append(e.clients, g.Client)
			}
		}
		e.players = len(e.clients)
		realBefore := m.realQueuedLocked()
		m.entries[node] = e
		m.queue = append(m.queue, e)
		m.announceLocked(realBefore)
		m.addBotsLocked(e)
		log.Printf("entry: node %d apply %d players %d from %s (queue %d)", node, applyID, e.players, remoteIP, len(m.queue))
		m.tryMatchLocked()
		resp.EntryMatching = &mms.Response_EntryMatching{
			PolingInterval: proto.Uint32(uint32(mms.SystemConstant_PROCESS_WAIT_POLING_INTERVAL)),
			ApplyId:        proto.Uint32(applyID),
		}

	case mms.MethodType_CHECK_MATCHING:
		node := req.GetCheckMatching().GetNodeId()
		e, ok := m.entries[node]
		if !ok {
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
		if e, ok := m.entries[req.GetCancelMatching().GetNodeId()]; ok {
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

func worldKey(w *mms.World) string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d/%d", w.GetMuchaGameId(), w.GetVersion(), w.GetWorldId())
}

// tryMatchLocked fills rooms from the queue, oldest entries first. Entries stay whole, so a
// pair from one entry always lands on the same team.
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

// pickLocked chooses entries whose player counts add up to exactly size and can be split
// into two equal teams without breaking an entry up.
func (m *Matcher) pickLocked(size int) []*entry {
	if size <= 0 || len(m.queue) == 0 {
		return nil
	}
	head := m.queue[0]
	var pick []*entry
	total := 0
	for _, e := range m.queue {
		if m.cfg.RequireSameWorld && e.world != head.world {
			continue
		}
		if total+e.players > size {
			continue
		}
		if len(pick) > 0 && time.Since(head.entered) < m.widenAfter() && m.recentlyFailedLocked(pick, e) {
			continue
		}
		pick = append(pick, e)
		total += e.players
		if total == size {
			if _, ok := splitTeams(pick, size/2); ok {
				return pick
			}
			pick, total = pick[:len(pick)-1], total-e.players
		}
	}
	return nil
}

// splitTeams assigns each entry to team 0 or 1 so both teams have half players.
func splitTeams(es []*entry, half int) ([]int, bool) {
	n := len(es)
	for mask := 0; mask < 1<<n; mask++ {
		c := 0
		for i, e := range es {
			if mask&(1<<i) != 0 {
				c += e.players
			}
		}
		if c == half {
			t := make([]int, n)
			for i := range es {
				if mask&(1<<i) == 0 {
					t[i] = 0
				} else {
					t[i] = 1
				}
			}
			return t, true
		}
	}
	return nil, false
}

func (m *Matcher) makeRoomLocked(es []*entry) {
	total := 0
	for _, e := range es {
		total += e.players
	}
	teams, _ := splitTeams(es, total/2)
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
