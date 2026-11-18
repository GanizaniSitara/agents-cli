package main

import "testing"

func TestNextAttentionShortcutSkipsActiveBusyParkedAndBackground(t *testing.T) {
	row := func(pid uint32, state, desktop string) watchEntry {
		return watchEntry{Sample: watchSample{Session: Session{PID: pid, State: state}, DesktopID: desktop, HWND: uintptr(pid)}}
	}
	rows := []watchEntry{row(1, "prompt", "here"), row(2, "idle", "here"), row(3, "busy", "here"), row(4, "idle", "here"), row(5, "prompt", "other"), row(6, "error", "here")}
	rows[3].Parked = true
	frame := newWatchPanelFrame(rows, nil, 0, len(rows), false)
	for _, tc := range []struct{ active, want uint32 }{{1, 2}, {2, 1}, {6, 1}, {3, 1}} {
		p, ok := watchNextAttentionTarget(frame, "here", tc.active, uintptr(tc.active))
		if !ok || p.Row.Sample.PID != tc.want {
			t.Fatalf("active %d: got %+v %v, want %d", tc.active, p, ok, tc.want)
		}
	}
	frame.Rows[0].Parked = true
	if p, ok := watchNextAttentionTarget(frame, "here", 6, 6); !ok || p.Row.Sample.PID != 2 {
		t.Fatal("return to top failed to skip parked first row")
	}
	frame.Rows[0].Parked = false
	frame = newWatchPanelFrame(rows[:1], nil, 0, 1, false)
	if _, ok := watchNextAttentionTarget(frame, "here", 1, 1); ok {
		t.Fatal("only active session selected itself")
	}
	frame = newWatchPanelFrame(rows, nil, 1, 1, false)
	if p, ok := watchNextAttentionTarget(frame, "here", 1, 1); !ok || p.Row.Sample.PID != 2 {
		t.Fatal("ignored visible viewport")
	}
	frame.Menu = true
	if _, ok := watchNextAttentionTarget(frame, "here", 1, 1); ok {
		t.Fatal("selected while settings open")
	}
}

func TestActionShortcutForegroundIdentity(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 1, Title: "one"}, HWND: 10}}, {Sample: watchSample{Session: Session{PID: 2, Title: "two"}, HWND: 10}}}
	if r, ok := watchForegroundSession(rows, 10, "two", true); !ok || r.Sample.PID != 2 {
		t.Fatal("wrong Terminal tab")
	}
	if _, ok := watchForegroundSession(rows, 10, "custom caption", true); ok {
		t.Fatal("guessed unknown Terminal tab")
	}
	if _, ok := watchForegroundSession(rows[:1], 10, "other tab", true); ok {
		t.Fatal("assumed sole tracked tab was active")
	}
	rows[0].Sample.Title = "two"
	if _, ok := watchForegroundSession(rows, 10, "two", true); ok {
		t.Fatal("ambiguous tabs accepted")
	}
	if _, ok := watchForegroundSession(rows, 99, "two", false); ok {
		t.Fatal("unrelated foreground selected first session")
	}
	if r, ok := watchForegroundSession(rows[:1], 10, "new native caption", false); !ok || r.Sample.PID != 1 {
		t.Fatal("native console identity lost on title update")
	}
}

func TestActionHotkeyCollisionAndSettings(t *testing.T) {
	active := map[uintptr]bool{watchFirstRowHotkey: true, watchHotkeyBase: true, watchRenameHotkey: true}
	problem := watchRegisterActionShortcut(watchParkHotkey, "Ctrl+Alt+Shift+P", "P", func(id, mods, key uintptr) bool {
		if id != watchParkHotkey || mods != 0x4007 || key != 'P' {
			t.Fatal("wrong chord")
		}
		return false
	}, func(id uintptr) { delete(active, id) })
	if problem == "" || len(active) != 3 {
		t.Fatal("collision disabled unrelated shortcuts")
	}
	if watchActionShortcutChoice("", "P") != "Off" || watchActionShortcutChoice("bad", "R") != "Off" {
		t.Fatal("new shortcuts unexpectedly enabled")
	}
	if watchNextActionShortcut("Off", "P") != "Ctrl+Alt+Shift+P" {
		t.Fatal("wrong first selectable binding")
	}
	if p := watchRegisterActionShortcut(watchParkHotkey, "Ctrl+Win+P", "P", func(id, mods, key uintptr) bool { return id == watchParkHotkey && mods == 0x400a && key == 'P' }, func(uintptr) {}); p != "" {
		t.Fatal("configurable Win chord rejected", p)
	}
	dir := t.TempDir()
	want := watchSettings{FirstRowShortcut: "Ctrl+Win+Z", Shortcuts: "Ctrl+Alt+Shift", ParkShortcut: "Ctrl+Alt+Shift+P", RenameShortcut: "Off"}
	if err := saveWatchSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	got := loadWatchSettings(dir)
	if got.ParkShortcut != want.ParkShortcut || got.RenameShortcut != want.RenameShortcut || got.FirstRowShortcut != want.FirstRowShortcut || got.Shortcuts != want.Shortcuts {
		t.Fatal("independent settings lost")
	}
	for _, key := range []uintptr{'P', 'R'} {
		if watchShortcutReleased(func(k uintptr) bool { return k == key }) {
			t.Fatal("action key still down accepted")
		}
	}
}
