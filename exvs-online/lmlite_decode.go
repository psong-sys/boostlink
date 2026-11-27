package main

// -lm-decode: prints a recording made by lm-record (JSON lines of UDP payloads
// between the game and the Live Monitor) as a readable conversation.

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type lmCapLine struct {
	T   float64 `json:"t"`
	If  string  `json:"if"`
	Src string  `json:"src"`
	Dst string  `json:"dst"`
	Hex string  `json:"hex"`
}

func lmPortOf(addr string) int {
	i := strings.LastIndexByte(addr, ':')
	var p int
	fmt.Sscanf(addr[i+1:], "%d", &p)
	return p
}

func runLMDecode(path string, w io.Writer, full bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	seen := map[string]bool{} // the recorder can see a packet on two interfaces
	frags := map[string]*fragAssembler{}
	for sc.Scan() {
		var l lmCapLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		key := l.Src + l.Dst + l.Hex
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 4096 {
			seen = map[string]bool{key: true}
		}
		b, err := hex.DecodeString(l.Hex)
		if err != nil {
			continue
		}
		sp, dp := lmPortOf(l.Src), lmPortOf(l.Dst)
		dir := "cab>lm"
		if sp == 48000 || (sp >= 49000 && sp <= 49015 && len(b) > 57 && b[56] == 2) {
			dir = "lm>cab"
		}
		if (sp >= 49000 && sp <= 49015) || (dp >= 49000 && dp <= 49015 && len(b) == 276) {
			if len(b) == 276 {
				fmt.Fprintf(w, "%8.3f %s announce role=%d idx=%d group=%d port=%d reply=%d\n", l.T, dir, b[56], b[58], le32(b, 48), binary.LittleEndian.Uint16(b[80:]), le32(b, 12))
			}
			continue
		}
		recs, tail, derr := lmDecode(b)
		for _, r := range recs {
			switch r.Type {
			case recLink:
				if r.Sub == linkAck || r.Sub == linkPing {
					continue // 60 Hz link noise
				}
				fmt.Fprintf(w, "%8.3f %s link %d ch%d %x\n", l.T, dir, r.Sub, r.Ch, r.Raw)
			case recFrag:
				fk := dir + fmt.Sprint(r.Seq)
				if frags[fk] == nil {
					frags[fk] = &fragAssembler{}
				}
				if whole, ok := frags[fk].add(r); ok {
					lmPrintMsg(w, l.T, dir, r.Seq, r.Ch, r.Kind, whole, full, "frag")
				}
			case recMsg:
				lmPrintMsg(w, l.T, dir, r.Seq, r.Ch, r.Kind, r.Payload, full, "")
			}
		}
		if derr != nil {
			fmt.Fprintf(w, "%8.3f %s ?? %v: %x\n", l.T, dir, derr, tail)
		}
	}
	return sc.Err()
}

func lmPrintMsg(w io.Writer, t float64, dir string, seq, ch uint32, kind byte, p []byte, full bool, note string) {
	id, flag, ok := ulgHeader(p)
	name := "?"
	if ok {
		if kind == 2 {
			name = fmt.Sprintf("ULG %d", id)
		} else {
			name = fmt.Sprintf("ctl %d", id)
		}
	}
	h := hex.EncodeToString(p)
	if !full && len(h) > 160 {
		h = h[:160] + "..."
	}
	fmt.Fprintf(w, "%8.3f %s seq%d ch%d kind%d %s flag%d %dB %s %s\n", t, dir, seq, ch, kind, name, flag, len(p), note, h)
}
