package main

import (
	"testing"
	"time"
)

func TestWatchShortcutWaitsForRelease(t *testing.T) {
	for _, held := range []uintptr{0x11, 0x5B, 'Z', '1'} {
		if watchShortcutReleased(func(key uintptr) bool { return key == held }) {
			t.Fatalf("activation allowed with key %x down", held)
		}
	}
	if !watchShortcutReleased(func(uintptr) bool { return false }) {
		t.Fatal("released chord blocked")
	}
}

func TestWatchSelectionSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	s := watchSettings{Shortcuts: "Ctrl+Alt+Shift", FirstRowShortcut: "Ctrl+Win+Z", DisableSelectionBlink: true, DisableForeground: true}
	if err := saveWatchSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	got := loadWatchSettings(dir)
	if !got.DisableSelectionBlink || !got.DisableForeground || got.Shortcuts != s.Shortcuts || got.FirstRowShortcut != s.FirstRowShortcut {
		t.Fatalf("settings lost: %+v", got)
	}
}

func TestWatchParkLifecycle(t *testing.T) {
	w := newWatchTracker(0)
	s := watchSample{Session: Session{PID: 1, State: "idle"}, Phase: "idle", RootStarted: "one", HWND: 1}
	active := s
	active.Phase = "active"
	if !watchCanPark(active) {
		t.Fatal("focused idle row hides park control")
	}
	now := time.Now()
	w.observe(s, now)
	w.observe(s, now.Add(time.Second))
	if !w.togglePark(s) || !w.entries[1].Parked {
		t.Fatal("idle was not parked")
	}
	w.observe(s, now.Add(2*time.Second))
	if !w.entries[1].Parked {
		t.Fatal("ordinary idle refresh unparked")
	}
	stale := s
	stale.RootStarted = "old"
	if w.togglePark(stale) || !w.entries[1].Parked {
		t.Fatal("stale identity changed parking")
	}
	for _, state := range []string{"busy", "prompt", "error"} {
		next := s
		next.State = state
		if w.observe(next, now.Add(3*time.Second)) || !w.entries[1].Parked || w.entries[1].Attention {
			t.Fatalf("%s lost parking or requested attention", state)
		}
		if !w.togglePark(next) || w.entries[1].Parked || !w.togglePark(next) {
			t.Fatalf("%s could not unpark and park", state)
		}
	}
}

func TestWatchParkRunningThroughInputAndCompletion(t *testing.T) {
	w := newWatchTracker(0)
	s := watchSample{Session: Session{PID: 1, AgentPID: 2, State: "busy"}, Phase: "working", RootStarted: "one", HWND: 1, DesktopID: "here"}
	now := time.Now()
	w.observe(s, now)
	if !w.togglePark(s) {
		t.Fatal("running session cannot be parked")
	}
	previous := watchPanelStates(w.panelRows())
	for _, phase := range []string{"question", "working", "finished"} {
		s.Phase = phase
		s.State, s.PendingQuestions, s.Working = "busy", 0, true
		if phase == "question" {
			s.State, s.PendingQuestions = "prompt", 1
		}
		if phase == "finished" {
			s.State, s.Working = "idle", false
		}
		for i := 0; i < 2; i++ {
			now = now.Add(time.Second)
			if w.observe(s, now) {
				t.Fatal("parked transition notified", phase)
			}
		}
		e := w.entries[1]
		if !e.Parked || e.Attention || len(w.rows()) != 0 {
			t.Fatal("parked transition entered attention queue", phase)
		}
		rows, _ := watchPanelLayout(w.panelRows(), "here", nil, nil)
		if len(rows) != 1 || !rows[0].Parked || rows[0].Sample.Phase != phase {
			t.Fatal("parked row lost live status", phase)
		}
		if watchPanelNeedsWake(previous, rows) {
			t.Fatal("parked transition reopened panel", phase)
		}
		if _, ok := watchFirstRowTarget(watchPanelFrame{Rows: rows}, "here"); ok {
			t.Fatal("parked transition selected by first-row shortcut", phase)
		}
	}
	// A newly discovered child still belongs to the parked console.
	s.AgentPID++
	w.observe(s, now.Add(time.Second))
	if !w.entries[1].Parked {
		t.Fatal("child discovery cleared parking")
	}
	// PID reuse must not park the replacement session.
	s.RootStarted = "replacement"
	w.observe(s, now.Add(2*time.Second))
	if w.entries[1].Parked {
		t.Fatal("parking leaked to replacement console")
	}
}

func TestWatchParkClearsAndRestoresInputSignals(t *testing.T) {
	w := newWatchTracker(0)
	s := watchSample{Session: Session{PID: 1, State: "prompt"}, Phase: "question", RootStarted: "one", HWND: 1, DesktopID: "here", PendingQuestions: 1, Working: true}
	w.observe(s, time.Now())
	flashes := watchFlashController{}
	on := false
	flash := func(_ uintptr, value bool) { on = value }
	flashes.update(w.entries, "here", flash)
	if !on || watchTaskbarState(w.entries[1], "here") != 8 {
		t.Fatal("input request did not signal")
	}
	if !w.togglePark(s) {
		t.Fatal("input request cannot be parked")
	}
	flashes.update(w.entries, "here", flash)
	flashes.update(w.entries, "away", flash)
	flashes.update(w.entries, "here", flash)
	if on || w.entries[1].Attention || watchTaskbarState(w.entries[1], "here") != 0 {
		t.Fatal("parked input kept or regained attention")
	}
	if !w.togglePark(s) {
		t.Fatal("input request cannot be unparked")
	}
	flashes.update(w.entries, "here", flash)
	if !on || watchTaskbarState(w.entries[1], "here") != 8 {
		t.Fatal("unparking did not restore input signals")
	}
}

func TestWatchParkOrderingAndShortcut(t *testing.T) {
	parked := watchEntry{Parked: true, Stopped: true, Sample: watchSample{Session: Session{PID: 1, State: "idle"}, DesktopID: "here"}}
	busy := watchEntry{Sample: watchSample{Session: Session{PID: 2, State: "busy"}, DesktopID: "here"}}
	rows, _ := watchPanelLayout([]watchEntry{parked, busy}, "here", nil, nil)
	if rows[1].Sample.PID != 1 {
		t.Fatal("parked row not last")
	}
	frame := watchPanelFrame{Rows: []watchEntry{parked, busy}}
	p, ok := watchFirstRowTarget(frame, "here")
	if !ok || p.Row.Sample.PID != 2 {
		t.Fatal("shortcut did not skip parked row")
	}
	frame.Rows = frame.Rows[:1]
	if _, ok := watchFirstRowTarget(frame, "here"); ok {
		t.Fatal("shortcut selected only parked row")
	}
	if _, ok := watchShortcutRow(frame, watchHotkeyBase); !ok {
		t.Fatal("explicit numbered shortcut lost")
	}
	parked.Sample.HWND = 10
	if watchActiveRows([]watchEntry{parked}, 10)[0].Sample.Phase == "active" {
		t.Fatal("focus hid PARKED")
	}
}
