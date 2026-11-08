package main

import "testing"

func TestWatchShortcutCollisionReleasesWholeSet(t *testing.T) {
	active := map[uintptr]bool{}
	problem := watchRegisterShortcuts("Ctrl+Alt+Shift", func(id, modifiers, key uintptr) bool {
		if key == '4' {
			return false
		}
		if modifiers != 0x4007 {
			t.Fatalf("modifiers = %#x", modifiers)
		}
		active[id] = true
		return true
	}, func(id uintptr) { delete(active, id) })
	if problem == "" || len(active) != 0 {
		t.Fatalf("collision left partial shortcuts: %q %v", problem, active)
	}
}

func TestWatchShortcutOrderAndOff(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 12}}}, {Sample: watchSample{Session: Session{PID: 34}}}, {Sample: watchSample{Session: Session{PID: 56}}}}
	frame := newWatchPanelFrame(rows, nil, 1, 2, false)
	p, ok := watchShortcutRow(frame, watchHotkeyBase)
	if !ok || p.Row.Sample.PID != 34 || p.RowIndex != 1 {
		t.Fatalf("shortcut must follow displayed row, got %+v", p)
	}
	if _, ok := watchShortcutRow(frame, watchHotkeyBase+2); ok {
		t.Fatal("shortcut selected an undisplayed row")
	}
	frame.Menu = true
	if _, ok := watchShortcutRow(frame, watchHotkeyBase); ok {
		t.Fatal("settings must not select an invisible session")
	}
	if watchShortcutDigit(9) != "0" || watchShortcutDigit(10) != "" {
		t.Fatal("incorrect numbered key mapping")
	}
	if watchShortcutChoice("") != "Ctrl+Alt+Shift" || watchNextShortcut("Ctrl+Shift") != "Off" {
		t.Fatal("incorrect default or settings cycle")
	}
	watchRegisterShortcuts("Off", func(id, modifiers, key uintptr) bool {
		t.Fatal("Off registered a shortcut")
		return true
	}, func(id uintptr) {})
}

func TestWatchShortcutSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	if err := saveWatchSettings(dir, watchSettings{Shortcuts: "Off"}); err != nil {
		t.Fatal(err)
	}
	if got := loadWatchSettings(dir).Shortcuts; got != "Off" {
		t.Fatalf("shortcut choice not saved: %q", got)
	}
}
