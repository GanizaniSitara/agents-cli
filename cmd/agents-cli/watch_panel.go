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
	var rows []watchEntry
	var summaries []watchPanelSummary
	var painted watchPanelFrame
	var pressed watchPanelPress
	capacity := 1
	currentDesktop := watchCurrentDesktop()
	var previousStates map[uint32]string
	wakeReady := false
	var tray watchTrayData
	resize := func() {
		if pressed.Active {
			return
		} // keep the panel still through mouse-up
		area := selectWatchMonitor(monitors, settings.Monitor).Work
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
			n = len(monitors) + len(desktops) + 1
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
		watchUser.NewProc("SetWindowPos").Call(hwnd, ^uintptr(0), uintptr(area.Right-width-12), uintptr(area.Bottom-height-12), uintptr(width), uintptr(height), flags)
		watchUser.NewProc("InvalidateRect").Call(hwnd, 0, 1)
	}
	brush, _, _ := watchGDI.NewProc("CreateSolidBrush").Call(theme.colour(theme.Background))
	defer watchGDI.NewProc("DeleteObject").Call(brush)
	fontHeight := int32(-15)
	inputFont, _, _ := watchGDI.NewProc("CreateFontW").Call(uintptr(fontHeight), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Segoe UI"))))
	defer watchGDI.NewProc("DeleteObject").Call(inputFont)
	proc := syscall.NewCallback(func(window uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
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
			if !settings.Hidden && !pressed.Active {
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
			pressed = painted.hit(int(int16(lp)), int(int16(lp>>16)))
			if pressed.Active {
				pressed.Time = time.Now()
				pressed.Screen = watchPoint{int32(pressed.X), int32(pressed.Y)}
				watchUser.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&pressed.Screen)))
				watchUser.NewProc("SetCapture").Call(window)
			}
			return 0
		case 0x215, 0x1f: // capture lost / cancel mode: never activate
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
					_ = saveWatchSettings(a.dir, settings)
				} else if i >= len(monitors) && i < len(monitors)+len(desktops) {
					settings.toggleDesktop(desktops[i-len(monitors)].ID)
					_ = saveWatchSettings(a.dir, settings)
					resize()
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
			// SwitchToThisWindow follows the shell's user-requested window switch,
			// including virtual desktops. No background path calls this API.
			watchFlash(row.Sample.HWND, false)
			watchUser.NewProc("ShowWindowAsync").Call(row.Sample.HWND, 9)
			watchUser.NewProc("SwitchToThisWindow").Call(row.Sample.HWND, 1)
			set, _, _ := watchUser.NewProc("SetForegroundWindow").Call(row.Sample.HWND)
			a.logClick(p, releaseY, state, "activation-requested", true, set != 0)
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
			watchText(dc, "Settings", 249, 8, 312, 30)
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
						if inputFont != 0 && phase != "idle" {
							watchGDI.NewProc("SelectObject").Call(dc, inputFont)
						}
					}
					watchGDI.NewProc("SetTextColor").Call(dc, colour)
					watchText(dc, status, 12, top+2, 145, top+22)
					watchGDI.NewProc("SelectObject").Call(dc, font)
					watchText(dc, age.Round(time.Second).String(), 150, top+3, 224, top+22)
					watchText(dc, row.Sample.Context, 230, top+3, 328, top+22)
					watchGDI.NewProc("SetTextColor").Call(dc, colour)
					if inputFont != 0 {
						watchGDI.NewProc("SelectObject").Call(dc, inputFont)
					}
					watchText(dc, watchRowLabel(row.Sample), 12, top+22, 328, top+40)
					watchGDI.NewProc("SelectObject").Call(dc, font)
					watchGDI.NewProc("SetTextColor").Call(dc, metadata)
					meta := firstNonEmpty(row.Sample.Desktop, "Desktop ?")
					watchText(dc, meta+" | "+row.Sample.Agents.label(), 12, top+38, 270, top+55)
					watchText(dc, row.Sample.Tool, 276, top+38, 350, top+55)
					watchText(dc, "x", 336, top+9, 358, top+34)
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
	icon, _, _ := watchUser.NewProc("LoadIconW").Call(0, 32512)
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
