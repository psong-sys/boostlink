package main

// The public home page a match server shows at "/" (and "/home"): what Boost Link is,
// a live queue count, how to join, and a Discord button.

import (
	"html/template"
	"net"
	"net/http"
	"strings"
)

type homeData struct {
	Host     string
	Discord  string
	Tagline  string
	RoomSize int
}

var homePage = template.Must(template.New("home").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Boost Link</title>
<meta name="description" content="Online matchmaking for EXVS2 Infinite Boost on PC.">
<meta name="theme-color" content="#0b090c">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Chakra+Petch:ital,wght@0,500;0,700;1,700&family=Anton&display=swap" rel="stylesheet">
<style>
:root{
  --ink:#0b090c; --paper:#fbf9ee; --muted:#b9b3c2;
  --red:#e9465e; --yellow:#f0f31d; --gold:#eadd29; --blue:#2c6ece; --cyan:#25b1c8; --teal:#16dece; --magenta:#d23a75; --orange:#e09f33;
  --panel:#16131a; --head:"Anton",Impact,"Arial Narrow Bold",sans-serif; --body:"Chakra Petch",system-ui,"Segoe UI",sans-serif;
}
*{box-sizing:border-box}
html{background:var(--ink)}
body{margin:0;color:var(--paper);font:500 17px/1.5 var(--body);background:
  radial-gradient(circle at 1px 1px,rgba(210,58,117,.22) 1.2px,transparent 1.6px) 0 0/9px 9px,
  linear-gradient(160deg,#1a0f1d 0%,var(--ink) 45%,#08131c 100%);min-height:100vh;overflow-x:hidden}
.band{height:22px;background:repeating-linear-gradient(-45deg,var(--yellow) 0 14px,var(--ink) 14px 28px)}
.band.cy{background:repeating-linear-gradient(45deg,var(--cyan) 0 14px,var(--ink) 14px 28px)}
main{max-width:860px;margin:0 auto;padding:36px 16px 56px}
/* wordmark */
.mark{font-family:var(--head);font-size:clamp(56px,13vw,112px);line-height:.9;margin:0;letter-spacing:1px;text-transform:uppercase;
  color:var(--yellow);transform:skew(-10deg);display:inline-block;
  text-shadow:3px 3px 0 var(--ink),-2px -2px 0 var(--ink),2px -2px 0 var(--ink),-2px 2px 0 var(--ink),8px 8px 0 var(--red),11px 11px 0 var(--ink)}
.mark span{color:var(--cyan)}
.tag{display:inline-block;margin:22px 0 0;padding:10px 26px 10px 18px;background:var(--blue);color:#fff;font-weight:700;
  clip-path:polygon(0 0,100% 0,calc(100% - 16px) 100%,0 100%);border-left:6px solid var(--yellow)}
/* live tiles */
.live{display:grid;grid-template-columns:repeat(3,1fr);gap:18px;margin:40px 0 8px}
.tile{position:relative;background:var(--panel);border:3px solid var(--paper);padding:14px 16px 16px;box-shadow:6px 6px 0 var(--c)}
.tile::before{content:"";position:absolute;left:0;top:0;width:58%;height:6px;background:var(--c)}
.tile b{display:block;font-family:var(--head);font-weight:400;font-size:48px;line-height:1.05;color:var(--c);font-variant-numeric:tabular-nums}
.tile span{color:var(--muted);font-size:14px;text-transform:uppercase;letter-spacing:.6px}
.on{display:inline-block;width:10px;height:10px;margin-right:7px;background:var(--muted);transform:rotate(45deg)}
.on.live{background:var(--teal);box-shadow:0 0 10px var(--teal)}
/* headings + steps */
h2{font-family:var(--head);font-weight:400;font-size:34px;text-transform:uppercase;letter-spacing:1px;margin:44px 0 14px;
  display:inline-block;padding:2px 22px 2px 12px;background:var(--red);color:var(--ink);transform:skew(-10deg)}
h2>i{display:inline-block;transform:skew(10deg);font-style:normal}
ol{list-style:none;padding:0;margin:0;counter-reset:s}
li{counter-increment:s;display:grid;grid-template-columns:52px 1fr;gap:14px;align-items:start;padding:12px 0;border-bottom:2px dashed rgba(251,249,238,.14)}
li::before{content:counter(s);font-family:var(--head);font-size:28px;line-height:44px;text-align:center;color:var(--ink);background:var(--yellow);
  clip-path:polygon(18% 0,100% 0,82% 100%,0 100%)}
li b{color:var(--yellow)}
code{display:inline-block;max-width:100%;font:700 16px var(--body);color:var(--ink);background:var(--teal);padding:1px 8px;overflow-wrap:anywhere}
/* buttons */
.btns{display:flex;flex-wrap:wrap;gap:16px;margin-top:34px}
.btn{display:inline-block;padding:14px 30px;font:700 17px var(--body);text-transform:uppercase;letter-spacing:.8px;text-decoration:none;
  clip-path:polygon(14px 0,100% 0,calc(100% - 14px) 100%,0 100%);transition:transform .12s}
.btn:hover,.btn:focus-visible{transform:translateY(-2px)}
.btn.primary{background:var(--yellow);color:var(--ink)}
.btn.sec{background:var(--cyan);color:var(--ink)}
.note{color:var(--muted);font-size:14px;margin-top:44px}
@media (max-width:560px){.live{gap:12px}.tile{box-shadow:4px 4px 0 var(--c)}.tile{padding:10px}.tile b{font-size:32px}.tile span{font-size:11px}h2{font-size:28px}li{grid-template-columns:42px 1fr}}
@media (prefers-reduced-motion:reduce){.btn{transition:none}}
</style></head><body>
<div class="band"></div>
<main>
<h1 class="mark">Boost <span>Link</span></h1><br>
<p class="tag">{{if .Tagline}}{{.Tagline}}{{else}}Online matches for EXVS2 Infinite Boost on PC.{{end}}</p>

<section class="live" aria-live="polite">
<div class="tile" style="--c:var(--yellow)"><b id="searching">–</b><span><i class="on" id="dot"></i>Searching now</span></div>
<div class="tile" style="--c:var(--red)"><b id="matched">–</b><span>Matches this hour</span></div>
<div class="tile" style="--c:var(--cyan)"><b id="ok">–</b><span>Connections OK</span></div>
</section>

<h2><i>How to join</i></h2>
<ol>
<li><div>Have your own copy of EXVS2 Infinite Boost (v1.04.27) running on PC. We don't share game files.</div></li>
<li><div>Get the Boost Link pack{{if .Discord}} from our Discord{{end}} and extract it into your game folder.</div></li>
<li><div>Open the launcher, pick your network adapter in <b>Settings</b>, then click <b>Check my PC</b>.</div></li>
<li><div>Go to <b>Play &gt; Join</b>, type <code>{{.Host}}</code>, press Start, and search in online versus.</div></li>
</ol>

<div class="btns">
{{if .Discord}}<a class="btn primary" href="{{.Discord}}" rel="noopener">Join the Discord</a>{{end}}
<a class="btn sec" href="/status">Live queue</a>
</div>

<p class="note">Boost Link is a fan project, not affiliated with Bandai Namco or Sunrise. Matches are played directly between players; this server only introduces you.</p>
</main>
<div class="band cy"></div>
<script>
async function tick(){
  try{
    const r=await fetch('/status.json',{cache:'no-store'}); if(!r.ok) return;
    const s=await r.json();
    document.getElementById('searching').textContent=s.searching;
    document.getElementById('dot').className='on'+(s.searching>0?' live':'');
    document.getElementById('matched').textContent=(s.recentRooms||[]).filter(x=>!x.test&&x.ageSeconds<3600).length;
    const res=s.results||[]; const ok=res.filter(x=>x.connected).length;
    document.getElementById('ok').textContent=res.length?ok+'/'+res.length:'–';
  }catch(e){}
}
tick(); setInterval(tick,5000);
</script>
</body></html>`))

func (m *Matcher) HomeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "" || host == "localhost" || strings.HasPrefix(host, "127.") {
			host = "this server's address"
		}
		d := homeData{Host: host, Tagline: m.cfg.SiteTagline, RoomSize: m.cfg.RoomSize}
		if strings.HasPrefix(m.cfg.DiscordInvite, "https://") {
			d.Discord = m.cfg.DiscordInvite
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = homePage.Execute(w, d)
	})
}
