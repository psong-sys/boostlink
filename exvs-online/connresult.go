package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"exvsonline/pb/exvs"
)

// ConnResult is a plain-English summary of one CHECK_COMMUNICATION report, which the game
// sends after every matching attempt. It holds no IPs, serials or shop ids.
type ConnResult struct {
	At        time.Time `json:"at"`
	From      string    `json:"from"` // "host PC" or "player PC"
	Try       uint32    `json:"try"`
	Found     bool      `json:"found"`
	Result    string    `json:"result"`  // UdpHpResultType name without the UDPHP_ prefix
	Meaning   string    `json:"meaning"` // what happened
	Fix       string    `json:"fix,omitempty"`
	NAT       string    `json:"nat"` // short NAT description
	RTTs      []uint32  `json:"rtts,omitempty"`
	Connected bool      `json:"connected"`
	ApplyID   uint32    `json:"applyId,omitempty"` // ties a failure back to the pair (not personal)
}

type hpInfo struct{ meaning, fix string }

var hpText = map[exvs.UdpHpResultType]hpInfo{
	exvs.UdpHpResultType_UDPHP_SUCCESS: {"Connected to the other player.", ""},
	exvs.UdpHpResultType_UDPHP_FAIL_CANCELED_MYSELF: {"This PC stopped the attempt (search cancelled, or the timer ran out) before it connected.",
		"Nothing to fix unless it keeps happening; then queue again on both PCs at the same time."},
	exvs.UdpHpResultType_UDPHP_FAIL_CANCELED_SOMEONE: {"The other player cancelled or dropped out before the connection finished.",
		"Check the other PC's RESULT line, it usually holds the real reason."},
	exvs.UdpHpResultType_UDPHP_FAIL_TIMEOUT: {"Matched, but the two games could not reach each other in time.",
		"Allow vsac29_Release.exe through Windows Firewall (private and public) on both PCs. One timeout is normal: the game retries by itself. If every try fails, run Check my PC on both PCs."},
	exvs.UdpHpResultType_UDPHP_FAIL_TIMEOUT_NO_PACKETS: {"Matched, but not a single packet arrived from the other player.",
		"Usually a firewall or the wrong network adapter. Allow the game through Windows Firewall on both PCs, and check both use their real Ethernet or Wi-Fi adapter (not ZeroTier or Radmin)."},
	exvs.UdpHpResultType_UDPHP_FAIL_RTT_OUT_OF_LIMIT: {"Connected, but the ping was over the game's limit, so it dropped the match.",
		"The route between the PCs is too slow. The host can raise rttLimitMs in its config to allow it (with more input delay)."},
	exvs.UdpHpResultType_UDPHP_FAIL_OTHERS: {"The connection failed for a reason the game does not name.",
		"Send both PCs' traffic.log and live-monitor.log."},
	exvs.UdpHpResultType_UDPHP_NONE: {"No connection was tried.", ""},
}

func natText(st *exvs.Request_CheckCommunication_StunResult) string {
	if st == nil {
		return "unknown"
	}
	m, f := st.GetNatMappingType(), st.GetNatFilteringType()
	var mt string
	switch m {
	case exvs.Request_CheckCommunication_StunResult_NM_NONE:
		mt = "mapping not measured"
	case exvs.Request_CheckCommunication_StunResult_NM_ENDPOINT_INDEPENDENT:
		mt = "easy NAT"
	default:
		mt = "strict NAT (hard to punch through)"
	}
	var ft string
	switch f {
	case exvs.Request_CheckCommunication_StunResult_NF_NONE:
		ft = "no filtering info"
	case exvs.Request_CheckCommunication_StunResult_NF_ENDPOINT_INDEPENDENT:
		ft = "open filtering"
	case exvs.Request_CheckCommunication_StunResult_NF_ADDRESS_DEPENDENT:
		ft = "filters by address"
	default:
		ft = "filters by address and port (normal home router)"
	}
	return mt + ", " + ft
}

func summarizeCheck(c *exvs.Request_CheckCommunication, from string) ConnResult {
	r := ConnResult{At: time.Now(), From: from, Try: c.GetMatchingTryNum(), ApplyID: c.GetApplyId(), Found: c.GetFoundPlayerFlag(), NAT: natText(c.GetStunResult())}
	hp := c.GetUdpHpResult()
	if c.UdpHpResult == nil {
		hp = exvs.UdpHpResultType_UDPHP_NONE
	}
	r.Result = strings.TrimPrefix(hp.String(), "UDPHP_")
	r.Connected = hp == exvs.UdpHpResultType_UDPHP_SUCCESS
	info, ok := hpText[hp]
	if !ok {
		info = hpInfo{"Unknown result code " + hp.String() + ".", "Send both PCs' traffic.log."}
	}
	r.Meaning, r.Fix = info.meaning, info.fix
	if !r.Found && hp == exvs.UdpHpResultType_UDPHP_NONE {
		r.Meaning = "Nobody was matched on this try."
		r.Fix = "Both players must search at the same time, with the same host IP. Check the status page to see who is queued."
	}
	for _, p := range c.GetPlayers() {
		r.RTTs = append(r.RTTs, p.GetRtt())
	}
	return r
}

// Line is the one-line form written to traffic.log and the console.
func (r ConnResult) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "try %d: ", r.Try)
	if r.Found {
		b.WriteString("opponent found. ")
	} else {
		b.WriteString("no opponent. ")
	}
	fmt.Fprintf(&b, "%s: %s", r.Result, r.Meaning)
	if r.Fix != "" {
		fmt.Fprintf(&b, " FIX: %s", r.Fix)
	}
	fmt.Fprintf(&b, " NAT: %s.", r.NAT)
	if len(r.RTTs) > 0 {
		fmt.Fprintf(&b, " Ping (ms): %v, limit %d.", r.RTTs, rttLimitNow())
	}
	return b.String()
}

// addResult keeps the last 20 results for the status page.
func (m *Matcher) addResult(r ConnResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.noteResultLocked(r)
	m.results = append(m.results, r)
	if len(m.results) > 20 {
		m.results = m.results[len(m.results)-20:]
	}
}

// ReportHandler accepts a ConnResult from a player's proxy (POST /report).
func (m *Matcher) ReportHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var cr ConnResult
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&cr); err != nil {
			http.Error(w, "bad report", http.StatusBadRequest)
			return
		}
		cr.At = time.Now()
		cr.From = "player PC"
		clip := func(s string, n int) string {
			if len(s) > n {
				return s[:n]
			}
			return s
		}
		cr.Result, cr.Meaning, cr.Fix, cr.NAT = clip(cr.Result, 40), clip(cr.Meaning, 300), clip(cr.Fix, 400), clip(cr.NAT, 120)
		if len(cr.RTTs) > 8 {
			cr.RTTs = cr.RTTs[:8]
		}
		m.addResult(cr)
		w.WriteHeader(http.StatusNoContent)
	})
}

// reportCheck logs the summary and hands it to the host's status page.
func (p *Proxy) reportCheck(c *exvs.Request_CheckCommunication) {
	from := "player PC"
	if p.local != nil {
		from = "host PC"
	}
	res := summarizeCheck(c, from)
	line := res.Line()
	p.tl.Line("RESULT", line)
	log.Printf("RESULT %s", line)
	if p.local != nil {
		p.local.addResult(res)
		return
	}
	go func() {
		b, _ := json.Marshal(res)
		u := *p.match
		u.Path = "/report"
		resp, err := p.client.Post(u.String(), "application/json", bytes.NewReader(b))
		if err != nil {
			return // older host without /report, or host down: the local log still has it
		}
		resp.Body.Close()
	}()
}
