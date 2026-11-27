package main

// Light Live Monitor: what to answer. Every rule here was read off a recording of the
// real Live Monitor and the new game build (card protocol 141) on one PC, 2026-11-27.
//
// Conventions seen in every message:
//   - channel-1 messages start [size u32][id u16][flag u16]; flag 1 = a reply
//   - the last 8 bytes are the sender's pointer to the request; a reply echoes it
//   - +16 of many LM messages is a constant the game checks against its own table
//   - kind 0 = link control ("ctl"), kind 2 = Live Monitor messages ("ULG"),
//     kind 4 = clock sync

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	kindCtl   = 0
	kindULG   = 2
	kindClock = 4

	// any non-zero value works as "our pointer"; the game only echoes it back
	lmPtr = uint64(0x00007ff775800140)
)

// LM-side constants the real Live Monitor put at +16 (and +12) of its messages.
const (
	lmK3  = 0x006d4fb0 // ULG 3 terminal info
	lmK4  = 0x006d6a80 // ULG 4 terminal state
	lmK5  = 0x006d6000 // ULG 5 card entry
	lmK6  = 0x006d6ee0 // ULG 6 entry prepare
	lmK15 = 0x006d5df0 // ULG 15 notice
	lmK0  = 0x0020e310 // ctl 0 request
	lmK0x = 0x0020e340 // ctl 0 before an Exit & Save answer
)

// lmMsg makes a payload of n bytes with the channel-1 header filled in.
func lmMsg(id, flag uint16, n int) []byte {
	b := make([]byte, n)
	put32(b, 0, uint32(n))
	binary.LittleEndian.PutUint16(b[4:], id)
	binary.LittleEndian.PutUint16(b[6:], flag)
	return b
}

func putTail(b []byte, v uint64) { binary.LittleEndian.PutUint64(b[len(b)-8:], v) }
func tailOf(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b[len(b)-8:])
}

// reply makes a reply to req: same id, flag 1, n bytes, request's tail echoed.
func reply(req []byte, n int) []byte {
	id := binary.LittleEndian.Uint16(req[4:])
	b := lmMsg(id, 1, n)
	putTail(b, tailOf(req))
	return b
}

// lmSettings are the Live Monitor's operator settings (29\testmode_lm.json).
type lmSettings map[string]int64

func (s lmSettings) get(key string, def int64) int64 {
	if v, ok := s[".TestModeParam."+key]; ok {
		return v
	}
	return def
}

// loadLMSettings reads 29\testmode_lm.json (an svd1 container: 36-byte header with a
// SHA-1, then JSON {"storage": {key: {"value": v}}}). Missing file = defaults.
func loadLMSettings(path string) (lmSettings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return lmSettings{}, err
	}
	if len(b) < 36 || string(b[:4]) != "svd1" {
		return lmSettings{}, errors.New("not an svd1 file")
	}
	n := binary.LittleEndian.Uint64(b[8:16])
	if n > uint64(len(b)-36) {
		return lmSettings{}, errors.New("svd1 size past end")
	}
	payload := b[36 : 36+n]
	h := sha1.New()
	h.Write(b[:16])
	h.Write(payload)
	if !bytes.Equal(h.Sum(nil), b[16:36]) {
		return lmSettings{}, errors.New("svd1 checksum mismatch")
	}
	var doc struct {
		Storage map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return lmSettings{}, err
	}
	s := lmSettings{}
	for k, v := range doc.Storage {
		if i, err := strconv.ParseInt(string(v.Value), 10, 64); err == nil {
			s[k] = i
		}
	}
	return s, nil
}

