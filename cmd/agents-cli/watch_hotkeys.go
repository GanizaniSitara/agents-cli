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
const watchParkHotkey = 0x4201
const watchRenameHotkey = 0x4202
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
	for _, key := range []uintptr{0x10, 0x11, 0x12, 0x5B, 0x5C, 'Z', 'P', 'R', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9'} {
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

func watchFirstRowTarget(frame watchPanelFrame, current string) (watchPanelPress, bool) {
	if frame.Menu {
		return watchPanelPress{}, false
	}
	for i, row := range frame.Rows {
		if !row.Parked && watchDesktopSelectable(row.Sample.DesktopID, current) {
			p := frame.hit(20, 34+i*panelRowHeight(frame.Compact)+20)
			return p, p.Kind == "row"
		}
	}
	return watchPanelPress{}, false
}

// Shared Terminal HWNDs are not session identities. Require an exact unique
// caption match there; never park or rename an arbitrary tab in that window.
func watchForegroundSession(rows []watchEntry, foreground uintptr, title string, terminal bool) (watchEntry, bool) {
	var candidates []watchEntry
	for _, row := range rows {
		if foreground != 0 && row.Sample.HWND == foreground {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 1 && !terminal {
		return candidates[0], true
	}
	var matches []watchEntry
	for _, row := range candidates {
		if row.Sample.Title == title {
			matches = append(matches, row)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return watchEntry{}, false
}

type watchAttentionCycle struct {
	last watchSample
	seen map[uint32]watchSample
}

func (c *watchAttentionCycle) next(frame watchPanelFrame, current string, activePID uint32, foreground uintptr) (watchPanelPress, bool) {
	var active watchSample
	for _, row := range frame.Rows {
		if row.Sample.PID == activePID {
			active = row.Sample
		}
	}
	if activePID == 0 || !watchSameClickSession(active, c.last) {
		c.seen = make(map[uint32]watchSample)
	}
	if c.seen == nil {
		c.seen = make(map[uint32]watchSample)
	}
	remaining := frame
	remaining.Rows = append([]watchEntry(nil), frame.Rows...)
	for i, row := range remaining.Rows {
		if seen, ok := c.seen[row.Sample.PID]; ok && watchSameClickSession(seen, row.Sample) {
			remaining.Rows[i].Parked = true
		}
	}
	p, ok := watchNextAttentionTarget(remaining, current, activePID, foreground)
	if !ok && !frame.Menu {
		c.seen = make(map[uint32]watchSample)
		if activePID != 0 {
			c.seen[activePID] = active
		}
		p, ok = watchNextAttentionTarget(frame, current, activePID, foreground)
	}
	if ok {
		c.last = p.Row.Sample
		c.seen[p.Row.Sample.PID] = p.Row.Sample
	}
	return p, ok
}

func watchNextAttentionTarget(frame watchPanelFrame, current string, activePID uint32, foreground uintptr) (watchPanelPress, bool) {
	if frame.Menu {
		return watchPanelPress{}, false
	}
	start := -1
	// Return to the top from any lower row. Only advance past row one
	// when it is already active, so repeated use always navigates away.
	if len(frame.Rows) > 0 && activePID != 0 && frame.Rows[0].Sample.PID == activePID {
		start = 0
	}
	for n := 1; n <= len(frame.Rows); n++ {
		i := (start + n) % len(frame.Rows)
		row := frame.Rows[i]
		s := row.Sample
		if row.Parked || !watchDesktopSelectable(s.DesktopID, current) || s.PID == activePID || (activePID == 0 && foreground != 0 && s.HWND == foreground) {
			continue
		}
		if activePID != 0 && !(s.State == "idle" || s.State == "unknown" || s.State == "prompt" || s.State == "error" || s.PendingQuestions > 0 || s.Phase == "finished" || s.Phase == "waiting" || s.Phase == "question" || s.Phase == "review" || s.Phase == "blocked") {
			continue
		}
		p := frame.hit(20, 34+i*panelRowHeight(frame.Compact)+20)
		return p, p.Kind == "row"
	}
	return watchPanelPress{}, false
}

func watchActionShortcutChoices(key string) []string {
	return []string{"Off", "Ctrl+Alt+Shift+" + key, "Ctrl+Alt+" + key, "Ctrl+Win+" + key}
}

func watchActionShortcutChoice(choice, key string) string {
	// New bindings stay opt-in until explicitly selected in Settings.
	for _, candidate := range watchActionShortcutChoices(key) {
		if candidate == choice {
			return choice
		}
	}
	return "Off"
}

func watchNextActionShortcut(choice, key string) string {
	choices := watchActionShortcutChoices(key)
	for i, candidate := range choices {
		if candidate == watchActionShortcutChoice(choice, key) {
			return choices[(i+1)%len(choices)]
		}
	}
	return "Off"
}

func watchRegisterActionShortcut(id uintptr, choice, key string, register func(uintptr, uintptr, uintptr) bool, unregister func(uintptr)) string {
	unregister(id)
	choice = watchActionShortcutChoice(choice, key)
	if choice == "Off" {
		return ""
	}
	modifiers := uintptr(1 | 2)
	if choice == "Ctrl+Alt+Shift+"+key {
		modifiers |= 4
	}
	if choice == "Ctrl+Win+"+key {
		modifiers = 2 | 8
	}
	if !register(id, modifiers|0x4000, uintptr(key[0])) {
		return "Unavailable: " + choice
	}
	return ""
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
	p := frame.hit(20, 34+i*panelRowHeight(frame.Compact)+20)
	return p, p.Kind == "row"
}

func (a *watchApp) activateWatchRow(p watchPanelPress, releaseY int) {
	a.selectionID++
	a.activateWatchRowSelected(p, releaseY, false)
}

func (a *watchApp) activateWatchRowSelected(p watchPanelPress, releaseY int, terminalSelected bool) {
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
	settings := a.currentSettings()
	pulseColour := watchPulseColour(row, loadWatchTheme(a.dir), p.Trigger == "double-click")
	var cloaked uint32
	hr, _, _ := watchDWM.NewProc("DwmGetWindowAttribute").Call(row.Sample.HWND, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
	if !watchDesktopSelectable(row.Sample.DesktopID, a.currentDesktop()) || int32(hr) < 0 || cloaked != 0 {
		a.logClick(p, releaseY, state, "background-desktop-display-only", false, false)
		return
	}
	if settings.DisableForeground {
		if !settings.DisableSelectionBlink {
			watchPulseWindow(row.Sample, pulseColour, settings.SelectionBlinkStyle)
		}
		a.logClick(p, releaseY, state, "foreground-disabled", false, false)
		return
	}
	if !terminalSelected && watchWindowClass(row.Sample.HWND) == "CASCADIA_HOSTING_WINDOW_CLASS" {
		if a.selectTerminal == nil {
			a.logClick(p, releaseY, state, "terminal-selector-unavailable", false, false)
			return
		}
		a.selectTerminal(watchTerminalRequest{Press: p, ReleaseY: releaseY, Generation: a.selectionID})
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
	a.ui.step("selection.foreground")
	set, _, _ := watchUser.NewProc("SetForegroundWindow").Call(row.Sample.HWND)
	if set != 0 {
		a.foregroundLift.selectWindow(row.Sample, watchCoveredByTopmost(row.Sample.HWND), watchTopmost(row.Sample.HWND), watchLiftIdentity, watchSetLift)
		if settings.MovePointerOnShortcut && (p.Trigger == "first-row-shortcut" || p.Trigger == "numbered-shortcut") {
			watchMovePointer(row.Sample)
		}
	}
	if !settings.DisableSelectionBlink {
		a.ui.step("selection.blink")
		watchPulseWindow(row.Sample, pulseColour, settings.SelectionBlinkStyle)
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
