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
		w.entries[1].Parked = true
		next := s
		next.State = state
		w.observe(next, now.Add(3*time.Second))
		if w.entries[1].Parked || w.togglePark(next) {
			t.Fatalf("%s can stay parked", state)
		}
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