// lmBootInfo builds the ULG 10 reply (1136 B): the operator settings every game reads.
// Five "GAME PCBs SETTING" records of 156 B start at -24+156n (record 0's first bytes
// fall under the message header, which wins), then coin and tournament settings.
func lmBootInfo(s lmSettings, now time.Time) []byte {
	b := lmMsg(10, 1, 1136)
	set := func(off int, v int64) {
		if off >= 16 && off+4 <= len(b)-8 {
			put32(b, off, uint32(int32(v)))
		}
	}
	for n := 0; n < 5; n++ {
		base := -24 + 156*n
		f := fmt.Sprintf("gameServerPcb.settingFile.[%d].", n)
		def := func(k string, d int64) int64 { return s.get(f+k, d) }
		set(base+28, def("attractSound", 2))
		set(base+32, def("battleBgm", 0))
		set(base+36, def("coinCostSetting.continueCost", 1))
		set(base+40, def("coinCostSetting.plusCoinCost", 1))
		set(base+44, def("coinCostSetting.startCost", 1))
		set(base+48, def("coinCostSetting.timeRentalCost", 5))
		set(base+52, def("totalConnectionNum", 4))
		set(base+56, def("networkGroup", -1))
		set(base+60, def("useSettingType", 0))
		for g := 0; g < 3; g++ {
			gb := base + 64 + 40*g
			gk := fmt.Sprintf("group.[%d].", g)
			set(gb+0, def(gk+"connectionNum", 4))
			set(gb+4, def(gk+"battleMode", 0))
			set(gb+8, def(gk+"cpuMode", 0))
			set(gb+12, def(gk+"trainingMode", 0))
			set(gb+36, def(gk+"freePlay", 0))
		}
	}
	// after the records: the store-wide coin block, then tournament and clock
	set(784, 2)
	set(792, s.get("coin.coinCostSetting.continueCost", 1))
	set(796, s.get("coin.coinCostSetting.plusCoinCost", 1))
	set(800, 1)
	set(804, s.get("coin.coinCostSetting.timeRentalCost", 5))
	set(808, s.get("coin.remoteSetup", 1))
	set(960, 1)
	set(964, 1)
	set(980, 1)
	set(1040, s.get("livemonitor.tournamentSetting.entryMaxTeam", 16))
	set(1044, s.get("livemonitor.tournamentSetting.live", 1))
	set(1064, s.get("livemonitor.tournamentSetting.entryHour", -1))
	set(1072, s.get("livemonitor.tournamentSetting.startHour", -1))
	set(1084, s.get("closeTimeSetting.timezone", 16))
	set(1096, s.get("livemonitor.tournamentSetting.sound", 2))
	set(1100, 1)
	// the header wins over record 0
	put32(b, 8, 0)
	put32(b, 12, 0)
	put32(b, 16, uint32(now.Unix()))
	put32(b, 20, 0)
	put32(b, 24, 0)
	return b
}

// lmResponse answers one game the way the real Live Monitor does.
type lmResponse struct {
	Settings lmSettings
	Serial   string            // 12-digit LM serial
	Replay   map[uint16][]byte // debug: recorded real replies by ULG id
	Logf     func(string, ...any)

	lm *LightLM
	mu sync.Mutex
}

func (r *lmResponse) logf(f string, a ...any) {
	if r.Logf != nil {
		r.Logf(f, a...)
	}
}

func (r *lmResponse) lmID() uint32 {
	ip := r.lm.IP.To4()
	return 0x00026440 | uint32(ip[3])<<24
}

func (r *lmResponse) Connected(c *lmCab) []lmOut           { return nil }
func (r *lmResponse) Tick(c *lmCab, now time.Time) []lmOut { return nil }

func (r *lmResponse) Message(c *lmCab, kind byte, p []byte) []lmOut {
	switch kind {
	case kindCtl:
		return r.ctl(c, p)
	case kindClock:
		return r.clock(c, p)
	case kindULG:
		return r.ulg(c, p)
	}
	r.logf("game %d: unknown message kind %d (%d B)", c.Index, kind, len(p))
	return nil
}

