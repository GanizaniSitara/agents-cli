package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var watchUser = windows.NewLazySystemDLL("user32.dll")
var watchGDI = windows.NewLazySystemDLL("gdi32.dll")

const watchRowHeight = 56

type watchRect struct{ Left, Top, Right, Bottom int32 }
type watchPoint struct{ X, Y int32 }
type watchMessage struct {
	HWND           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          watchPoint
	Private        uint32
}
type watchClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	IconSmall                          uintptr
}
type watchPaint struct {
	DC                 uintptr
	Erase              int32
	Rect               watchRect
	Restore, IncUpdate int32
	Reserved           [32]byte
}

func watchPanel(a *watchApp) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	name := windows.StringToUTF16Ptr("AgentsCLIWatchPanel")
	settings := loadWatchSettings(a.dir)
	theme := loadWatchTheme(a.dir)
	menu := false
	monitors := watchMonitors()
	desktops := watchDesktops()
	taskbar, releaseTaskbar := openWatchTaskbar()
	defer releaseTaskbar()
	taskbarSignals := watchTaskbarController{}
	defer taskbarSignals.update(nil, "", taskbar.set)
	stoppedCount := 0
	unknownCount := 0
	lastCount := -1
	lastRender := ""
	offset := 0
	var hwnd uintptr
	shortcutProblem := ""
	firstRowProblem := ""
	unregisterShortcut := func(id uintptr) { watchUser.NewProc("UnregisterHotKey").Call(hwnd, id) }
	registerShortcut := func(id, modifiers, key uintptr) bool {
		ok, _, _ := watchUser.NewProc("RegisterHotKey").Call(hwnd, id, modifiers, key)
		return ok != 0
	}
	applyShortcuts := func() {
		shortcutProblem = watchRegisterShortcuts(settings.Shortcuts, registerShortcut, unregisterShortcut)
	}
	applyFirstRowShortcut := func() {
		firstRowProblem = watchRegisterFirstRowShortcut(settings.FirstRowShortcut, registerShortcut, unregisterShortcut)
	}
	var rows []watchEntry
	var summaries []watchPanelSummary
	var painted watchPanelFrame
	var pressed watchPanelPress
	dragging := false
	var dragOffset watchPoint
	capacity := 1
	currentDesktop := watchCurrentDesktop()
	var previousStates map[uint32]string
	wakeReady := false
	var tray watchTrayData
	resize := func() {
		if pressed.Active || dragging {
			return
		} // keep the panel still through mouse-up
		area := watchPositionMonitor(monitors, settings).Work
		n := len(rows)
		capacity = watchPanelCapacity(area.Bottom-area.Top, len(summaries))
		if n > capacity {
			n = capacity
		}
		maxOffset := len(rows) - capacity
		if maxOffset < 0 {
			maxOffset = 0
		}
		if offset > maxOffset {
			offset = maxOffset
		}
		if menu {
			n = len(monitors) + len(desktops) + 6
		}
		height := int32(34 + n*watchRowHeight + len(summaries)*28)
		if menu {
			height = int32(34 + n*44)
		}
		width := int32(360)
		flags := uintptr(0x10)
		if !settings.Hidden {
			flags |= 0x40
		}
		position := watchPanelPosition(area, width, height, settings.Position)
		watchUser.NewProc("SetWindowPos").Call(hwnd, ^uintptr(0), uintptr(position.X), uintptr(position.Y), uintptr(width), uintptr(height), flags)
		watchUser.NewProc("InvalidateRect").Call(hwnd, 0, 1)
	}
	finishDrag := func() {
		if !dragging {
			return
		}
		dragging = false
		watchUser.NewProc("KillTimer").Call(hwnd, 2)
		watchUser.NewProc("ReleaseCapture").Call()
		var rect watchRect
		watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		settings.Position = &watchPoint{rect.Left, rect.Top}
		settings.Monitor = watchPositionMonitor(monitors, settings).Device
		_ = saveWatchSettings(a.dir, settings)
		resize()
	}
	moveDrag := func() {
		var cursor watchPoint
		watchUser.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
		watchUser.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(cursor.X-dragOffset.X), uintptr(cursor.Y-dragOffset.Y), 0, 0, 0x1|0x4|0x10)
	}
	brush, _, _ := watchGDI.NewProc("CreateSolidBrush").Call(theme.colour(theme.Background))
	defer watchGDI.NewProc("DeleteObject").Call(brush)
	fontHeight := int32(-15)
	inputFont, _, _ := watchGDI.NewProc("CreateFontW").Call(uintptr(fontHeight), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Segoe UI"))))
	defer watchGDI.NewProc("DeleteObject").Call(inputFont)
	proc := syscall.NewCallback(func(window uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x312: // WM_HOTKEY: same targets and identity checks as explicit clicks
			if wp == watchFirstRowHotkey {
				if p, ok := watchFirstRowTarget(painted); ok && firstRowProblem == "" && watchFirstRowChoice(settings.FirstRowShortcut) != "Off" && !pressed.Active && !dragging {
					p.Time = time.Now()
					a.activateWatchRow(p, p.Y)
				}
				return 0
			}
			if p, ok := watchShortcutRow(painted, wp); ok && shortcutProblem == "" && watchShortcutModifiers(settings.Shortcuts) != 0 && !pressed.Active && !dragging {
				p.Time = time.Now()
				a.activateWatchRow(p, p.Y)
			}
			return 0
		case 0x8001: // tray callback: reopening never activates the panel
			if lp == 0x202 || lp == 0x205 {
				settings.Hidden = false
				_ = saveWatchSettings(a.dir, settings)
				watchUser.NewProc("ShowWindow").Call(window, 4)
				resize()
			}
			return 0
		case 0x7e: // display configuration changed; disconnected selection falls back
			monitors = watchMonitors()
			resize()
			return 0
		case 0x21:
			return 3 // WM_MOUSEACTIVATE: deliver click, never focus panel
		case 0x113: // WM_TIMER
			if wp == 2 {
				if dragging {
					buttons, _, _ := watchUser.NewProc("GetAsyncKeyState").Call(1)
					if buttons&0x8000 == 0 {
						finishDrag()
					} else {
						moveDrag()
					}
				}
				return 0
			}
			if token, err := os.ReadFile(filepath.Join(a.dir, "stop")); err == nil {
				if string(token) == startTime(uint32(os.Getpid())).Format(time.RFC3339Nano) {
					watchUser.NewProc("DestroyWindow").Call(window)
					return 0
				}
			}
			// A nonactivating window may not receive mouse-up outside its
			// visible area. Cancel that abandoned press without activating.
			if pressed.Active && time.Since(pressed.Time) >= time.Second {
				buttons, _, _ := watchUser.NewProc("GetAsyncKeyState").Call(1)
				if buttons&0x8000 == 0 {
					p := pressed
					pressed.Active = false
					watchUser.NewProc("ReleaseCapture").Call()
					if p.Kind == "row" {
						a.logClick(p, -1, watchReadTarget(p.Row.Sample), "release-not-delivered", false, false)
					}
				}
			}
			a.mu.Lock()
			allRows := a.tracker.panelRows()
			newDesktop := watchCurrentDesktop()
			a.flashes.update(a.tracker.entries, newDesktop, watchFlash)
			taskbarSignals.update(a.tracker.entries, newDesktop, taskbar.set)
			rows, summaries = watchPanelLayout(allRows, newDesktop, desktops, a.expanded)
			wakeRows, _ := watchPanelLayout(allRows, newDesktop, desktops, nil)
			stoppedCount = 0
			unknownCount = 0
			for _, row := range rows {
				if row.Stopped {
					stoppedCount++
				}
				if row.Sample.State == "unknown" {
					unknownCount++
				}
			}
			a.mu.Unlock()
			// Only relevant status changes reopen the panel. Age ticks, working
			// screen redraws and background desktop changes cannot wake it.
			if wakeReady && newDesktop == currentDesktop && watchPanelNeedsWake(previousStates, wakeRows) && settings.Hidden {
				settings.Hidden = false
				_ = saveWatchSettings(a.dir, settings)
				watchUser.NewProc("ShowWindow").Call(window, 4)
			}
			previousStates = watchPanelStates(wakeRows)
			currentDesktop = newDesktop
			if len(allRows) > 0 {
				wakeReady = true
			}
			if stoppedCount != lastCount {
				tray.update(stoppedCount, 1)
				lastCount = stoppedCount
			}
			if offset >= len(rows) {
				offset = 0
			}
			if !settings.Hidden && !pressed.Active && !dragging {
				key := watchPanelRenderKey(rows, summaries, offset, capacity, currentDesktop, stoppedCount, unknownCount, time.Now().Unix()/10)
				if key != lastRender {
					lastRender = key
					resize()
				}
			}
			return 0
		case 0x20a: // WM_MOUSEWHEEL, scroll a bounded viewport
			if pressed.Active {
				return 0
			}
			if int16(wp>>16) < 0 {
				if offset+capacity < len(rows) {
					offset++
				}
			} else if offset > 0 {
				offset--
			}
			watchUser.NewProc("InvalidateRect").Call(window, 0, 1)
			return 0
		case 0x201: // WM_LBUTTONDOWN: select the last fully painted target
			x, y := int32(int16(lp)), int32(int16(lp>>16))
			if x >= 0 && x < 245 && y >= 0 && y < 34 {
				dragging = true
				dragOffset = watchPoint{x, y}
				watchUser.NewProc("SetCapture").Call(window)
				watchUser.NewProc("SetTimer").Call(window, 2, 16, 0)
				return 0
			}
			pressed = painted.hit(int(int16(lp)), int(int16(lp>>16)))
			if pressed.Active {
				pressed.Time = time.Now()
				pressed.Screen = watchPoint{int32(pressed.X), int32(pressed.Y)}
				watchUser.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&pressed.Screen)))
				watchUser.NewProc("SetCapture").Call(window)
			}
			return 0
		case 0x200: // WM_MOUSEMOVE: header drag, without activating the window
			if dragging && wp&1 != 0 {
				point := watchPoint{int32(int16(lp)), int32(int16(lp >> 16))}
				watchUser.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&point)))
				watchUser.NewProc("SetWindowPos").Call(window, 0, uintptr(point.X-dragOffset.X), uintptr(point.Y-dragOffset.Y), 0, 0, 0x1|0x4|0x10)
			}
			return 0
		case 0x215, 0x1f: // capture lost / cancel mode: never activate
			finishDrag()
			if pressed.Active {
				p := pressed
				pressed.Active = false
				watchUser.NewProc("ReleaseCapture").Call()
				if p.Kind == "row" {
					a.logClick(p, -1, watchReadTarget(p.Row.Sample), "cancelled", false, false)
				}
				resize()
			}
			return 0
		case 0x202: // WM_LBUTTONUP: use the saved press, never re-hit-test
			if dragging {
				finishDrag()
				return 0
			}
			p := pressed
			pressed.Active = false
			watchUser.NewProc("ReleaseCapture").Call()
			if !p.Active {
				// Diagnose a missed down event without ever using release to
				// choose an activation target.
				candidate := painted.hit(int(int16(lp)), int(int16(lp>>16)))
				if candidate.Kind == "row" {
					candidate.Screen = watchPoint{int32(candidate.X), int32(candidate.Y)}
					watchUser.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&candidate.Screen)))
					a.logClick(candidate, candidate.Y, watchReadTarget(candidate.Row.Sample), "release-without-press", false, false)
				}
				return 0
			}
			defer resize()
			releaseY := int(int16(lp >> 16))
			point := watchPoint{int32(int16(lp)), int32(releaseY)}
			watchUser.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&point)))
			dx, dy := point.X-p.Screen.X, point.Y-p.Screen.Y
			if dx > 8 || dx < -8 || dy > 8 || dy < -8 {
				if p.Kind == "row" {
					a.logClick(p, releaseY, watchReadTarget(p.Row.Sample), "drag-cancelled", false, false)
				}
				return 0
			}
			x, y := p.X, p.Y
			if y < 34 {
				if x >= 316 {
					settings.Hidden = true
					_ = saveWatchSettings(a.dir, settings)
					watchUser.NewProc("ShowWindow").Call(window, 0)
					return 0
				}
				if x >= 245 {
					menu = !menu
					theme = loadWatchTheme(a.dir)
					monitors = watchMonitors()
					desktops = watchDesktops()
				}
				resize()
				return 0
			}
			if p.Kind == "menu" {
				i := (y - 34) / 44
				if i >= 0 && i < len(monitors) {
					settings.Monitor = monitors[i].Device
					settings.Position = nil
					_ = saveWatchSettings(a.dir, settings)
				} else if i >= len(monitors) && i < len(monitors)+len(desktops) {
					settings.toggleDesktop(desktops[i-len(monitors)].ID)
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				} else if i == len(monitors)+len(desktops)+4 {
					settings.Position = nil
					_ = saveWatchSettings(a.dir, settings)
				} else if i == len(monitors)+len(desktops) {
					settings.Shortcuts = watchNextShortcut(settings.Shortcuts)
					applyShortcuts()
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				} else if i == len(monitors)+len(desktops)+2 {
					settings.FirstRowShortcut = watchNextFirstRowShortcut(settings.FirstRowShortcut)
					applyFirstRowShortcut()
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				} else if i == len(monitors)+len(desktops)+1 || i == len(monitors)+len(desktops)+3 {
					return 0
				}
				menu = false
				resize()
				return 0
			}
			// Desktop summaries only change the panel view. Never fall through
			// to the session activation path, including for non-toggle summaries.
			if p.Kind == "summary" {
				if summary := p.Summary; summary.Toggle {
					a.mu.Lock()
					if a.expanded == nil {
						a.expanded = map[string]bool{}
					}
					a.expanded[summary.DesktopID] = !a.expanded[summary.DesktopID]
					rows, summaries = watchPanelLayout(a.tracker.panelRows(), currentDesktop, desktops, a.expanded)
					a.save()
					a.mu.Unlock()
					offset = 0
					resize()
				}
				return 0
			}
			if p.Kind != "row" {
				return 0
			}
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
				return 0
			}
			if x >= 330 {
				a.mu.Lock()
				a.tracker.dismiss(row.Sample.PID)
				a.save()
				rows, summaries = watchPanelLayout(a.tracker.panelRows(), currentDesktop, desktops, a.expanded)
				a.mu.Unlock()
				watchFlash(row.Sample.HWND, false)
				a.logClick(p, releaseY, state, "dismissed", false, false)
				resize()
				return 0
			}
			a.activateWatchRow(p, releaseY)
			return 0
		case 0x0f: // WM_PAINT
			// A frame snapshot is valid for hit-testing only after every pixel
			// has been painted, including when Windows exposes a partial region.
			watchUser.NewProc("InvalidateRect").Call(window, 0, 1)
			var ps watchPaint
			dc, _, _ := watchUser.NewProc("BeginPaint").Call(window, uintptr(unsafe.Pointer(&ps)))
			frame := painted
			if !pressed.Active {
				frame = newWatchPanelFrame(rows, summaries, offset, capacity, menu)
				frame.Generation = painted.Generation + 1
				var rect watchRect
				watchUser.NewProc("GetWindowRect").Call(window, uintptr(unsafe.Pointer(&rect)))
				frame.PanelTop = rect.Top
			}
			watchGDI.NewProc("SetBkMode").Call(dc, 1)
			watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
			font, _, _ := watchGDI.NewProc("GetStockObject").Call(17)
			old, _, _ := watchGDI.NewProc("SelectObject").Call(dc, font)
			header := fmt.Sprintf("Agents | %d stopped", stoppedCount)
			if unknownCount > 0 {
				header = fmt.Sprintf("%d stopped | %d unknown", stoppedCount, unknownCount)
			}
			a.mu.Lock()
			problem := a.problem
			a.mu.Unlock()
			if problem != "" {
				header = "Agents | reader retrying"
				watchGDI.NewProc("SetTextColor").Call(dc, theme.rowColour("error"))
			}
			watchText(dc, header, 12, 8, 240, 30)
			watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
			settingsLabel := "Settings"
			if frame.Menu {
				settingsLabel = "Back"
			}
			watchText(dc, settingsLabel, 249, 8, 312, 30)
			watchText(dc, "Hide", 324, 8, 358, 30)
			if frame.Menu {
				selected := selectWatchMonitor(monitors, settings.Monitor)
				for i, m := range monitors {
					prefix := "   "
					if m.Device == selected.Device {
						prefix = "[x] "
					}
					top := int32(34 + i*44)
					watchText(dc, prefix+monitorLabel(m), 12, top+8, 350, top+35)
				}
				for i, d := range desktops {
					prefix := "[ ] "
					if settings.monitorsDesktop(d.ID) {
						prefix = "[x] "
					}
					top := int32(34 + (len(monitors)+i)*44)
					watchText(dc, prefix+"Monitor "+d.Name, 12, top+8, 350, top+35)
				}
				top := int32(34 + (len(monitors)+len(desktops))*44)
				watchText(dc, "Shortcuts: "+watchShortcutChoice(settings.Shortcuts)+"  [change]", 12, top+8, 350, top+35)
				top += 44
				hint := "1-9, 0: shown rows | app shortcuts may clash"
				if shortcutProblem != "" {
					hint = shortcutProblem
				}
				watchText(dc, hint, 12, top+8, 350, top+35)
				top += 44
				watchText(dc, "First row: "+watchFirstRowChoice(settings.FirstRowShortcut)+"  [change]", 12, top+8, 350, top+35)
				top += 44
				firstRowStatus := "Off | independent of numbered shortcuts"
				if watchFirstRowChoice(settings.FirstRowShortcut) != "Off" {
					firstRowStatus = "Registered: " + watchFirstRowChoice(settings.FirstRowShortcut)
				}
				if firstRowProblem != "" {
					firstRowStatus = firstRowProblem
				}
				watchText(dc, firstRowStatus, 12, top+8, 350, top+35)
				top += 44
				watchText(dc, "Reset location", 12, top+8, 350, top+35)
				top += 44
				watchText(dc, "Done | choices are remembered", 12, top+8, 350, top+35)
			} else {
				for i, row := range frame.Rows {
					top := int32(34 + i*watchRowHeight)
					age := time.Since(row.Changed)
					phase := row.Sample.Phase
					if phase == "" {
						phase = "idle"
						if row.Sample.State == "busy" {
							phase = "working"
						}
						if row.Sample.State == "prompt" {
							phase = "waiting"
						}
					}
					status := firstNonEmpty(row.Sample.Reason, phase)
					if phase == "working" {
						status = "RUNNING"
					}
					colour := theme.rowColour(phase)
					metadata := theme.colour(theme.Metadata)
					if prominent := watchProminentStatus(phase); prominent != "" {
						watchFill(dc, watchRect{0, top, 360, top + watchRowHeight - 1}, theme.rowColour(phase))
						colour, metadata = theme.colour("#191919"), theme.colour("#493713")
						status = prominent
						if inputFont != 0 && phase != "idle" && phase != "question" && phase != "review" {
							watchGDI.NewProc("SelectObject").Call(dc, inputFont)
						}
					}
					watchGDI.NewProc("SetTextColor").Call(dc, colour)
					watchText(dc, status, 12, top+2, 145, top+22)
					watchGDI.NewProc("SelectObject").Call(dc, font)
					watchText(dc, age.Round(time.Second).String(), 150, top+3, 224, top+22)
					watchText(dc, row.Sample.Context, 230, top+3, 328, top+22)
					watchGDI.NewProc("SetTextColor").Call(dc, colour)
					descriptionTop := top + 22
					if phase == "idle" || phase == "question" || phase == "review" || phase == "working" {
						descriptionTop = top + 16
					}
					if inputFont != 0 && phase != "working" {
						watchGDI.NewProc("SelectObject").Call(dc, inputFont)
					}
					watchText(dc, watchRowLabel(row.Sample), 12, descriptionTop, 328, descriptionTop+18)
					watchGDI.NewProc("SelectObject").Call(dc, font)
					watchGDI.NewProc("SetTextColor").Call(dc, metadata)
					meta := firstNonEmpty(row.Sample.Desktop, "Desktop ?")
					watchText(dc, meta+" | "+row.Sample.Agents.label(), 12, descriptionTop+18, 270, descriptionTop+35)
					watchText(dc, row.Sample.Tool, 276, descriptionTop+18, 350, descriptionTop+35)
					if shortcutProblem == "" && watchShortcutModifiers(settings.Shortcuts) != 0 {
						watchText(dc, watchShortcutDigit(i), 337, top+2, 358, top+20)
					}
					watchText(dc, "x", 336, top+26, 358, top+46)
					watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
				}
				for i, summary := range frame.Summaries {
					top := int32(34 + len(frame.Rows)*watchRowHeight + i*28)
					watchGDI.NewProc("SetTextColor").Call(dc, theme.colour(theme.Metadata))
					watchText(dc, summary.Text, 12, top+4, 350, top+26)
				}
			}
			watchGDI.NewProc("SelectObject").Call(dc, old)
			watchUser.NewProc("EndPaint").Call(window, uintptr(unsafe.Pointer(&ps)))
			if dc != 0 {
				painted = frame
			}
			return 0
		case 2:
			watchUser.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		r, _, _ := watchUser.NewProc("DefWindowProcW").Call(window, uintptr(msg), wp, lp)
		return r
	})
	class := watchClass{Proc: proc, Instance: uintptr(instance), Background: brush, Name: name}
	class.Size = uint32(unsafe.Sizeof(class))
	if r, _, err := watchUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); r == 0 {
		return fmt.Errorf("register panel: %v", err)
	}
	defer watchUser.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), uintptr(instance))
	// WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW | WS_EX_TOPMOST; WS_POPUP.
	window, _, err := watchUser.NewProc("CreateWindowExW").Call(0x08000088, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x80000000, 0, 0, 360, 34, 0, 0, uintptr(instance), 0)
	if window == 0 {
		return fmt.Errorf("create panel: %v", err)
	}
	hwnd = window
	applyShortcuts()
	applyFirstRowShortcut()
	defer func() {
		unregisterShortcut(watchFirstRowHotkey)
		for i := 0; i < 10; i++ {
			unregisterShortcut(watchHotkeyBase + uintptr(i))
		}
	}()
	icon, _, _ := watchUser.NewProc("LoadIconW").Call(uintptr(instance), 1)
	if icon == 0 {
		icon, _, _ = watchUser.NewProc("LoadIconW").Call(0, 32512)
	}
	tray = watchTrayData{HWND: hwnd, ID: 1, Callback: 0x8001, Icon: icon}
	if !tray.update(0, 0) {
		return fmt.Errorf("cannot create watcher tray icon")
	}
	defer tray.update(0, 2)
	resize()
	watchUser.NewProc("SetTimer").Call(hwnd, 1, 1000, 0)
	var msg watchMessage
	for {
		r, _, err := watchUser.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("panel message loop: %v", err)
		}
		if r == 0 {
			break
		}
		watchUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		watchUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}

func watchText(dc uintptr, text string, left, top, right, bottom int32) {
	r := watchRect{left, top, right, bottom}
	u := windows.StringToUTF16(text)
	watchUser.NewProc("DrawTextW").Call(dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), 0x20|0x8000|0x100) // single line, ellipsis, no & mnemonics
}

func watchFill(dc uintptr, rect watchRect, colour uintptr) {
	brush, _, _ := watchGDI.NewProc("CreateSolidBrush").Call(colour)
	watchUser.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&rect)), brush)
	watchGDI.NewProc("DeleteObject").Call(brush)
}
