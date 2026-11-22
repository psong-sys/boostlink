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
:root{--bg:#0b090c;--fg:#fbf9ee;--muted:#b9b3c2;--card:#16131a;--line:#3a3442;--accent:#25b1c8;--ok:#16dece;--bad:#e9465e;--yellow:#f0f31d;--red:#e9465e;--cyan:#25b1c8;
  --head:"Anton",Impact,sans-serif;--body:"Chakra Petch",system-ui,"Segoe UI",sans-serif}
*{box-sizing:border-box}
body{margin:0;background:radial-gradient(circle at 1px 1px,rgba(210,58,117,.2) 1.2px,transparent 1.6px) 0 0/9px 9px,var(--bg);color:var(--fg);font:500 16px/1.5 var(--body)}
body::before{content:"";display:block;height:14px;background:repeating-linear-gradient(-45deg,var(--yellow) 0 10px,var(--bg) 10px 20px)}
main{max-width:760px;margin:0 auto;padding:24px 16px 40px}
h1{font-family:var(--head);font-weight:400;font-size:44px;line-height:1;margin:0 0 6px;color:var(--yellow);text-transform:uppercase;transform:skew(-10deg);display:inline-block;text-shadow:5px 5px 0 var(--red)}
h1 a{color:inherit;text-decoration:none}
.sub{color:var(--muted);margin:6px 0 22px}
.tiles{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-bottom:24px}
.tile{background:var(--card);border:3px solid var(--fg);padding:12px 14px;box-shadow:5px 5px 0 var(--c,var(--cyan))}
.tile b{display:block;font-family:var(--head);font-weight:400;font-size:40px;color:var(--c,var(--cyan));font-variant-numeric:tabular-nums}
.tile span{color:var(--muted);font-size:13px;text-transform:uppercase;letter-spacing:.5px}
h2{font-family:var(--head);font-weight:400;font-size:24px;text-transform:uppercase;letter-spacing:.8px;margin:28px 0 10px;color:var(--cyan)}
table{width:100%;border-collapse:collapse;background:var(--card);border:2px solid var(--line)}
td,th{padding:8px 12px;text-align:left;border-bottom:1px solid var(--line);font-variant-numeric:tabular-nums}
th{color:var(--muted);font-weight:500;font-size:13px;text-transform:uppercase}.empty{color:var(--muted);padding:12px;border:2px dashed var(--line)}
.res{background:var(--card);border:2px solid var(--line);border-left:6px solid var(--bad);padding:10px 14px;margin-bottom:8px}
.res.ok{border-left-color:var(--ok)}.res p{margin:4px 0}.res .fix{font-weight:700;color:var(--yellow)}.res .meta,.res .when{color:var(--muted);font-size:14px}
.lb .pilot{font-weight:700}.w{color:var(--ok)}.l{color:var(--bad)}.when{color:var(--muted);font-size:14px;white-space:nowrap}
.scroll{overflow-x:auto}
@media (max-width:540px){.tiles{gap:10px}.tile b{font-size:30px}.tile span{font-size:11px}.opt{display:none}td,th{padding:8px 8px}}
</style></head><body><main>
<h1><a href="/">Boost Link</a></h1><p class="sub">Updates every 5 seconds. Up for {{dur .UptimeSeconds}}.</p>
<div class="tiles">
<div class="tile" style="--c:var(--yellow)"><b>{{.Searching}}</b><span>searching now</span></div>
<div class="tile" style="--c:var(--red)"><b>{{.InRooms}}</b><span>just matched</span></div>
<div class="tile"><b>{{.RoomSize}}</b><span>players per match</span></div>
</div>
<h2>Leaderboard</h2>
{{if .Leaderboard}}<div class="scroll"><table class="lb"><tr><th>#</th><th>Pilot</th><th>W</th><th>L</th><th>Win %</th><th>Streak</th><th>Best</th><th class="opt">Avg dmg</th></tr>
{{range $i, $p := .Leaderboard}}<tr><td>{{inc $i}}</td><td class="pilot">{{$p.Name}}</td><td class="w">{{$p.Wins}}</td><td class="l">{{$p.Losses}}</td><td>{{$p.WinPct}}%</td><td>{{if $p.Streak}}{{$p.Streak}}{{else}}-{{end}}</td><td>{{$p.Best}}</td><td class="opt">{{$p.AvgDamage}}</td></tr>{{end}}</table></div>
{{else}}<div class="empty">No results yet. Every online match played through this server counts.</div>{{end}}
{{if .Fights}}<h2>Recent fights</h2><table>
{{range .Fights}}<tr><td>{{if .Winners}}<b class="w">{{names .Winners}}</b>{{else}}?{{end}} beat {{if .Losers}}<b class="l">{{names .Losers}}</b>{{else}}?{{end}}</td><td class="when">{{ago .At}} ago</td></tr>{{end}}</table>{{end}}
<h2>In the queue</h2>
{{if .WaitingGroups}}<table><tr><th>Players</th><th>Waiting</th></tr>
{{range .WaitingGroups}}<tr><td>{{.Players}}</td><td>{{dur .WaitSeconds}}</td></tr>{{end}}</table>
{{else}}<div class="empty">Nobody is searching right now.</div>{{end}}
<h2>Recent matches</h2>
{{if .RecentRooms}}<table><tr><th>Match</th><th>Players</th><th>Started</th></tr>
{{range .RecentRooms}}<tr><td>#{{.ID}}{{if .Test}} (test){{end}}</td><td>{{.Players}}</td><td>{{dur .AgeSeconds}} ago</td></tr>{{end}}</table>
{{else}}<div class="empty">No matches yet.</div>{{end}}
<h2>Connection results</h2>
{{if .Results}}{{range .Results}}<div class="res {{if .Connected}}ok{{else}}bad{{end}}"><b>{{.From}}, try {{.Try}}: {{.Result}}</b> <span class="when">{{ago .At}} ago</span>
<p>{{.Meaning}}</p>{{if .Fix}}<p class="fix">Fix: {{.Fix}}</p>{{end}}<p class="meta">NAT: {{.NAT}}{{if .RTTs}}. Ping: {{range $i, $r := .RTTs}}{{if $i}}, {{end}}{{$r}} ms{{end}} (limit {{$.RTTLimit}}){{end}}</p></div>{{end}}
{{else}}<div class="empty">No connection attempts yet. The game reports one after every match try.</div>{{end}}
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