func (r *lmResponse) ctl(c *lmCab, p []byte) []lmOut {
	id, flag, ok := ulgHeader(p)
	if !ok || flag == 1 {
		return nil // an answer to one of ours
	}
	switch id {
	case 2: // hello: +28 is the game's terminal id
		if len(p) >= 32 {
			c.ID = le32(p, 28)
		}
		b := reply(p, 40)
		put32(b, 20, 0x12)
		put32(b, 24, 1)
		return []lmOut{{kindCtl, b}}
	case 13:
		return []lmOut{{kindCtl, r.ctl0(c.ID, lmK0, 0)}, {kindCtl, r.ctl0(r.lmID(), lmK0, 0)}, {kindCtl, ctlOK(p)}}
	case 3: // test menu Exit & Save
		r.logf("game %d: Exit & Save", c.Index)
		st := r.state(c, 3, 0, 1)
		return []lmOut{{kindCtl, r.ctl0(c.ID, lmK0x, 1)}, {kindCtl, ctlOK(p)}, {kindULG, st}, {kindULG, appAck(tailOf(p))}}
	case 0:
		b := reply(p, 32)
		put32(b, 12, 1)
		return []lmOut{{kindCtl, b}}
	}
	r.logf("game %d: unknown control %d (%d B), said OK", c.Index, id, len(p))
	return []lmOut{{kindCtl, ctlOK(p)}}
}

func ctlOK(req []byte) []byte {
	b := reply(req, 32)
	put32(b, 16, 1)
	return b
}

func (r *lmResponse) ctl0(target, k uint32, v12 uint32) []byte {
	b := lmMsg(0, 0, 40)
	put32(b, 12, v12)
	put32(b, 16, k)
	put32(b, 24, target)
	putTail(b, lmPtr)
	return b
}

// clock: the game sends two 24 B records (aimed at itself and at the LM) holding its
// own object pointers; the LM answers each with its own. The values are opaque to the
// game beyond being echoed, so ours are fixed.
func (r *lmResponse) clock(c *lmCab, p []byte) []lmOut {
	if len(p) != 24 {
		return nil // the game's own part list (128 B) is never answered
	}
	target := le32(p, 12)
	n, a, b8 := 40, uint64(0x0000_0202_552b_b502), uint64(0x54fc93c0)
	if target != c.ID {
		n, a, b8 = 128, 0x0000_0202_62f6_6002, 0x552e9040
	}
	b := make([]byte, n)
	binary.LittleEndian.PutUint64(b[0:], a)
	binary.LittleEndian.PutUint64(b[8:], b8)
	put32(b, 20, target)
	copy(b[24:32], p[16:24])
	if n == 128 { // the LM's software parts: (part, version) pairs
		put32(b, 32, 11)
		put32(b, 36, 0x202)
		pairs := [][2]uint32{{0, 0x201}, {4, 0x240}, {5, 0x264}, {6, 0x202}, {7, 0x208}, {8, 0x280}, {9, 0x2bb}, {0x10, 0x2c0}, {0x11, 0x2a8}, {0x12, 0x201}, {0x13, 0x208}}
		for i, pr := range pairs {
			put32(b, 40+8*i, pr[0])
			put32(b, 44+8*i, pr[1])
		}
	}
	return []lmOut{{kindClock, b}}
}

func appAck(tail uint64) []byte {
	b := lmMsg(31, 1, 32)
	putTail(b, tail)
	return b
}

func (r *lmResponse) terminalInfo(c *lmCab) []byte {
	b := lmMsg(3, 0, 240)
	put32(b, 12, c.ID)
	put32(b, 16, lmK3)
	put32(b, 24, c.ID)
	copy(b[36:40], c.Addr.IP.To4())
	put32(b, 52, uint32(c.Addr.Port))
	put32(b, 76, 0xffffffff)
	put32(b, 84, 0x10000)
	putTail(b, lmPtr)
	return b
}

func (r *lmResponse) state(c *lmCab, st byte, peer uint32, handle uint32) []byte {
	b := lmMsg(4, 0, 56)
	put32(b, 12, 0xa5)
	put32(b, 16, lmK4)
	put32(b, 24, c.ID)
	b[28] = st
	put32(b, 36, peer)
	put32(b, 40, handle)
	putTail(b, lmPtr)
	return b
}

