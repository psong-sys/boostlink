package main

import (
	"testing"
	"time"
)

func TestHangWatchFiresOnlyWhenQuiet(t *testing.T) {
	w := newHangWatch(t.TempDir(), &trafficLog{})
	w.after = 30 * time.Millisecond
	w.resultSaved()
	w.activity() // the game moved on
	time.Sleep(80 * time.Millisecond)
	if w.runs != 0 {
		t.Fatalf("probe ran although the game kept talking")
	}
	w.resultSaved()
	time.Sleep(80 * time.Millisecond)
	w.mu.Lock()
	runs := w.runs
	w.mu.Unlock()
	if runs != 1 {
		t.Fatalf("probe should run once when the game goes quiet, ran %d", runs)
	}
	var nilWatch *hangWatch
	nilWatch.resultSaved() // a proxy without a watch must not crash
	nilWatch.activity()
}
