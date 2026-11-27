package main

// Debug aid: EXVS_LM_REPLAY=<lm-capture-*.jsonl> (or lm-replay.jsonl next to
// exvs-online.exe) makes the Light LM answer boot
// questions (ULG 10, 16, 9) with the real Live Monitor's recorded replies, so a failing
// field can be found by elimination. Never used unless that variable is set.

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
)

func loadLMReplay(path string) (map[uint16][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	out := map[uint16][]byte{}
	asm := map[uint32]*fragAssembler{}
	for sc.Scan() {
		var l lmCapLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if lmPortOf(l.Src) != lmSessionPort || lmPortOf(l.Dst) == lmSessionPort {
			continue
		}
		b, _ := hex.DecodeString(l.Hex)
		recs, _, _ := lmDecode(b)
		for _, r := range recs {
			var p []byte
			switch r.Type {
			case recMsg:
				p = r.Payload
			case recFrag:
				if asm[r.Seq] == nil {
					asm[r.Seq] = &fragAssembler{}
				}
				w, ok := asm[r.Seq].add(r)
				if !ok {
					continue
				}
				p = w
			default:
				continue
			}
			if r.Kind != kindULG {
				continue
			}
			if id, flag, ok := ulgHeader(p); ok && flag == 1 && out[id] == nil {
				out[id] = append([]byte(nil), p...)
			}
		}
	}
	return out, sc.Err()
}
