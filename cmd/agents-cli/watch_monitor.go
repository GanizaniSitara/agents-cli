package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type watchMonitor struct {
	Device  string    `json:"device"`
	Number  int       `json:"number"`
	Bounds  watchRect `json:"bounds"`
	Work    watchRect `json:"work"`
	Primary bool      `json:"primary"`
}

type watchSettings struct {
	DisableSelectionBlink bool        `json:"disable_selection_blink,omitempty"`
	DisableForeground     bool        `json:"disable_foreground,omitempty"`
	DisableDesktopSwitch  bool        `json:"disable_desktop_switch,omitempty"`
	Shortcuts             string      `json:"shortcuts,omitempty"`
	FirstRowShortcut      string      `json:"first_row_shortcut,omitempty"`
	Position              *watchPoint `json:"position,omitempty"`
	Monitor               string      `json:"monitor"`
	Hidden                bool        `json:"hidden"`
	ExcludedDesktops      []string    `json:"excluded_desktops,omitempty"`
}

func watchPositionMonitor(monitors []watchMonitor, settings watchSettings) watchMonitor {
	if p := settings.Position; p != nil {
		for _, m := range monitors {
			if p.X >= m.Work.Left && p.X < m.Work.Right && p.Y >= m.Work.Top && p.Y < m.Work.Bottom {
				return m
			}
		}
	}
	return selectWatchMonitor(monitors, settings.Monitor)
}

func watchPanelPosition(area watchRect, width, height int32, saved *watchPoint) watchPoint {
	p := watchPoint{area.Right - width - 12, area.Bottom - height - 12}
	if saved != nil {
		p = *saved
	}
	if p.X > area.Right-width {
		p.X = area.Right - width
	}
	if p.Y > area.Bottom-height {
		p.Y = area.Bottom - height
	}
	if p.X < area.Left {
		p.X = area.Left
	}
	if p.Y < area.Top {
		p.Y = area.Top
	}
	return p
}

func watchMonitors() []watchMonitor {
	var monitors []watchMonitor
	cb := syscall.NewCallback(func(handle, dc, rect, param uintptr) uintptr {
		var info struct {
			Size         uint32
			Bounds, Work watchRect
			Flags        uint32
			Device       [32]uint16
		}
		info.Size = uint32(unsafe.Sizeof(info))
		if ok, _, _ := watchUser.NewProc("GetMonitorInfoW").Call(handle, uintptr(unsafe.Pointer(&info))); ok != 0 {
			device := windows.UTF16ToString(info.Device[:])
			number, _ := strconv.Atoi(strings.TrimPrefix(device, `\\.\DISPLAY`))
			monitors = append(monitors, watchMonitor{Device: device, Number: number, Bounds: info.Bounds, Work: info.Work, Primary: info.Flags&1 != 0})
		}
		return 1
	})
	watchUser.NewProc("EnumDisplayMonitors").Call(0, 0, cb, 0)
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].Number < monitors[j].Number })
	return monitors
}

func selectWatchMonitor(monitors []watchMonitor, device string) watchMonitor {
	for _, m := range monitors {
		if m.Device == device {
			return m
		}
	}
	for _, m := range monitors {
		if m.Primary {
			return m
		}
	}
	if len(monitors) > 0 {
		return monitors[0]
	}
	return watchMonitor{Work: watchRect{Right: 800, Bottom: 600}}
}

func monitorLabel(m watchMonitor) string {
	w, h := m.Bounds.Right-m.Bounds.Left, m.Bounds.Bottom-m.Bounds.Top
	orientation := "landscape"
	if h > w {
		orientation = "portrait"
	}
	return fmt.Sprintf("Monitor %d | %s | %d x %d", m.Number, orientation, w, h)
}

func loadWatchSettings(dir string) watchSettings {
	settings := watchSettings{FirstRowShortcut: watchDefaultFirstRowShortcut}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &settings)
	}
	return settings
}

func saveWatchSettings(dir string, s watchSettings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Readers must see the previous or the new desktop filter, never a
	// partially written JSON file that would momentarily enable all desktops.
	tmp := filepath.Join(dir, "settings.tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return windows.MoveFileEx(windows.StringToUTF16Ptr(tmp), windows.StringToUTF16Ptr(filepath.Join(dir, "settings.json")), windows.MOVEFILE_REPLACE_EXISTING)
}

// NOTIFYICONDATAW, full Win7+ layout (pointer aligned on both x86 and x64).
type watchTrayData struct {
	Size                uint32
	HWND                uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	Title               [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}

func (n *watchTrayData) update(count int, op uintptr) bool {
	n.Size = uint32(unsafe.Sizeof(*n))
	n.Flags = 1 | 2 | 4 // callback, icon, tooltip; never balloon/sound
	n.Tip = [128]uint16{}
	copy(n.Tip[:], windows.StringToUTF16(fmt.Sprintf("Agents | %d stopped | click to open", count)))
	ok, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW").Call(op, uintptr(unsafe.Pointer(n)))
	return ok != 0
}
