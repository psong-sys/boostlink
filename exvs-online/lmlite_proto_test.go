package main

import (
	"bytes"
	"testing"
)

func TestLMRecordsRoundTrip(t *testing.T) {
	ulg := make([]byte, 40)
	put32(ulg, 0, 40)
	ulg[4] = 22
	ulg[6] = 1
	var dg []byte
	dg = append(dg, encAck(1, 7)...)
	dg = append(dg, encMsg(8, 1, 2, ulg)...)
	dg = append(dg, encPing(1, 3, true, []byte{1, 2, 3, 4, 5, 6, 7})...)
	dg = append(dg, encAccept(1, [4]byte{192, 168, 1, 2}, 48001, 1)...)
	recs, tail, err := lmDecode(dg)
	if err != nil || tail != nil || len(recs) != 4 {
		t.Fatalf("decode: %v %x %d", err, tail, len(recs))
	}
	if recs[0].Sub != linkAck || recs[0].Seq != 7 {
		t.Errorf("ack %+v", recs[0])
	}
	if recs[1].Seq != 8 || recs[1].Kind != 2 || !bytes.Equal(recs[1].Payload, ulg) {
		t.Errorf("msg %+v", recs[1])
	}
	if id, flag, ok := ulgHeader(recs[1].Payload); !ok || id != 22 || flag != 1 {
		t.Errorf("ulg header %d %d %v", id, flag, ok)
	}
	if recs[3].Sub != linkAccept || recs[3].Payload[4] != 192 {
		t.Errorf("accept %x", recs[3].Raw)
	}
}

func TestLMFragments(t *testing.T) {
	big := make([]byte, 3688)
	for i := range big {
		big[i] = byte(i)
	}
	frags := encFrags(5, 1, 2, big)
	if len(frags) != 8 {
		t.Fatalf("%d frags", len(frags))
	}
	var fa fragAssembler
	var got []byte
	for i := len(frags) - 1; i >= 0; i-- { // out of order
		recs, _, err := lmDecode(frags[i])
		if err != nil || len(recs) != 1 {
			t.Fatal(err)
		}
		if w, ok := fa.add(recs[0]); ok {
			got = w
		}
	}
	if !bytes.Equal(got, big) {
		t.Fatal("reassembly mismatch")
	}
}

func TestLMDecodeUnknownRecord(t *testing.T) {
	dg := append(encAck(1, 1), 0xff, 0, 0, 0)
	recs, tail, err := lmDecode(dg)
	if err == nil || len(recs) != 1 || len(tail) != 4 {
		t.Fatalf("%v %d %x", err, len(recs), tail)
	}
}
