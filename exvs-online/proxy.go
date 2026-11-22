package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"exvsonline/pb/exvs"
	"exvsonline/pb/mms"

	"google.golang.org/protobuf/proto"
)

type Proxy struct {
	cfg      ProxyConfig
	upstream *url.URL
	match    *url.URL // nil when the match server is built in
	local    *Matcher
	client   *http.Client
	tl       *trafficLog
	rturn    remoteTurn
	rset     remoteSettings
	hw       *hangWatch // probes the game when it goes quiet after a result
}

func NewProxy(cfg ProxyConfig, local *Matcher, tl *trafficLog) (*Proxy, error) {
	up, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("bad upstream: %w", err)
	}
	p := &Proxy{cfg: cfg, upstream: up, local: local, tl: tl,
		client: &http.Client{Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if local == nil {
		if cfg.MatchServer == "" {
			return nil, fmt.Errorf("proxy mode needs proxy.matchServer (or use mode \"both\")")
		}
		if p.match, err = url.Parse(cfg.MatchServer); err != nil {
			return nil, fmt.Errorf("bad matchServer: %w", err)
		}
	}
	return p, nil
}

func isMatchPath(path string) bool {
	return strings.HasSuffix(strings.TrimRight(path, "/"), "/match")
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProxyBody+1))
	r.Body.Close()
	if err != nil || len(body) > maxProxyBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}

	if r.Method == http.MethodGet && p.local != nil && (r.URL.Path == "/status" || r.URL.Path == "/status.json") {
		p.local.StatusHandler().ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodPost && p.local != nil && r.URL.Path == "/report" {
		r.Body = io.NopCloser(bytes.NewReader(body))
		p.local.ReportHandler().ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && p.local != nil && r.URL.Path == "/home" {
		p.local.HomeHandler().ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && p.local != nil && r.URL.Path == "/settings" {
		p.local.SettingsHandler().ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/turninfo" {
		turnInfoHandler(w, r)
		return
	}
	if r.Method == http.MethodPost && isMatchPath(r.URL.Path) {
		// Only our own setting decides the team code, never the caller.
		r.Header.Del(TeamHeader)
		if t := normTeam(p.cfg.TeamCode); t != "" {
			r.Header.Set(TeamHeader, t)
		}
		if p.local != nil {
			r.Body = io.NopCloser(bytes.NewReader(body))
			p.local.Handler(p.tl).ServeHTTP(w, r)
			return
		}
		p.hw.activity()
		p.logMMS("MMS>", body, &mms.Request{})
		p.forward(w, r, body, p.match, "/match", func(rb []byte) []byte {
			p.logMMS("MMS<", rb, &mms.Response{})
			return rb
		})
		return
	}

	// Card server traffic. Decode game requests for the log and so RegisterPcb can be patched.
	var gameReq *exvs.Request
	if r.Method == http.MethodPost && len(body) > 0 && !strings.Contains(r.Header.Get("Content-Type"), "form") {
		req := &exvs.Request{}
		if err := (proto.UnmarshalOptions{AllowPartial: true, DiscardUnknown: false}).Unmarshal(body, req); err == nil && req.Type != nil && req.RequestId != nil {
			gameReq = req
			p.tl.Msg("GAME>", req)
			switch req.GetType() {
			case exvs.MethodType_MTHD_SAVE_VSM_ON_RESULT:
				p.hw.resultSaved()
			case exvs.MethodType_MTHD_PING, exvs.MethodType_MTHD_LOAD_RANKING:
				// sent on a timer even while the game is stuck, so not a sign it moved on
			default:
				p.hw.activity()
			}
			if c := req.GetCheckCommunication(); c != nil {
				p.reportCheck(c)
			}
		}
	}
	var patch func([]byte) []byte
	if gameReq != nil {
		patch = func(b []byte) []byte { return p.patchGameResponse(r, gameReq, b) }
	}
	p.forward(w, r, body, p.upstream, "", patch)
}

// forward sends the request on to target. pathOverride replaces the path when set.
// The original Host header is kept so the card server keeps building URLs that point at us.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, body []byte, target *url.URL, pathOverride string, patch func([]byte) []byte) {
	u := *target
	if pathOverride != "" {
		u.Path = strings.TrimRight(target.Path, "/") + pathOverride
	} else {
		u.Path = strings.TrimRight(target.Path, "/") + r.URL.Path
	}
	u.RawQuery = r.URL.RawQuery
	out, err := http.NewRequest(r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	out.Header.Del("Accept-Encoding") // keep bodies plain so they can be patched
	if target == p.upstream {
		out.Host = r.Host
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		out.Header.Set("X-Forwarded-For", ip)
	}
	resp, err := p.client.Do(out)
	if err != nil {
		log.Printf("forward %s %s: %v", r.Method, u.String(), err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	if patch != nil && resp.StatusCode == 200 {
		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			if zr, err := gzip.NewReader(bytes.NewReader(rb)); err == nil {
				if plain, err := io.ReadAll(zr); err == nil {
					rb = plain
					resp.Header.Del("Content-Encoding")
				}
			}
		}
		rb = patch(rb)
	}
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(rb)))
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}

func (p *Proxy) patchGameResponse(r *http.Request, req *exvs.Request, b []byte) []byte {
	resp := &exvs.Response{}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, resp); err != nil {
		p.tl.Line("GAME<", fmt.Sprintf("<undecodable %d bytes: %v>", len(b), err))
		return b
	}
	if req.GetType() == exvs.MethodType_MTHD_REGISTER_PCB && resp.RegisterPcb != nil {
		p.tl.Msg("GAME< (original)", resp)
		p.patchRegisterPcb(r, resp.RegisterPcb)
		fillRequired(resp)
		nb, err := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		if err != nil {
			log.Printf("re-encode RegisterPcb: %v", err)
			return b
		}
		p.tl.Msg("GAME< (patched)", resp)
		return nb
	}
	if req.GetType() == exvs.MethodType_MTHD_LOAD_BLACK_LIST && resp.LoadBlackList != nil {
		orig := proto.Clone(resp)
		if p.patchBlackList(resp.LoadBlackList) {
			p.tl.Msg("GAME< (original)", orig)
			fillRequired(resp)
			if nb, err := (proto.MarshalOptions{AllowPartial: true}).Marshal(resp); err == nil {
				p.tl.Msg("GAME< (patched ping limit)", resp)
				return nb
			}
			resp = orig.(*exvs.Response)
		}
	}
	if req.GetType() == exvs.MethodType_MTHD_PRE_LOAD_CARD && p.patchCardMobileSuit(req.PreLoadCard, resp.PreLoadCard) {
		fillRequired(resp)
		if nb, err := (proto.MarshalOptions{AllowPartial: true}).Marshal(resp); err == nil {
			p.tl.Msg("GAME< (patched card mobile suit)", resp)
			return nb
		} else {
			log.Printf("re-encode PreLoadCard: %v", err)
			return b
		}
	}
	if req.GetType() == exvs.MethodType_MTHD_LOAD_GAME_DATA && resp.LoadGameData != nil && resp.LoadGameData.OnVsInfo == nil {
		t, d := p.cfg.OnlineTimeLimitSeconds, p.cfg.OnlineDamageLevel
		if t == 0 {
			t = 230
		}
		if d == 0 {
			d = 2
		}
		resp.LoadGameData.OnVsInfo = &exvs.Response_LoadGameData_OnVsInfo{
			RuleTimeLimitClass: proto.Uint32(t), RuleTimeLimitFree: proto.Uint32(t),
			RuleDamageLevelTeam: proto.Uint32(d), RuleDamageLevelShuffle: proto.Uint32(d),
		}
		fillRequired(resp)
		nb, err := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		if err != nil {
			log.Printf("re-encode LoadGameData: %v", err)
			return b
		}
		p.tl.Msg("GAME< (patched on_vs_info)", resp)
		return nb
	}
	if n := fillRequired(resp); n > 0 {
		nb, err := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		if err != nil {
			log.Printf("re-encode after filling required fields: %v", err)
			return b
		}
		p.tl.Msg(fmt.Sprintf("GAME< (filled %d required)", n), resp)
		return nb
	}
	p.tl.Msg("GAME<", resp)
	return b
}

