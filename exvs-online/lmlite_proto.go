package main

// Light Live Monitor: wire format.
//
// The game (a "cab") needs a Live Monitor (LM) on its LAN. The real one is a second
// full copy of the game. Light LM answers the few things a cab asks it, inside
// exvs-online. The layouts here come from our own recordings of the game; the idea of
// a stand-in and some of the field notes come from ServerOmega's public protocol
// notes (github.com/ServerOmega/exvs2-lm-standin, docs/PROTOCOL.md). No code from it.
//
// A datagram on the session ports is a list of records:
//
//	c4: reliable message, 20 B header + len B payload
//	    [c4 0 0 0][seq u32][ch u8 kind u8 0 0][len u32][ch u32][payload]
//	c0: one fragment of a reliable message over 512 B
//	    [c0 0 0 0][seq][ch kind 0 0][idx][count][total][fragLen][0][ch][data]
//	c1: link control; sub 1 ack (12 B), 2 connect (8), 3 accept (36), 5 ping/pong (24)
//
// Every c4/c0 is acked by a cumulative c1 01 on its channel. Channel 1 is cab<->LM.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	recMsg  = 0xc4
	recFrag = 0xc0
	recLink = 0xc1

	linkAck     = 1
	linkConnect = 2
	linkAccept  = 3
	linkPing    = 5

	lmFragData   = 512
	lmMsgHdrLen  = 20
	lmFragHdrLen = 36
)

// lmRecord is one decoded record of a session datagram.
type lmRecord struct {
	Type byte // recMsg, recFrag or recLink
	Sub  byte // link sub-type (recLink)
	Ch   uint32
	Seq  uint32 // recMsg/recFrag: message seq; linkAck: acked seq; linkPing: counter
	Kind byte   // recMsg/recFrag

	// recFrag
	Idx, Count, Total uint32

	Payload []byte // recMsg payload, recFrag data, or link record body after ch
	Raw     []byte // the whole record
}

func le32(b []byte, o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }

func linkLen(sub byte) int {
	switch sub {
	case linkAck:
		return 12
	case linkConnect:
		return 8
	case linkAccept:
		return 36
	case linkPing:
		return 24
	}
	return 0
}

// lmDecode splits a session datagram into records. An unknown record ends the
// datagram (the rest is returned in tail) so a new record type is logged, not fatal.
func lmDecode(b []byte) (recs []lmRecord, tail []byte, err error) {
	for len(b) > 0 {
		if len(b) < 4 {
			return recs, b, errors.New("short record")
		}
		switch b[0] {
		case recMsg:
			if len(b) < lmMsgHdrLen {
				return recs, b, errors.New("short c4 header")
			}
			n := int(le32(b, 12))
			if n < 0 || lmMsgHdrLen+n > len(b) {
				return recs, b, fmt.Errorf("c4 len %d past end", n)
			}
			r := b[:lmMsgHdrLen+n]
			recs = append(recs, lmRecord{Type: recMsg, Seq: le32(r, 4), Ch: uint32(r[8]), Kind: r[9],
				Payload: r[lmMsgHdrLen:], Raw: r})
			b = b[len(r):]
		case recFrag:
			if len(b) < lmFragHdrLen {
				return recs, b, errors.New("short c0 header")
			}
			n := int(le32(b, 24))
			if n < 0 || lmFragHdrLen+n > len(b) {
				return recs, b, fmt.Errorf("c0 len %d past end", n)
			}
			r := b[:lmFragHdrLen+n]
			recs = append(recs, lmRecord{Type: recFrag, Seq: le32(r, 4), Ch: uint32(r[8]), Kind: r[9],
				Idx: le32(r, 12), Count: le32(r, 16), Total: le32(r, 20), Payload: r[lmFragHdrLen:], Raw: r})
			b = b[len(r):]
		case recLink:
			n := linkLen(b[1])
			if n == 0 || len(b) < n {
				return recs, b, fmt.Errorf("unknown or short c1 %02x", b[1])
			}
			r := b[:n]
			rec := lmRecord{Type: recLink, Sub: b[1], Ch: le32(r, 4), Payload: r[8:], Raw: r}
			if n >= 12 {
				rec.Seq = le32(r, 8)
			}
			recs = append(recs, rec)
			b = b[n:]
		default:
			return recs, b, fmt.Errorf("unknown record %02x", b[0])
		}
	}
	return recs, nil, nil
}

