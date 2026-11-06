package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	stoppedCount := 0
	unknownCount := 0
	lastCount := -1
	lastRender := ""
	offset := 0
	var hwnd uintptr
	var rows []watchEntry
	var tray watchTrayData
	resize := func() {
		area := selectWatchMonitor(monitors, settings.Monitor).Work
		n := len(rows)
		if n > 6 {
			n = 6
		}
		if menu {
			n = len(monitors) + len(desktops) + 3
		}
		height := int32(34 + n*watchRowHeight)
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
	brush, _, _ := watchGDI.NewProc("CreateSolidBrush").Call(theme.colour(theme.Background, 0))
	defer watchGDI.NewProc("DeleteObject").Call(brush)
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
			a.mu.Lock()
			rows = a.tracker.panelRows()
			stoppedCount = len(a.tracker.rows())
			unknownCount = 0
			for _, states := range a.coverage {
				unknownCount += states["unknown"]
			}
			a.mu.Unlock()
			if stoppedCount != lastCount {
				tray.update(stoppedCount, 1)
				lastCount = stoppedCount
			}
			if offset >= len(rows) {
				offset = 0
			}
			if !settings.Hidden {
				var key strings.Builder
				fmt.Fprintf(&key, "%d/%d/%d/", stoppedCount, unknownCount, time.Now().Unix()/10)
				for _, row := range rows {
					fmt.Fprintf(&key, "%d/%s/%s/%s/%s/", row.Sample.PID, row.Sample.State, row.Sample.Desktop, row.Sample.Phase, row.Sample.Agents.label())
				}
				if key.String() != lastRender {
					lastRender = key.String()
					resize()
				}
			}
			return 0
		case 0x20a: // WM_MOUSEWHEEL, scroll a bounded viewport
			if int16(wp>>16) < 0 {
				if offset+6 < len(rows) {
					offset++
				}
			} else if offset > 0 {
				offset--
			}
			watchUser.NewProc("InvalidateRect").Call(window, 0, 1)
			return 0
		case 0x202: // WM_LBUTTONUP: the only route to foreground activation
			x, y := int(int16(lp)), int(int16(lp>>16))
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
			if menu {
				i := (y - 34) / 44
				if i >= 0 && i < len(monitors) {
					settings.Monitor = monitors[i].Device
					_ = saveWatchSettings(a.dir, settings)
				} else if i >= len(monitors) && i < len(monitors)+len(desktops) {
					settings.toggleDesktop(desktops[i-len(monitors)].ID)
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				} else if i == len(monitors)+len(desktops) {
					settings.ColoursOff = !settings.ColoursOff
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				} else if i == len(monitors)+len(desktops)+1 {
					settings.TrustIdle = !settings.TrustIdle
					_ = saveWatchSettings(a.dir, settings)
					resize()
					return 0
				}
				menu = false
				resize()
				return 0
			}
			i := offset + (y-34)/watchRowHeight
			if i < 0 || i >= len(rows) {
				return 0
			}
			row := rows[i]
			if startTime(row.Sample.PID).Format(time.RFC3339) != normalizeWatchStart(row.Sample.Started) {
				return 0
			}
			if x >= 330 {
				a.mu.Lock()
				a.tracker.dismiss(row.Sample.PID)
				a.save()
				rows = a.tracker.panelRows()
				a.mu.Unlock()
				watchFlash(row.Sample.HWND, false)
				resize()
				return 0
			}
			// SwitchToThisWindow follows the shell's user-requested window switch,
			// including virtual desktops. No background path calls this API.
			watchFlash(row.Sample.HWND, false)
			watchUser.NewProc("ShowWindowAsync").Call(row.Sample.HWND, 9)
			watchUser.NewProc("SwitchToThisWindow").Call(row.Sample.HWND, 1)
			watchUser.NewProc("SetForegroundWindow").Call(row.Sample.HWND)
			return 0
		case 0x0f: // WM_PAINT
			var ps watchPaint
			dc, _, _ := watchUser.NewProc("BeginPaint").Call(window, uintptr(unsafe.Pointer(&ps)))
			watchGDI.NewProc("SetBkMode").Call(dc, 1)
			watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
			font, _, _ := watchGDI.NewProc("GetStockObject").Call(17)
			old, _, _ := watchGDI.NewProc("SelectObject").Call(dc, font)
			header := fmt.Sprintf("Agents · %d stopped", stoppedCount)
			if unknownCount > 0 {
				header = fmt.Sprintf("%d stopped · %d unknown", stoppedCount, unknownCount)
			}
			a.mu.Lock()
			problem := a.problem
			a.mu.Unlock()
			if problem != "" {
				header = "Agents · reader retrying"
				watchGDI.NewProc("SetTextColor").Call(dc, theme.rowColour("error", 0, settings.ColoursOff, settings.TrustIdle))
			}
			watchText(dc, header, 12, 8, 240, 30)
			watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
			watchText(dc, "Settings", 249, 8, 312, 30)
			watchText(dc, "Hide", 324, 8, 358, 30)
			if menu {
				selected := selectWatchMonitor(monitors, settings.Monitor)
				for i, m := range monitors {
					prefix := "   "
					if m.Device == selected.Device {
						prefix = "✓ "
					}
					top := int32(34 + i*44)
					watchText(dc, prefix+monitorLabel(m), 12, top+8, 350, top+35)
				}
				for i, d := range desktops {
					prefix := "☐ "
					if settings.monitorsDesktop(d.ID) {
						prefix = "☑ "
					}
					top := int32(34 + (len(monitors)+i)*44)
					watchText(dc, prefix+"Monitor "+d.Name, 12, top+8, 350, top+35)
				}
				top := int32(34 + (len(monitors)+len(desktops))*44)
				colours := "on"
				if settings.ColoursOff {
					colours = "off"
				}
				watchText(dc, "Trial colours: "+colours+" · click to toggle", 12, top+8, 350, top+35)
				idle := "cautious amber"
				if settings.TrustIdle {
					idle = "green"
				}
				watchText(dc, "Idle colour: "+idle, 12, top+44+8, 350, top+44+35)
				top += 88
				watchText(dc, "Done · choices are remembered", 12, top+8, 350, top+35)
			} else {
				end := offset + 6
				if end > len(rows) {
					end = len(rows)
				}
				for i := offset; i < end; i++ {
					row := rows[i]
					top := int32(34 + (i-offset)*watchRowHeight)
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
					if phase == "idle" && settings.TrustIdle {
						status = "idle"
					}
					colour := theme.rowColour(phase, age, settings.ColoursOff, settings.TrustIdle)
					x := int32(12)
					meta := strings.Replace(row.Sample.Desktop, "Desktop ", "D", 1)
					watchSegment(dc, row.Sample.Tool+" · "+meta+" · ", &x, top+3, 328, theme.colour(theme.Metadata, age))
					watchSegment(dc, fmt.Sprintf("%s · %s", status, age.Round(time.Second)), &x, top+3, 328, colour)
					watchGDI.NewProc("SetTextColor").Call(dc, colour)
					watchText(dc, firstNonEmpty(row.Sample.Name, row.Sample.Title, fmt.Sprint(row.Sample.PID)), 12, top+22, 328, top+42)
					watchGDI.NewProc("SetTextColor").Call(dc, theme.colour(theme.Metadata, age))
					watchText(dc, row.Sample.Agents.label(), 12, top+38, 328, top+55)
					watchText(dc, "×", 336, top+9, 358, top+34)
					watchGDI.NewProc("SetTextColor").Call(dc, 0xf2f0ed)
				}
			}
			watchGDI.NewProc("SelectObject").Call(dc, old)
			watchUser.NewProc("EndPaint").Call(window, uintptr(unsafe.Pointer(&ps)))
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

func watchSegment(dc uintptr, text string, x *int32, top, right int32, colour uintptr) {
	watchGDI.NewProc("SetTextColor").Call(dc, colour)
	watchText(dc, text, *x, top, right, top+20)
	u := windows.StringToUTF16(text)
	var size struct{ X, Y int32 }
	watchGDI.NewProc("GetTextExtentPoint32W").Call(dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&size)))
	*x += size.X
}
