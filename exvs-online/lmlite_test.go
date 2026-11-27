package main

import (
	"net"
	"testing"
	"time"
)

type echoResponder struct{ got chan []byte }

func (e *echoResponder) Connected(c *lmCab) []lmOut {
	return []lmOut{{Kind: 2, Payload: ulgMsg(3, 0, 240)}}
}
func (e *echoResponder) Message(c *lmCab, kind byte, p []byte) []lmOut {
	e.got <- p
	id, _, _ := ulgHeader(p)
	return []lmOut{{Kind: kind, Payload: ulgMsg(id, 1, 3688)}} // big reply: fragmented
}
func (e *echoResponder) Tick(*lmCab, time.Time) []lmOut { return nil }

func ulgMsg(id, flag uint16, n int) []byte {
	b := make([]byte, n)
	put32(b, 0, uint32(n))
	b[4], b[5] = byte(id), byte(id>>8)
	b[6] = byte(flag)
	return b
}

// A fake game on 127.0.0.1 links up, sends one message, and gets the boot push and a
// fragmented reply back, acking as it goes.
func TestLightLMSession(t *testing.T) {
	e := &echoResponder{got: make(chan []byte, 4)}
	lm := &LightLM{IP: net.IPv4(127, 0, 0, 1), Responder: e, Logf: t.Logf}
	if err := lm.Start(); err != nil {
		t.Skip("can't bind LM ports here:", err)
	}
	defer lm.Stop()
	cab, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 48003})
	if err != nil {
		t.Skip(err)
	}
	defer cab.Close()
	lmAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: lmSessionPort}
	connect := []byte{recLink, linkConnect, 0, 0, 1, 0, 0, 0}
	cab.WriteToUDP(connect, lmAddr)

	var fa fragAssembler
	accepted, push, big := false, false, false
	var ackTo uint32
	deadline := time.Now().Add(3 * time.Second)
	buf := make([]byte, 65536)
	sent := false
	for time.Now().Before(deadline) && !(accepted && push && big) {
		cab.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := cab.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		recs, _, _ := lmDecode(buf[:n])
		for _, r := range recs {
			switch {
			case r.Type == recLink && r.Sub == linkAccept:
				accepted = true
			case r.Type == recMsg:
				if id, _, _ := ulgHeader(r.Payload); id == 3 {
					push = true
				}
				ackTo = r.Seq
			case r.Type == recFrag:
				if w, ok := fa.add(r); ok && len(w) == 3688 {
					big = true
					ackTo = r.Seq
				}
			}
		}
		if accepted && !sent {
			sent = true
			cab.WriteToUDP(encMsg(1, 1, 2, ulgMsg(10, 0, 40)), lmAddr)
		}
		cab.WriteToUDP(encAck(1, ackTo), lmAddr)
	}
	if !accepted || !push || !big {
		t.Fatalf("accepted=%v push=%v big=%v", accepted, push, big)
	}
	select {
	case p := <-e.got:
		if id, _, _ := ulgHeader(p); id != 10 {
			t.Fatalf("got ULG %d", id)
		}
	default:
		t.Fatal("responder never saw the message")
	}
	if lm.Cabs() != 1 {
		t.Fatalf("cabs = %d", lm.Cabs())
	}
}
