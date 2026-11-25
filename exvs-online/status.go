package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// Status is the public snapshot behind /status and /status.json. It holds no IPs or serials.
type Status struct {
	Searching     int           `json:"searching"`   // players waiting in the queue
	WaitingGroups []StatusEntry `json:"waiting"`     // one per queue entry
	InRooms       int           `json:"inRooms"`     // players matched in the last few minutes, still polling
	RecentRooms   []StatusRoom  `json:"recentRooms"` // newest first
	Results       []ConnResult  `json:"results"`     // newest first
	Leaderboard   []PilotStats  `json:"leaderboard"` // most wins first
	Fights        []Fight       `json:"fights"`      // newest first
	RoomSize      int           `json:"roomSize"`
	RTTLimit      uint32        `json:"rttLimitMs"`
	UptimeSeconds int64         `json:"uptimeSeconds"`
	StoreTeams    int           `json:"storeTeams"`   // Store Team groups with 2+ players
	StorePlayers  int           `json:"storePlayers"` // players on the Store Team relay
	Now           time.Time     `json:"now"`
}

type StatusEntry struct {
	Players     int    `json:"players"`
	WaitSeconds int64  `json:"waitSeconds"`
	World       string `json:"world,omitempty"`
}

type StatusRoom struct {
	ID         uint32 `json:"id"`
	Players    int    `json:"players"`
	AgeSeconds int64  `json:"ageSeconds"`
	Test       bool   `json:"test,omitempty"`
}

func (m *Matcher) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	lim := m.cfg.RTTLimitMs
	if lim == 0 {
		lim = 300
	}
	st := Status{RoomSize: m.cfg.RoomSize, RTTLimit: lim, UptimeSeconds: int64(now.Sub(m.started).Seconds()), Now: now,
		WaitingGroups: []StatusEntry{}, RecentRooms: []StatusRoom{}}
	st.StoreTeams, st.StorePlayers = m.storeRelay.Stats()
	for _, e := range m.queue {
		if e.bot {
			continue
		}
		st.Searching += e.players
		st.WaitingGroups = append(st.WaitingGroups, StatusEntry{Players: e.players, WaitSeconds: int64(now.Sub(e.entered).Seconds()), World: e.world})
	}
	for _, e := range m.entries {
		if e.roomID != 0 && !e.bot {
			st.InRooms += e.players
		}
	}
	for i := len(m.recent) - 1; i >= 0; i-- {
		r := m.recent[i]
		st.RecentRooms = append(st.RecentRooms, StatusRoom{ID: r.ID, Players: r.Players, AgeSeconds: int64(now.Sub(r.At).Seconds()), Test: r.Bots})
	}
	if m.stats != nil {
		st.Leaderboard, st.Fights = m.stats.board(20)
	}
	st.Results = []ConnResult{}
	for i := len(m.results) - 1; i >= 0; i-- {
		st.Results = append(st.Results, m.results[i])
	}
	return st
}

var statusPage = template.Must(template.New("s").Funcs(template.FuncMap{
	"ago":   func(t time.Time) string { return fmtDur(int64(time.Since(t).Seconds())) },
	"dur":   fmtDur,
	"names": func(ns []string) string { return strings.Join(ns, " + ") },
	"inc":   func(i int) int { return i + 1 },
}).Parse(statusHTML))

func fmtDur(s int64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
}

const statusHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="5"><title>Boost Link status</title>
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Chakra+Petch:wght@500;700&family=Anton&display=swap" rel="stylesheet">
<style>
:root{--bg:#0b090c;--fg:#fbf9ee;--muted:#9a94a3;--dim:#6b6573;--line:#2a2530;--panel:#131016;--ok:#16dece;--bad:#e9465e;--yellow:#f0f31d;--red:#e9465e;--cyan:#25b1c8;
  --head:"Anton",Impact,sans-serif;--body:"Chakra Petch",system-ui,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:500 15px/1.5 var(--body)}