func (r *lmResponse) ulg(c *lmCab, p []byte) []lmOut {
	id, flag, ok := ulgHeader(p)
	if !ok {
		r.logf("game %d: bad message (%d B)", c.Index, len(p))
		return nil
	}
	if flag == 1 {
		return nil // 31 (app ack) and other answers
	}
	if rp := r.Replay[id]; rp != nil && (id == 10 || id == 16 || id == 9) {
		r.logf("game %d: answering %d with the recorded reply", c.Index, id)
		b := withTail(append([]byte(nil), rp...), tailOf(p))
		if id == 9 {
			return []lmOut{{kindULG, r.terminalInfo(c)}, {kindULG, b}}
		}
		return []lmOut{{kindULG, b}}
	}
	switch id {
	case 10: // boot info
		return []lmOut{{kindULG, withTail(lmBootInfo(r.Settings, time.Now()), tailOf(p))}}
	case 16: // LM info: serial + store coin record
		b := reply(p, 640)
		copy(b[12:24], []byte(r.Serial))
		for i, v := range []int64{2, 0, 1, 1, 1, 5, 1} {
			put32(b, 76+4*i, uint32(v))
		}
		put32(b, 588, 4)
		return []lmOut{{kindULG, b}}
	case 17: // schedule
		return []lmOut{{kindULG, reply(p, 32)}}
	case 9: // terminal table; the LM first introduces the game to itself
		if len(p) >= 28 && c.ID == 0 {
			c.ID = le32(p, 24)
		}
		out := []lmOut{{kindULG, r.terminalInfo(c)}, {kindULG, r.terminalTable(c, p)}}
		// in a store with more games, everyone hears about the newcomer and vice versa
		for _, o := range r.lm.others(c) {
			r.lm.sendTo(o, lmOut{kindULG, r.terminalInfo(c)})
			out = append(out, lmOut{kindULG, r.terminalInfo(o)})
		}
		return out
	case 22: // software version
		b := reply(p, 88)
		copy(b[16:20], r.lm.IP.To4())
		put32(b, 32, lmSessionPort)
		if len(p) >= 72 {
			copy(b[36:56], p[52:72])
		}
		return []lmOut{{kindULG, b}}
	case 11: // card inserted
		r.logf("game %d: card inserted", c.Index)
		e := append([]byte(nil), p...)
		binary.LittleEndian.PutUint16(e[4:], 5)
		copy(e[12:16], p[28:32])
		put32(e, 16, lmK5)
		put32(e, 64, 1)
		put32(e, 68, 0)
		putTail(e, lmPtr)
		ok := reply(p, 32)
		put32(ok, 12, 2)
		for _, o := range r.lm.others(c) {
			r.lm.sendTo(o, lmOut{kindULG, e})
		}
		return []lmOut{{kindULG, e}, {kindULG, ok}, {kindULG, r.notice()}}
	case 12: // entry prepare: echoed back as ULG 6
		e := append([]byte(nil), p...)
		binary.LittleEndian.PutUint16(e[4:], 6)
		put32(e, 12, 0x203)
		put32(e, 16, lmK6)
		putTail(e, lmPtr)
		return []lmOut{{kindULG, e}, {kindULG, appAck(tailOf(p))}}
	case 8: // the game's state
		st := byte(0)
		if len(p) > 28 {
			st = p[28]
		}
		var out []lmOut
		var s4 []byte
		switch st {
		case 2:
			s4 = r.state(c, 2, 0, 0xffffffff)
		case 6, 8:
			s4 = r.state(c, 11, r.lmID(), 0xffffffff)
		case 11:
			s4 = r.state(c, 3, 0, 1)
		}
		if s4 != nil {
			if len(p) > 29 {
				s4[29] = p[29]
			}
			out = append(out, lmOut{kindULG, s4})
			for _, o := range r.lm.others(c) {
				r.lm.sendTo(o, lmOut{kindULG, s4})
			}
		}
		return append(out, lmOut{kindULG, appAck(tailOf(p))})
	case 24, 25, 26:
		r.logf("game %d: replay request %d, not kept", c.Index, id)
		return []lmOut{{kindULG, appAck(tailOf(p))}}
	}
	r.logf("game %d: unknown request %d (%d B), acked", c.Index, id, len(p))
	return []lmOut{{kindULG, appAck(tailOf(p))}}
}

func withTail(b []byte, t uint64) []byte { putTail(b, t); return b }

