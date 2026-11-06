package main

import (
	"testing"
	"time"
)

func TestWatchStableTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	w := newWatchTracker(3 * time.Second)
	s := watchSample{Session: Session{PID: 1, Started: "a", State: "busy"}, HWND: 9}
	step := func(d time.Duration, state, sig string) bool {
		s.State, s.Signature = state, sig
		return w.observe(s, now.Add(d))
	}
	step(0, "busy", "a")
	step(time.Second, "idle", "b")
	step(2*time.Second, "busy", "c")
	step(3*time.Second, "idle", "d")
	if step(5*time.Second, "idle", "d") {
		t.Fatal("brief pause notified")
	}
	if !step(6*time.Second, "idle", "d") {
		t.Fatal("stable completion missed")
	}
	if step(7*time.Second, "idle", "d") {
		t.Fatal("duplicate notification")
	}
	w.dismiss(1)
	if len(w.rows()) != 0 {
		t.Fatal("dismiss failed")
	}
	step(8*time.Second, "idle", "d")
	if len(w.rows()) != 0 {
		t.Fatal("dismissal did not stick")
	}
	step(9*time.Second, "busy", "e")
	step(10*time.Second, "prompt", "f")
	if !step(13*time.Second, "prompt", "f") {
		t.Fatal("new input wait missed")
	}
	step(14*time.Second, "busy", "g")
	if len(w.rows()) != 0 {
		t.Fatal("resumed work still displayed")
	}
}

func TestWatchBaselineUnknownChangingAndPIDReuse(t *testing.T) {
	now := time.Unix(100, 0)
	w := newWatchTracker(3 * time.Second)
	s := watchSample{Session: Session{PID: 1, Started: "a", State: "idle"}, HWND: 9, Signature: "a"}
	w.observe(s, now)
	if w.observe(s, now.Add(3*time.Second)) {
		t.Fatal("startup must not flash old idle windows")
	}
	if len(w.rows()) != 1 {
		t.Fatal("startup idle should appear in list")
	}
	s.State = "busy"
	w.observe(s, now.Add(4*time.Second))
	s.State = "idle"
	w.observe(s, now.Add(5*time.Second))
	s.Signature = "redraw"
	w.observe(s, now.Add(7*time.Second))
	if w.observe(s, now.Add(9*time.Second)) {
		t.Fatal("changing screen must reset debounce")
	}
	s.State = "unknown"
	w.observe(s, now.Add(10*time.Second))
	if len(w.rows()) != 0 {
		t.Fatal("unreadable window shown as stopped")
	}
	s.Started = "b"
	s.State = "idle"
	w.observe(s, now.Add(11*time.Second))
	if w.observe(s, now.Add(14*time.Second)) {
		t.Fatal("reused PID inherited busy state")
	}
	w.prune(nil)
	if len(w.rows()) != 0 {
		t.Fatal("closed window retained")
	}
}
