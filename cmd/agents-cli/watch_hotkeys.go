package main

import (
	"fmt"
	"time"
	"unsafe"
)

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

const watchFirstRowHotkey = 0x4200
const watchDefaultFirstRowShortcut = "Ctrl+Win+Z"

type watchShortcutHealth struct {
	Binding    string `json:"binding"`
	Registered bool   `json:"registered"`
	Problem    string `json:"problem,omitempty"`
}

func (a *watchApp) recordShortcut(kind, binding, problem string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shortcuts == nil {
		a.shortcuts = map[string]watchShortcutHealth{}
	}
	a.shortcuts[kind] = watchShortcutHealth{Binding: binding, Registered: binding != "Off" && problem == "", Problem: problem}
}

// Do not hand focus to a console while the chord is still down: subsequent
// repeat/key-up events belong to the invoking app, not the selected shell.
func watchShortcutReleased(down func(uintptr) bool) bool {
	for _, key := range []uintptr{0x10, 0x11, 0x12, 0x5B, 0x5C, 'Z', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9'} {
		if down(key) {
			return false
		}
	}
	return true
}

var watchFirstRowChoices = []string{"Off", "Ctrl+Win+Z", "Ctrl+Alt+Shift+Z", "Ctrl+Alt+Z"}

func watchFirstRowChoice(choice string) string {
	if choice == "" {
		return watchDefaultFirstRowShortcut
	}
	for _, value := range watchFirstRowChoices {
		if choice == value {
			return value
		}
	}
	return "Off"
}

func watchNextFirstRowShortcut(choice string) string {
	for i, value := range watchFirstRowChoices {
		if watchFirstRowChoice(choice) == value {
			return watchFirstRowChoices[(i+1)%len(watchFirstRowChoices)]
		}
	}
	return "Off"
}

func watchRegisterFirstRowShortcut(choice string, register func(id, modifiers, key uintptr) bool, unregister func(id uintptr)) string {
	choice = watchFirstRowChoice(choice)
	unregister(watchFirstRowHotkey) // never unregister the numbered set
	var modifiers uintptr
	switch watchFirstRowChoice(choice) {
	case "Ctrl+Win+Z":
		modifiers = 2 | 8
	case "Ctrl+Alt+Shift+Z":
		modifiers = 2 | 1 | 4
	case "Ctrl+Alt+Z":
		modifiers = 2 | 1
	default:
		return ""
	}
	if !register(watchFirstRowHotkey, modifiers|0x4000, 'Z') {
		return "Unavailable: " + choice + "; row 1 shortcut off"
	}
	return ""
}

func watchFirstRowTarget(frame watchPanelFrame) (watchPanelPress, bool) {
	if frame.Menu {
		return watchPanelPress{}, false
	}
	for i, row := range frame.Rows {
		if !row.Parked {
			p := frame.hit(20, 34+i*watchRowHeight+20)
			return p, p.Kind == "row"
		}
	}
	return watchPanelPress{}, false
}

// SW_RESTORE changes a visible maximized/snapped window's placement. Only a
// genuinely minimized window needs its saved placement restored for activation.
func watchRestoreMinimized(hwnd uintptr, minimized func(uintptr) bool, restore func(uintptr)) {
	if minimized(hwnd) {
		restore(hwnd)
	}
}

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
	// Only a session click or an explicit session shortcut calls this path.
	settings := loadWatchSettings(a.dir)
	pulseColour := watchPulseColour(row, loadWatchTheme(a.dir), p.Trigger == "double-click")
	if settings.DisableDesktopSwitch {
		manager, release := openWatchDesktopManager()
		desktop, _ := windowWatchDesktop(manager, row.Sample.HWND, watchDesktops())
		release()
		if desktop == "" || desktop != watchCurrentDesktop() {
			a.logClick(p, releaseY, state, "desktop-switch-disabled", false, false)
			return
		}
	}
	if settings.DisableForeground {
		if !settings.DisableSelectionBlink {
			watchPulseWindow(row.Sample, pulseColour)
		}
		a.logClick(p, releaseY, state, "foreground-disabled", false, false)
		return
	}
	var before watchRect
	watchUser.NewProc("GetWindowRect").Call(row.Sample.HWND, uintptr(unsafe.Pointer(&before)))
	wasMinimized, _, _ := watchUser.NewProc("IsIconic").Call(row.Sample.HWND)
	watchFlash(row.Sample.HWND, false)
	watchRestoreMinimized(row.Sample.HWND, func(hwnd uintptr) bool {
		iconic, _, _ := watchUser.NewProc("IsIconic").Call(hwnd)
		return iconic != 0
	}, func(hwnd uintptr) { watchUser.NewProc("ShowWindowAsync").Call(hwnd, 9) })
	// SwitchToThisWindow can post SC_RESTORE even for an existing window.
	// Foreground activation alone lets the shell follow the explicit selection
	// across desktops without an additional restore request.
	set, _, _ := watchUser.NewProc("SetForegroundWindow").Call(row.Sample.HWND)
	if set != 0 {
		a.foregroundLift.selectWindow(row.Sample, watchCoveredByTopmost(row.Sample.HWND), watchTopmost(row.Sample.HWND), watchLiftIdentity, watchSetLift)
	}
	if !settings.DisableSelectionBlink {
		watchPulseWindow(row.Sample, pulseColour)
	}
	a.logClick(p, releaseY, state, "activation-requested", true, set != 0)
	go func() {
		time.Sleep(250 * time.Millisecond)
		var after watchRect
		watchUser.NewProc("GetWindowRect").Call(row.Sample.HWND, uintptr(unsafe.Pointer(&after)))
		foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
		_ = watchAppendClick(a.dir, watchClickRecord{Time: time.Now(), PID: row.Sample.PID, HWND: row.Sample.HWND,
			Outcome: "activation-geometry", BoundsBefore: &before, BoundsAfter: &after, WasMinimized: wasMinimized != 0,
			ForegroundAfter: foreground, ForegroundPID: watchWindowPID(foreground), ForegroundTitle: watchWindowTitle(foreground)})
	}()
}
