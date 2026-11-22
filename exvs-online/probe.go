package main

import (
	_ "embed"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"
)

// probeSay prints probe progress. The automatic probe sends it to the log instead.
var probeSay = func(format string, a ...any) { log.Printf("probe: "+format, a...) }

//go:embed probe_vtables.txt
var probeVtables string

// hangWatch runs the game probe when the game goes quiet after saving a result.
// After an online match the game saves its result and then, on some setups, never
// leaves the result screen. A probe taken while it is stuck shows which step it waits in.
type hangWatch struct {
	mu    sync.Mutex
	timer *time.Timer
	runs  int
	dir   string
	tl    *trafficLog
	after time.Duration
}

func newHangWatch(dir string, tl *trafficLog) *hangWatch {
	return &hangWatch{dir: dir, tl: tl, after: 40 * time.Second}
}

// resultSaved arms the watch. Any later game request disarms it.
func (w *hangWatch) resultSaved() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	if w.runs >= 3 {
		return
	}
	w.timer = time.AfterFunc(w.after, w.fire)
}

func (w *hangWatch) activity() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.mu.Unlock()
}

func (w *hangWatch) fire() {
	w.mu.Lock()
	w.timer = nil
	w.runs++
	w.mu.Unlock()
	out := filepath.Join(w.dir, "hang-probe-"+time.Now().Format("20060102-150405")+".txt")
	if w.tl != nil {
		w.tl.Line("PROBE", fmt.Sprintf("game quiet %s after saving the result; probing", w.after))
	}
	err := runProbe(out)
	if w.tl != nil {
		if err != nil {
			w.tl.Line("PROBE", "failed: "+err.Error())
		} else {
			w.tl.Line("PROBE", "wrote "+filepath.Base(out))
		}
	}
}