// resolveTurn returns the relay to advertise: a fixed config entry, or with host "auto"
// this process's own relay (host) or the match host's (players).
func (p *Proxy) resolveTurn() *HostPort {
	t := p.cfg.Turn
	if t == nil || t.Host != "auto" {
		return t
	}
	ti := localTurn
	if ti == nil && p.match != nil {
		ti = p.rturn.get(p.client, strings.TrimRight(p.match.String(), "/"))
	}
	if ti == nil {
		return nil
	}
	return &HostPort{Host: ti.Host, Port: ti.Port, User: ti.User, Pass: ti.Pass}
}

func (p *Proxy) patchRegisterPcb(r *http.Request, rp *exvs.Response_RegisterPcb) {
	var keep []*exvs.Response_RegisterPcb_ServerInfo
	hasMatch := false
	for _, s := range rp.ServerInfo {
		switch s.GetServerType() {
		case exvs.ServerType_SRV_MATCH:
			// Keep the card server's own entry (its URI format is known to work with the game)
			// as long as it points back at this proxy.
			hasMatch = true
			keep = append(keep, s)
		case exvs.ServerType_SRV_STUN:
			if p.cfg.Stun == nil {
				keep = append(keep, s)
			}
		case exvs.ServerType_SRV_TURN:
			if p.cfg.Turn == nil {
				keep = append(keep, s)
			}
		}
	}
	if !hasMatch {
		port := p.cfg.MatchPort
		keep = append(keep, &exvs.Response_RegisterPcb_ServerInfo{
			ServerType: exvs.ServerType_SRV_MATCH.Enum(),
			Uri:        proto.String(r.Host + "/match"),
			Port:       &port,
		})
	}
	if s := p.cfg.Stun; s != nil && s.Host != "" {
		host := s.Host
		switch host {
		case "auto":
			host = firstLANIP()
		case "match":
			// The match server's own STUN (a public server runs one next to matchmaking).
			host = p.matchHostIP()
		}
		keep = append(keep, &exvs.Response_RegisterPcb_ServerInfo{
			ServerType: exvs.ServerType_SRV_STUN.Enum(), Uri: proto.String(host), Port: proto.Uint32(s.Port),
		})
	}
	if t := p.resolveTurn(); t != nil && t.Host != "" {
		e := &exvs.Response_RegisterPcb_ServerInfo{
			ServerType: exvs.ServerType_SRV_TURN.Enum(), Uri: proto.String(t.Host), Port: proto.Uint32(t.Port),
		}
		if t.User != "" {
			e.AccountName, e.AccountPass = proto.String(t.User), proto.String(t.Pass)
		}
		keep = append(keep, e)
	}
	rp.ServerInfo = keep
	// Off unless configured: an untested attempt at the same-LAN 21-11 error.
	if len(rp.LmIpAddresses) == 0 && len(p.cfg.LMIPAddresses) > 0 {
		if len(p.cfg.LMIPAddresses) == 1 && p.cfg.LMIPAddresses[0] == "auto" {
			rp.LmIpAddresses = localIPv4s()
		} else if p.cfg.LMIPAddresses[0] != "off" {
			rp.LmIpAddresses = append([]string(nil), p.cfg.LMIPAddresses...)
		}
	}
}

