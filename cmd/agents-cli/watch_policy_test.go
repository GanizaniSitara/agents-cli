package main

import "testing"

func TestWatchPolicyInterruptOncePerEpisode(t *testing.T) {
	a := watchPolicyAttempts{}
	s := watchSample{Session: Session{PID: 10, State: "error"}, RootStarted: "first", Phase: "blocked"}
	if a.ready(s, false) {
		t.Fatal("disabled setting interrupted")
	}
	if !a.ready(s, true) {
		t.Fatal("new block missed")
	}
	a[s.PID] = s.RootStarted
	if a.ready(s, true) {
		t.Fatal("repeated blocked frame interrupted twice")
	}
	s.State, s.Phase = "unknown", "unknown"
	a.ready(s, true)
	s.State, s.Phase = "error", "blocked"
	if a.ready(s, true) {
		t.Fatal("reader reconnect repeated interruption")
	}
	s.State, s.Phase = "busy", "working"
	a.ready(s, true)
	s.State, s.Phase = "error", "blocked"
	if !a.ready(s, true) {
		t.Fatal("new episode suppressed")
	}
	a[s.PID] = s.RootStarted
	s.RootStarted = "replacement"
	if !a.ready(s, true) {
		t.Fatal("new process inherited old attempt")
	}
}

func TestWatchOptionalActionsSettings(t *testing.T) {
	dir := t.TempDir()
	s := loadWatchSettings(dir)
	if s.MovePointerOnShortcut || s.InterruptPolicyBlock {
		t.Fatal("intrusive options enabled by default")
	}
	s.MovePointerOnShortcut, s.InterruptPolicyBlock = true, true
	if err := saveWatchSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	loaded := loadWatchSettings(dir)
	if !loaded.MovePointerOnShortcut || !loaded.InterruptPolicyBlock {
		t.Fatal("options not persisted")
	}
}
