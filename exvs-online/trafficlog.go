package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// trafficLog writes every decoded request/response so a failed test run shows exactly
// which call the game stopped on.
type trafficLog struct {
	mu sync.Mutex
	f  *os.File
}

func newTrafficLog(path string, on bool) *trafficLog {
	if !on {
		return &trafficLog{}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("traffic log disabled: %v", err)
		return &trafficLog{}
	}
	log.Printf("logging decoded traffic to %s", path)
	return &trafficLog{f: f}
}

var pj = protojson.MarshalOptions{EmitUnpopulated: false, UseEnumNumbers: false, AllowPartial: true}

func (t *trafficLog) Msg(dir string, m proto.Message) {
	if t.f == nil || m == nil {
		return
	}
	b, err := pj.Marshal(m)
	if err != nil {
		b = []byte(fmt.Sprintf("<marshal error %v>", err))
	}
	t.Line(dir, string(b))
}

func (t *trafficLog) Line(dir, s string) {
	if t.f == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintf(t.f, "%s %s %s\n", time.Now().Format("15:04:05.000"), dir, s)
}
