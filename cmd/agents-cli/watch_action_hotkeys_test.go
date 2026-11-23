package main

import "testing"

func TestAttentionShortcutCanLeaveBusyMailForUnknownSessions(t *testing.T) {
	rows := []watchEntry{}
	for i, state := range []string{"busy", "busy", "unknown", "unknown"} {
		pid := uint32(i + 1)
		rows = append(rows, watchEntry{Sample: watchSample{Session: Session{PID: pid, State: state}, HWND: uintptr(pid), DesktopID: "here"}})
	}
	frame := newWatchPanelFrame(rows, nil, 0, len(rows), false)
	var cycle watchAttentionCycle
	active := uint32(1)
	for _, want := range []uint32{3, 4, 3} {
		p, ok := cycle.next(frame, "here", active, uintptr(active))
		if !ok || p.Row.Sample.PID != want {
			t.Fatalf("from %d got %d, %v; want %d", active, p.Row.Sample.PID, ok, want)
		}
		active = want
	}
}

func TestAttentionCycleVisitsAllRowsBeforeWrapping(t *testing.T) {
	rows := []watchEntry{}
	for _, pid := range []uint32{1, 2, 3, 4, 5} {
		rows = append(rows, watchEntry{Sample: watchSample{Session: Session{PID: pid, State: "idle"}, HWND: uintptr(pid), DesktopID: "here"}})
	}
	rows[3].Parked = true
	rows[4].Sample.DesktopID = "other" // other-desktop rows stay in the rotation
	frame := newWatchPanelFrame(rows, nil, 0, len(rows), false)
	var cycle watchAttentionCycle
	active := uint32(3)
	for _, want := range []uint32{1, 2, 3, 5} {
		p, ok := cycle.next(frame, "here", active, uintptr(active))
		if !ok || p.Row.Sample.PID != want {
			t.Fatalf("active %d: got %d, %v; want %d", active, p.Row.Sample.PID, ok, want)
		}
		active = want
		// Status updates can reorder the active row while cycling.
		frame.Rows[0], frame.Rows[1] = frame.Rows[1], frame.Rows[0]
	}
	// Selecting a different window manually restarts from the top.
	p, ok := cycle.next(frame, "here", 3, 3)
	if !ok || p.Row.Sample.PID != frame.Rows[0].Sample.PID {
		t.Fatal("manual focus change did not restart at top")
	}
}

func TestNextAttentionShortcutSkipsActiveBusyParked(t *testing.T) {
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
	// Other-desktop rows are part of the rotation: they are selected like any
	// other row and pulled onto the current desktop on activation.
	otherOnly := newWatchPanelFrame([]watchEntry{row(7, "idle", "other")}, nil, 0, 1, false)
	if p, ok := watchNextAttentionTarget(otherOnly, "here", 1, 1); !ok || p.Row.Sample.PID != 7 {
		t.Fatal("rotation must include other-desktop rows")
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

func TestAttentionRotationAdvancesPastFailedPull(t *testing.T) {
	// Simulates the Ctrl+Win+Z fall-through: the first candidate's desktop
	// move fails, so the panel calls cycle.next again without any successful
	// activation. The failed row is already marked seen and must be skipped,
	// never dead-ending the rotation.
	rows := []watchEntry{}
	for _, pid := range []uint32{1, 2, 3} {
		rows = append(rows, watchEntry{Sample: watchSample{Session: Session{PID: pid, State: "idle"}, HWND: uintptr(pid), DesktopID: "other"}})
	}
	frame := newWatchPanelFrame(rows, nil, 0, len(rows), false)
	var cycle watchAttentionCycle
	p, ok := cycle.next(frame, "here", 0, 0)
	if !ok || p.Row.Sample.PID != 1 {
		t.Fatalf("first candidate = %+v %v", p, ok)
	}
	// "desktop-move-failed" on pid 1: the panel re-queries the cycle within
	// the same press via nextAfterFailure, which must not reset seen.
	p2, ok := cycle.nextAfterFailure(frame, "here", 0, 0)
	if !ok || p2.Row.Sample.PID == 1 {
		t.Fatalf("failed candidate repeated: pid=%d ok=%v", p2.Row.Sample.PID, ok)
	}
	if p2.Row.Sample.PID != 2 {
		t.Fatalf("expected next candidate 2, got %+v", p2)
	}
	p3, ok := cycle.nextAfterFailure(frame, "here", 0, 0)
	if !ok || p3.Row.Sample.PID != 3 {
		t.Fatalf("expected candidate 3, got %+v %v", p3, ok)
	}
	// With a tracked foreground session active, a failed pull must also
	// advance (the new-press reset in next() must not fire mid-press).
	rows = append(rows, watchEntry{Sample: watchSample{Session: Session{PID: 9, State: "busy"}, HWND: 9, DesktopID: "here"}})
	frame = newWatchPanelFrame(rows, nil, 0, len(rows), false)
	var cycle2 watchAttentionCycle
	p, ok = cycle2.next(frame, "here", 9, 9)
	if !ok || p.Row.Sample.PID != 1 {
		t.Fatalf("with active 9: first candidate = %d %v", p.Row.Sample.PID, ok)
	}
	p2, ok = cycle2.nextAfterFailure(frame, "here", 9, 9)
	if !ok || p2.Row.Sample.PID != 2 {
		t.Fatalf("with active 9: failed candidate repeated or wrong next: pid=%d ok=%v", p2.Row.Sample.PID, ok)
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
