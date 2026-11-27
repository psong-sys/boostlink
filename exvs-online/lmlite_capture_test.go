package main

// Replays a real recording (LM_CAPTURE=lm-capture-*.jsonl, LM_SETTINGS=testmode_lm.json)
// through lmResponse and prints every word that differs from what the real Live
// Monitor sent. Skipped without a recording; recordings never go in the repo.

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
)

type capMsg struct {
	fromCab bool
	kind    byte
	p       []byte
}

func readCapture(t *testing.T, path string) (msgs []capMsg, cabPort int) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	seen := map[string]bool{}
	asm := map[string]*fragAssembler{}
	got := map[string]bool{}
	for sc.Scan() {
		var l lmCapLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || seen[l.Src+l.Dst+l.Hex] {
			continue
		}
		seen[l.Src+l.Dst+l.Hex] = true
		sp, dp := lmPortOf(l.Src), lmPortOf(l.Dst)
		if sp < 48000 || sp > 48016 || dp < 48000 || dp > 48016 || sp == dp {
			continue
		}
		fromCab := sp != lmSessionPort
		if fromCab {
			cabPort = sp
		}
		b, _ := hex.DecodeString(l.Hex)
		recs, _, _ := lmDecode(b)
		for _, r := range recs {
			key := fmt.Sprint(fromCab, r.Seq)
			if r.Type == recFrag {
				if asm[key] == nil {
					asm[key] = &fragAssembler{}
				}
				w, ok := asm[key].add(r)
				if !ok || got[key] {
					continue
				}
				got[key] = true
				msgs = append(msgs, capMsg{fromCab, r.Kind, w})
			} else if r.Type == recMsg && !got[key] {
				got[key] = true
				msgs = append(msgs, capMsg{fromCab, r.Kind, r.Payload})
			}
		}
	}
	return
}

func TestLightLMAgainstRecording(t *testing.T) {
	path := os.Getenv("LM_CAPTURE")
	if path == "" {
		t.Skip("set LM_CAPTURE to a recording")
	}
	settings, _ := loadLMSettings(os.Getenv("LM_SETTINGS"))
	msgs, port := readCapture(t, path)
	lm := &LightLM{IP: net.IPv4(192, 168, 1, 8), cabs: map[string]*lmCab{}}
	r := &lmResponse{Settings: settings, lm: lm, Logf: t.Logf}
	c := &lmCab{Addr: &net.UDPAddr{IP: lm.IP, Port: port}, Index: uint16(port - 48001), accepted: true}
	var real []capMsg
	for _, m := range msgs {
		if !m.fromCab {
			real = append(real, m)
		}
	}
	used := make([]bool, len(real))
	for _, m := range msgs {
		if !m.fromCab {
			continue
		}
		for _, o := range r.Message(c, m.kind, m.p) {
			id, _, _ := ulgHeader(o.Payload)
			match := -1
			for i, x := range real {
				xid, _, _ := ulgHeader(x.p)
				if !used[i] && x.kind == o.Kind && len(x.p) == len(o.Payload) && (o.Kind == kindClock || xid == id) {
					match = i
					break
				}
			}
			if match < 0 {
				t.Errorf("kind %d id %d (%d B): the real LM sent nothing like it", o.Kind, id, len(o.Payload))
				continue
			}
			used[match] = true
			x := real[match].p
			var diffs []string
			for i := 0; i+4 <= len(x)-8; i += 4 {
				a, b := binary.LittleEndian.Uint32(x[i:]), binary.LittleEndian.Uint32(o.Payload[i:])
				if a != b {
					diffs = append(diffs, fmt.Sprintf("+%d real %#x ours %#x", i, a, b))
				}
			}
			if id == 9 && o.Kind == 2 {
				first(diffs, 0)
				for _, d := range diffs {
					t.Log("  9:", d)
				}
			}
			t.Logf("kind %d id %d %dB: %d words differ %v", o.Kind, id, len(x), len(diffs), first(diffs, 12))
		}
	}
	for i, x := range real {
		if !used[i] {
			id, _, _ := ulgHeader(x.p)
			t.Logf("real LM also sent kind %d id %d (%d B)", x.kind, id, len(x.p))
		}
	}
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