body::before{content:"";display:block;height:5px;background:repeating-linear-gradient(-45deg,var(--yellow) 0 8px,var(--bg) 8px 16px)}
main{max-width:1040px;margin:0 auto;padding:22px 16px 48px}
header{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-bottom:18px;border-bottom:1px solid var(--line)}
.logo{font-family:var(--head);font-weight:400;font-size:30px;line-height:1;color:var(--yellow);text-transform:uppercase;transform:skew(-10deg);text-shadow:3px 3px 0 var(--red);text-decoration:none}
.live{color:var(--muted);font-size:13px;display:flex;align-items:center;gap:8px;white-space:nowrap}
.live i{width:8px;height:8px;border-radius:50%;background:var(--ok);box-shadow:0 0 0 3px rgba(22,222,206,.18)}
.strip{display:flex;flex-wrap:wrap;gap:8px 32px;padding:18px 0 26px}
.stat{display:flex;align-items:baseline;gap:8px}
.stat b{font-family:var(--head);font-weight:400;font-size:34px;line-height:1;font-variant-numeric:tabular-nums}
.stat span{color:var(--muted);font-size:13px}
.grid{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(0,1fr);gap:40px}
section{margin-bottom:30px}
h2{display:flex;align-items:baseline;justify-content:space-between;font-size:12px;font-weight:700;letter-spacing:1.4px;text-transform:uppercase;color:var(--muted);margin:0 0 4px;padding-bottom:8px;border-bottom:1px solid var(--line)}
h2 small{font-weight:500;letter-spacing:0;text-transform:none;color:var(--dim)}
table{width:100%;border-collapse:collapse}
td,th{padding:9px 6px;text-align:left;border-bottom:1px solid var(--line);font-variant-numeric:tabular-nums;white-space:nowrap}
th{color:var(--dim);font-weight:500;font-size:12px}
td.n,th.n{text-align:right}
.rank{color:var(--dim);width:28px}
.pilot{font-weight:700;width:100%}
tr.top .pilot{color:var(--yellow)}
.w{color:var(--ok)}.l{color:var(--bad)}
.none{color:var(--dim);padding:12px 0;margin:0}
.fight{display:flex;justify-content:space-between;gap:12px;padding:9px 0;border-bottom:1px solid var(--line)}
.fight .vs{color:var(--dim)}
.when{color:var(--dim);font-size:13px;white-space:nowrap}
.res{padding:10px 0;border-bottom:1px solid var(--line)}
.res .t{display:flex;justify-content:space-between;gap:10px}
.res .t b{font-weight:700}
.res .t b::before{content:"";display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--bad);margin-right:8px;vertical-align:middle}
.res.ok .t b::before{background:var(--ok)}
.res p{margin:3px 0 0 15px;color:var(--muted);font-size:14px}.res .fix{color:var(--yellow)}
@media (max-width:820px){.grid{grid-template-columns:1fr;gap:6px}}
@media (max-width:540px){.opt{display:none}.stat b{font-size:28px}.strip{gap:8px 22px}td,th{padding:9px 4px}}
</style></head><body><main>
<header><a class="logo" href="/">Boost Link</a><span class="live"><i></i>Live, up {{dur .UptimeSeconds}}</span></header>
<div class="strip">
<div class="stat"><b style="color:var(--yellow)">{{.Searching}}</b><span>searching now</span></div>
<div class="stat"><b style="color:var(--red)">{{.InRooms}}</b><span>just matched</span></div>
<div class="stat"><b style="color:var(--cyan)">{{.RoomSize}}</b><span>players per match</span></div>
</div>
<div class="grid"><div>
<section><h2>Leaderboard <small>online matches on this server</small></h2>
{{if .Leaderboard}}<table><tr><th class="rank">#</th><th>Pilot</th><th class="n">W</th><th class="n">L</th><th class="n">Win %</th><th class="n">Streak</th><th class="n opt">Best</th><th class="n opt">Avg dmg</th></tr>
{{range $i, $p := .Leaderboard}}<tr{{if eq $i 0}} class="top"{{end}}><td class="rank">{{inc $i}}</td><td class="pilot">{{$p.Name}}</td><td class="n w">{{$p.Wins}}</td><td class="n l">{{$p.Losses}}</td><td class="n">{{$p.WinPct}}%</td><td class="n">{{if $p.Streak}}{{$p.Streak}}{{else}}&ndash;{{end}}</td><td class="n opt">{{$p.Best}}</td><td class="n opt">{{$p.AvgDamage}}</td></tr>{{end}}</table>
{{else}}<p class="none">No results yet. Every online match played through this server counts.</p>{{end}}</section>
<section><h2>Recent fights</h2>
{{if .Fights}}{{range .Fights}}<div class="fight"><span>{{if .Winners}}<b class="w">{{names .Winners}}</b>{{else}}?{{end}} <span class="vs">beat</span> {{if .Losers}}<b class="l">{{names .Losers}}</b>{{else}}?{{end}}</span><span class="when">{{ago .At}} ago</span></div>{{end}}
{{else}}<p class="none">No fights yet.</p>{{end}}</section>
</div><div>
<section><h2>Queue</h2>
{{if .WaitingGroups}}<table><tr><th>Players</th><th class="n">Waiting</th></tr>
{{range .WaitingGroups}}<tr><td>{{.Players}}</td><td class="n">{{dur .WaitSeconds}}</td></tr>{{end}}</table>
{{else}}<p class="none">Nobody is searching right now.</p>{{end}}</section>
<section><h2>Recent matches</h2>
{{if .RecentRooms}}<table><tr><th>Match</th><th>Players</th><th class="n">Started</th></tr>
{{range .RecentRooms}}<tr><td>#{{.ID}}{{if .Test}} <span class="when">test</span>{{end}}</td><td>{{.Players}}</td><td class="n when">{{dur .AgeSeconds}} ago</td></tr>{{end}}</table>
{{else}}<p class="none">No matches yet.</p>{{end}}</section>
<section><h2>Connection results</h2>
{{if .Results}}{{range .Results}}<div class="res {{if .Connected}}ok{{end}}"><div class="t"><b>{{.From}}, try {{.Try}}: {{.Result}}</b><span class="when">{{ago .At}} ago</span></div>
<p>{{.Meaning}}</p>{{if .Fix}}<p class="fix">Fix: {{.Fix}}</p>{{end}}<p>NAT: {{.NAT}}{{if .RTTs}}. Ping: {{range $i, $r := .RTTs}}{{if $i}}, {{end}}{{$r}} ms{{end}} (limit {{$.RTTLimit}}){{end}}</p></div>{{end}}
{{else}}<p class="none">No connection attempts yet. The game reports one after every match try.</p>{{end}}</section>
</div></div>
</main></body></html>`

// StatusHandler serves /status (HTML) and /status.json.
func (m *Matcher) StatusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := m.Status()
		if r.URL.Path == "/status.json" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_ = json.NewEncoder(w).Encode(st)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = statusPage.Execute(w, st)
	})
}