// terminalTable: ULG 9 reply (3688 B). The game reads its own entry and the settings
// block; the rest of the real LM's reply is leftover memory, sent here as zeros.
func (r *lmResponse) terminalTable(c *lmCab, req []byte) []byte {
	b := reply(req, 3688)
	put32(b, 16, c.ID)
	copy(b[28:32], c.Addr.IP.To4())
	put32(b, 44, uint32(c.Addr.Port))
	put32(b, 68, 0xffffffff)
	put32(b, 76, 0x10000)
	boot := lmBootInfo(r.Settings, time.Now())
	put32(b, 220, le32(boot, 16))
	copy(b[232:588], boot[28:384]) // records 0-2; the real LM's leftover memory follows
	for _, w := range lmTableWords {
		put32(b, int(w[0]), w[1])
	}
	for _, off := range []int{780, 924, 1704, 2336} { // the LM's serial, in each LM-info copy
		copy(b[off:off+12], []byte(r.Serial))
	}
	return b
}

// notice: ULG 15, sent after a card goes in (times and the replay file URL).
func (r *lmResponse) notice() []byte {
	b := lmMsg(15, 0, 456)
	put32(b, 16, lmK15)
	put32(b, 24, 300)
	put32(b, 28, 5)
	put32(b, 32, 300)
	put32(b, 36, 300)
	copy(b[216:], "http://127.0.0.1/file/")
	putTail(b, lmPtr)
	return b
}

// lmTableWords: the non-pointer words of the real LM's ULG 9 reply after its first
// three settings records (more settings and LM-info copies; read off the 2026-11-27
// recording). Pointers into the LM's own memory are left out (zero).
var lmTableWords = [][2]uint32{
	{636, 0x4}, {676, 0x2}, {684, 0x1}, {688, 0x1}, {692, 0x1}, {696, 0x5}, {700, 0x4},
	{704, 0xffffffff}, {712, 0x4}, {752, 0x4}, {796, 0xc}, {804, 0xf}, {832, 0x2}, {840, 0x1},
	{852, 0x5}, {856, 0x4}, {904, 0x1}, {988, 0x2}, {996, 0x1}, {1000, 0x1}, {1004, 0x2},
	{1020, 0x40}, {1036, 0x8}, {1052, 0x1}, {1068, 0x38}, {1076, 0x4}, {1084, 0x4},
	{1088, 0x202}, {1100, 0x1}, {1156, 0x10}, {1196, 0x280}, {1236, 0x6}, {1396, 0xc0},
	{1400, 0x6}, {1404, 0x201}, {1408, 0x1}, {1412, 0x2}, {1416, 0x280}, {1420, 0x80},
	{1436, 0x6}, {1440, 0x201}, {1444, 0xa4}, {1460, 0x55555555}, {1516, 0x10}, {1540, 0x10010},
	{1580, 0x6}, {1588, 0x201}, {1596, 0x55555555}, {1636, 0x4}, {1644, 0x4}, {1660, 0x278},
	{1664, 0x1}, {1672, 0x4}, {1684, 0x4}, {1768, 0x2}, {1776, 0x1}, {1780, 0x1}, {1784, 0x1},
	{1788, 0x5}, {1792, 0x1}, {2280, 0x4}, {2400, 0x2}, {2408, 0x1}, {2412, 0x1}, {2416, 0x1},
	{2420, 0x5}, {2424, 0x1}, {2844, 0x1}, {2860, 0x40}, {2876, 0x8}, {2892, 0x201},
	{2908, 0x38}, {2916, 0x4}, {2924, 0x4}, {2928, 0x203}, {2940, 0x1}, {2972, 0x34},
	{2988, 0x4}, {2996, 0x10}, {3036, 0x20}, {3100, 0x20}, {3116, 0x34}, {3140, 0x7},
	{3228, 0x7}, {3244, 0x34}, {3276, 0xc4}, {3280, 0x1}, {3284, 0x201}, {3288, 0x20},
	{3340, 0x7}, {3344, 0x201}, {3348, 0x34}, {3364, 0x55555555}, {3420, 0x11}, {3484, 0x7},
	{3500, 0x55555555}, {3532, 0x40}, {3620, 0x20}, {3624, 0x10011}, {3628, 0x40},
}