// localIPv4s lists this PC's own LAN IPv4 addresses (no loopback: the LM binds to the
// adapter address, so listing 127.0.0.1 can send the game to an address with no LM).
func localIPv4s() []string {
	out := []string{}
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

// matchHostIP is the match server's address as an IPv4 string (the game wants an IP).
func (p *Proxy) matchHostIP() string {
	if p.match == nil {
		return firstLANIP()
	}
	h := p.match.Hostname()
	if ip := net.ParseIP(h); ip != nil {
		return h
	}
	if ips, err := net.LookupIP(h); err == nil {
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return h
}

// logMMS writes a forwarded matchmaking message to traffic.log, so a player PC
// can show what the game asked the remote match server.
func (p *Proxy) logMMS(dir string, b []byte, m proto.Message) {
	if p.tl == nil {
		return
	}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, m); err != nil {
		p.tl.Line(dir, fmt.Sprintf("<undecodable %d bytes: %v>", len(b), err))
		return
	}
	p.tl.Msg(dir, m)
}

// patchCardMobileSuit fills in the suit drawn on the card on the "Use this card?"
// screen. The game shows the first entry of PreLoadCard user.favorite_mobile_suits
// (tested 2026-11-22) and "NOT SET" when the list is empty, which it always is from
// Server-Infinite. The suit is cardMobileSuit when set, otherwise the player's
// first favorite suit from Server-Infinite's web API.
func (p *Proxy) patchCardMobileSuit(rq *exvs.Request_PreLoadCard, pl *exvs.Response_PreLoadCard) bool {
	if pl == nil || pl.User == nil || len(pl.User.FavoriteMobileSuits) > 0 {
		return false
	}
	ms := p.cfg.CardMobileSuit
	if ms == 0 && rq != nil {
		ms = p.firstFavoriteSuit(rq.GetAccessCode(), rq.GetChipId())
	}
	if ms == 0 {
		return false
	}
	pl.User.FavoriteMobileSuits = []*exvs.Response_PreLoadCard_MobileUserGroup_FavoriteMSGroup{{
		MstMobileSuitId: proto.Uint32(ms), MsUsedNum: proto.Uint32(0),
		BgmPlayMethod: proto.Uint32(0), BurstType: proto.Uint32(0)}}
	return true
}

// firstFavoriteSuit asks Server-Infinite's web API for the card's favorite suits
// and returns the first one's id (0 when there is none or the call fails).
func (p *Proxy) firstFavoriteSuit(accessCode, chipID string) uint32 {
	if accessCode == "" || chipID == "" || p.upstream == nil {
		return 0
	}
	u := strings.TrimRight(p.upstream.String(), "/") + "/ui/mobileSuit/getAllFavouriteMs/" +
		url.PathEscape(accessCode) + "/" + url.PathEscape(chipID)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(u)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return firstSuitID(b)
}

// firstSuitID reads the first suit id from the favorite suit list, which is
// either a JSON array or an object holding one, with ids under msId or MsId.
func firstSuitID(b []byte) uint32 {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return 0
	}
	if m, ok := v.(map[string]any); ok {
		for _, x := range m {
			if a, ok := x.([]any); ok {
				v = a
				break
			}
		}
	}
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return 0
	}
	if e, ok := a[0].(map[string]any); ok {
		for k, x := range e {
			if strings.EqualFold(k, "msId") || strings.EqualFold(k, "mstMobileSuitId") {
				if f, ok := x.(float64); ok && f > 0 {
					return uint32(f)
				}
			}
		}
	}
	return 0
}
