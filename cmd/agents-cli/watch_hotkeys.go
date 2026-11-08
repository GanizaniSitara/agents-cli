package main

import "fmt"

var watchShortcutChoices = []string{"Ctrl+Alt+Shift", "Ctrl+Alt", "Ctrl+Shift", "Off"}

func watchShortcutChoice(choice string) string {
	for _, s := range watchShortcutChoices {
		if s == choice {
			return s
		}
	}
	return watchShortcutChoices[0]
}

func watchNextShortcut(choice string) string {
	choice = watchShortcutChoice(choice)
	for i, s := range watchShortcutChoices {
		if s == choice {
			return watchShortcutChoices[(i+1)%len(watchShortcutChoices)]
		}
	}
	return "Off"
}

func watchShortcutModifiers(choice string) uintptr {
	switch watchShortcutChoice(choice) {
	case "Ctrl+Alt+Shift":
		return 1 | 2 | 4
	case "Ctrl+Alt":
		return 1 | 2
	case "Ctrl+Shift":
		return 2 | 4
	}
	return 0
}

func watchShortcutDigit(index int) string {
	if index < 0 || index >= 10 {
		return ""
	}
	return fmt.Sprint((index + 1) % 10)
}

const watchHotkeyBase = 0x4100

// Register the whole set or none, so a collision never leaves partial shortcuts.
func watchRegisterShortcuts(choice string, register func(id, modifiers, key uintptr) bool, unregister func(id uintptr)) string {
	for i := 0; i < 10; i++ {
		unregister(watchHotkeyBase + uintptr(i))
	}
	modifiers := watchShortcutModifiers(choice)
	if modifiers == 0 {
		return ""
	}
	for i := 0; i < 10; i++ {
		key := uintptr('0' + (i+1)%10)
		if !register(watchHotkeyBase+uintptr(i), modifiers|0x4000, key) {
			for j := 0; j < i; j++ {
				unregister(watchHotkeyBase + uintptr(j))
			}
			return fmt.Sprintf("Unavailable: %s+%s; shortcuts off", watchShortcutChoice(choice), watchShortcutDigit(i))
		}
	}
	return ""
}

func watchShortcutRow(frame watchPanelFrame, id uintptr) (watchPanelPress, bool) {
	if frame.Menu || id < watchHotkeyBase || id >= watchHotkeyBase+10 {
		return watchPanelPress{}, false
	}
	i := int(id - watchHotkeyBase)
	if i >= len(frame.Rows) {
		return watchPanelPress{}, false
	}
	p := frame.hit(20, 34+i*watchRowHeight+20)
	return p, p.Kind == "row"
}

func (a *watchApp) activateWatchRow(p watchPanelPress, releaseY int) {
	row := p.Row
	state := watchReadTarget(row.Sample)
	problem := watchTargetProblem(row.Sample, state)
	a.mu.Lock()
	current := a.tracker.entries[row.Sample.PID]
	if problem == "" && (current == nil || !watchSameClickSession(row.Sample, current.Sample)) {
		problem = "session-identity-changed"
	}
	a.mu.Unlock()
	if problem != "" {
		a.logClick(p, releaseY, state, problem, false, false)
		return
	}
	// Only a session click or an explicit numbered shortcut calls this path.
	watchFlash(row.Sample.HWND, false)
	watchUser.NewProc("ShowWindowAsync").Call(row.Sample.HWND, 9)
	watchUser.NewProc("SwitchToThisWindow").Call(row.Sample.HWND, 1)
	set, _, _ := watchUser.NewProc("SetForegroundWindow").Call(row.Sample.HWND)
	a.logClick(p, releaseY, state, "activation-requested", true, set != 0)
}
