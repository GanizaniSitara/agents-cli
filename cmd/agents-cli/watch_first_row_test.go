package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestShortcutHealthRecordsRegistrationAndCollision(t *testing.T) {
	a := &watchApp{dir: t.TempDir(), tracker: newWatchTracker(0)}
	a.recordShortcut("numbered", "Ctrl+Alt+Shift", "")
	a.recordShortcut("first_row", "Ctrl+Win+Z", "Unavailable: Ctrl+Win+Z; row 1 shortcut off")
	a.save()
	b, err := os.ReadFile(filepath.Join(a.dir, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt watchReceipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Shortcuts["numbered"].Registered || receipt.Shortcuts["first_row"].Registered || receipt.Shortcuts["first_row"].Problem == "" {
		t.Fatalf("collision receipt: %+v", receipt.Shortcuts)
	}
	a.recordShortcut("first_row", "Ctrl+Win+Z", "")
	if !a.shortcuts["first_row"].Registered || !a.shortcuts["numbered"].Registered {
		t.Fatal("successful registration lost")
	}
	a.recordShortcut("first_row", "Off", "")
	if a.shortcuts["first_row"].Registered || a.shortcuts["first_row"].Problem != "" {
		t.Fatal("Off reports registration or old collision")
	}
}

func TestSessionActivationRestoresOnlyMinimizedWindows(t *testing.T) {
	for _, minimized := range []bool{false, true} {
		restored := 0
		watchRestoreMinimized(123, func(hwnd uintptr) bool {
			if hwnd != 123 {
				t.Fatal("wrong target")
			}
			return minimized
		}, func(hwnd uintptr) { restored++ })
		if minimized && restored != 1 || !minimized && restored != 0 {
			t.Fatalf("minimized=%v restore calls=%d", minimized, restored)
		}
	}
}

func TestFirstRowShortcutRegistrationIndependent(t *testing.T) {
	active := map[uintptr]bool{}
	for i := 0; i < 10; i++ {
		active[watchHotkeyBase+uintptr(i)] = true
	}
	unregister := func(id uintptr) { delete(active, id) }
	problem := watchRegisterFirstRowShortcut("Ctrl+Win+Z", func(id, modifiers, key uintptr) bool {
		if id != watchFirstRowHotkey || modifiers != 0x400a || key != 'Z' {
			t.Fatalf("unexpected registration: %#x %#x %#x", id, modifiers, key)
		}
		return false // another application owns this chord
	}, unregister)
	if problem == "" || len(active) != 10 {
		t.Fatalf("collision affected numbered shortcuts: %q %v", problem, active)
	}
	problem = watchRegisterFirstRowShortcut("Ctrl+Win+Z", func(id, modifiers, key uintptr) bool {
		active[id] = true
		return true
	}, unregister)
	if problem != "" || !active[watchFirstRowHotkey] || len(active) != 11 {
		t.Fatal("successful registration not retained")
	}
	watchRegisterShortcuts("Off", func(id, modifiers, key uintptr) bool {
		t.Fatal("numbered Off attempted registration")
		return false
	}, unregister)
	if !active[watchFirstRowHotkey] || len(active) != 1 {
		t.Fatal("numbered setting changed independent shortcut")
	}
	watchRegisterFirstRowShortcut("Off", func(id, modifiers, key uintptr) bool {
		t.Fatal("first-row Off attempted registration")
		return false
	}, unregister)
	if len(active) != 0 {
		t.Fatal("first-row shortcut was not released")
	}
}

func TestFirstRowShortcutFollowsPaintedOrder(t *testing.T) {
	row := func(pid uint32) watchEntry { return watchEntry{Sample: watchSample{Session: Session{PID: pid}}} }
	frame := newWatchPanelFrame([]watchEntry{row(10), row(20)}, nil, 0, 2, false)
	p, ok := watchFirstRowTarget(frame)
	if !ok || p.Row.Sample.PID != 10 {
		t.Fatal("wrong first displayed row")
	}
	frame = newWatchPanelFrame([]watchEntry{row(20), row(10)}, nil, 0, 2, false)
	p, ok = watchFirstRowTarget(frame)
	if !ok || p.Row.Sample.PID != 20 {
		t.Fatal("shortcut remained tied to previous PID")
	}
	frame = newWatchPanelFrame([]watchEntry{row(20), row(10)}, nil, 1, 1, false)
	p, ok = watchFirstRowTarget(frame)
	if !ok || p.Row.Sample.PID != 10 || p.RowIndex != 1 {
		t.Fatal("shortcut ignored displayed scroll position")
	}
	frame.Menu = true
	if _, ok := watchFirstRowTarget(frame); ok {
		t.Fatal("Settings selected a hidden row")
	}
	if _, ok := watchFirstRowTarget(watchPanelFrame{}); ok {
		t.Fatal("empty panel selected a session")
	}
}

func TestFirstRowShortcutSettingsPersistIndependently(t *testing.T) {
	dir := t.TempDir()
	want := watchSettings{Shortcuts: "Ctrl+Alt+Shift", FirstRowShortcut: "Ctrl+Win+Z"}
	if err := saveWatchSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	got := loadWatchSettings(dir)
	if got.Shortcuts != want.Shortcuts || got.FirstRowShortcut != want.FirstRowShortcut {
		t.Fatalf("independent settings lost: %+v", got)
	}
	if watchFirstRowChoice("") != "Ctrl+Win+Z" || watchFirstRowChoice("bad chord") != "Off" {
		t.Fatal("default must be enabled; invalid binding must remain Off")
	}
	choice := "Off"
	for _, expected := range []string{"Ctrl+Win+Z", "Ctrl+Alt+Shift+Z", "Ctrl+Alt+Z", "Off"} {
		choice = watchNextFirstRowShortcut(choice)
		if choice != expected {
			t.Fatalf("choice=%s, want %s", choice, expected)
		}
	}
}

func TestFirstRowDefaultEnabledButExplicitOffPreserved(t *testing.T) {
	dir := t.TempDir()
	if loadWatchSettings(dir).FirstRowShortcut != "Ctrl+Win+Z" {
		t.Fatal("fresh configuration ships shortcut Off")
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"shortcuts":"Ctrl+Alt+Shift"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if loadWatchSettings(dir).FirstRowShortcut != "Ctrl+Win+Z" {
		t.Fatal("missing first-row setting disables shortcut")
	}
	if err := saveWatchSettings(dir, watchSettings{Shortcuts: "Ctrl+Alt+Shift", FirstRowShortcut: "Off"}); err != nil {
		t.Fatal(err)
	}
	if loadWatchSettings(dir).FirstRowShortcut != "Off" {
		t.Fatal("explicit owner Off was overridden")
	}
	var registered bool
	problem := watchRegisterFirstRowShortcut("", func(id, modifiers, key uintptr) bool {
		registered = id == watchFirstRowHotkey && modifiers == 0x400a && key == 'Z'
		return registered
	}, func(uintptr) {})
	if problem != "" || !registered {
		t.Fatal("default does not register Ctrl+Win+Z")
	}
}