func put32(b []byte, o int, v uint32) { binary.LittleEndian.PutUint32(b[o:], v) }

func encMsg(seq uint32, ch byte, kind byte, payload []byte) []byte {
	b := make([]byte, lmMsgHdrLen+len(payload))
	b[0] = recMsg
	put32(b, 4, seq)
	b[8], b[9] = ch, kind
	put32(b, 12, uint32(len(payload)))
	put32(b, 16, uint32(ch))
	copy(b[lmMsgHdrLen:], payload)
	return b
}

// encFrags splits a large message into c0 fragments, one per datagram.
func encFrags(seq uint32, ch, kind byte, payload []byte) [][]byte {
	count := (len(payload) + lmFragData - 1) / lmFragData
	var out [][]byte
	for i := 0; i < count; i++ {
		d := payload[i*lmFragData:]
		if len(d) > lmFragData {
			d = d[:lmFragData]
		}
		b := make([]byte, lmFragHdrLen+len(d))
		b[0] = recFrag
		put32(b, 4, seq)
		b[8], b[9] = ch, kind
		put32(b, 12, uint32(i))
		put32(b, 16, uint32(count))
		put32(b, 20, uint32(len(payload)))
		put32(b, 24, uint32(len(d)))
		put32(b, 32, uint32(ch))
		copy(b[lmFragHdrLen:], d)
		out = append(out, b)
	}
	return out
}

func encAck(ch, seq uint32) []byte {
	b := make([]byte, 12)
	b[0], b[1] = recLink, linkAck
	put32(b, 4, ch)
	put32(b, 8, seq)
	return b
}

func encAccept(ch uint32, ip [4]byte, port uint16, tail uint32) []byte {
	b := make([]byte, 36)
	b[0], b[1] = recLink, linkAccept
	put32(b, 4, ch)
	copy(b[12:16], ip[:])
	binary.LittleEndian.PutUint16(b[28:], port)
	put32(b, 32, tail)
	return b
}

// encPing: ping=true is a ping, false a pong; echo is the 7 bytes the peer sent.
func encPing(ch, counter uint32, ping bool, echo []byte) []byte {
	b := make([]byte, 24)
	b[0], b[1] = recLink, linkPing
	put32(b, 4, ch)
	put32(b, 8, counter)
	if ping {
		b[16] = 1
	}
	copy(b[17:], echo)
	return b
}

// fragAssembler joins c0 fragments into whole messages.
type fragAssembler struct{ parts map[uint32][][]byte }

func (f *fragAssembler) add(r lmRecord) ([]byte, bool) {
	if f.parts == nil {
		f.parts = map[uint32][][]byte{}
	}
	if r.Count == 0 || r.Count > 64 || r.Idx >= r.Count {
		return nil, false
	}
	p := f.parts[r.Seq]
	if len(p) != int(r.Count) {
		p = make([][]byte, r.Count)
		f.parts[r.Seq] = p
	}
	p[r.Idx] = append([]byte(nil), r.Payload...)
	var out []byte
	for _, x := range p {
		if x == nil {
			return nil, false
		}
		out = append(out, x...)
	}
	delete(f.parts, r.Seq)
	return out, true
}

// ulgHeader reads a channel-1 kind-2 payload header: [size u32][id u16][flag u16].
func ulgHeader(p []byte) (id, flag uint16, ok bool) {
	if len(p) < 8 || int(le32(p, 0)) != len(p) {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint16(p[4:]), binary.LittleEndian.Uint16(p[6:]), true
}
