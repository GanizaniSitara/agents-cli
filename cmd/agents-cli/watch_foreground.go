package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var watchDWM = windows.NewLazySystemDLL("dwmapi.dll")

func watchMovePointer(s watchSample) bool {
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if foreground != s.HWND || !watchLiftIdentity(s) {
		return false
	}
	var rect watchRect
	if ok, _, _ := watchUser.NewProc("GetClientRect").Call(s.HWND, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return false
	}
	point := watchPoint{(rect.Left + rect.Right) / 2, (rect.Top + rect.Bottom) / 2}
	if ok, _, _ := watchUser.NewProc("ClientToScreen").Call(s.HWND, uintptr(unsafe.Pointer(&point))); ok == 0 {
		return false
	}
	ok, _, _ := watchUser.NewProc("SetCursorPos").Call(uintptr(point.X), uintptr(point.Y))
	return ok != 0
}

// Only explicit selections may temporarily join the topmost band. The panel
// thread owns this state and releases it when focus leaves or the watcher exits.
type watchForegroundLift struct {
	sample watchSample
}

func (l *watchForegroundLift) release(valid func(watchSample) bool, set func(uintptr, bool) bool) {
	if l.sample.HWND != 0 && valid(l.sample) {
		set(l.sample.HWND, false)
	}
	l.sample = watchSample{}
}

func (l *watchForegroundLift) selectWindow(s watchSample, covered, topmost bool, valid func(watchSample) bool, set func(uintptr, bool) bool) {
	if l.sample.HWND == s.HWND && watchSameClickSession(l.sample, s) {
		// Another topmost app may have covered it since the previous selection.
		// Raise it again while retaining ownership of the original promotion.
		if valid(s) {
			set(s.HWND, true)
		}
		return
	}
	l.release(valid, set)
	if covered && !topmost && set(s.HWND, true) {
		l.sample = s
	}
}

func (l *watchForegroundLift) update(foreground uintptr, valid func(watchSample) bool, set func(uintptr, bool) bool) {
	if l.sample.HWND != 0 && foreground != l.sample.HWND {
		l.release(valid, set)
	}
}

func watchLiftIdentity(s watchSample) bool {
	exists, _, _ := watchUser.NewProc("IsWindow").Call(s.HWND)
	pid := watchWindowPID(s.HWND)
	return exists != 0 && pid == s.WindowPID && startTime(pid).Format(time.RFC3339Nano) == s.WindowStarted
}

func watchSetLift(hwnd uintptr, topmost bool) bool {
	insertAfter := ^uintptr(1) // HWND_NOTOPMOST
	if topmost {
		insertAfter = ^uintptr(0) // HWND_TOPMOST
	}
	// Never restore, move, resize or activate as a side effect of stacking.
	ok, _, _ := watchUser.NewProc("SetWindowPos").Call(hwnd, insertAfter, 0, 0, 0, 0, 0x1|0x2|0x10|0x200|0x4000)
	return ok != 0
}

func watchRectsOverlap(a, b watchRect) bool {
	return a.Left < b.Right && b.Left < a.Right && a.Top < b.Bottom && b.Top < a.Bottom
}

func watchTopmost(hwnd uintptr) bool {
	ex, _, _ := watchUser.NewProc("GetWindowLongPtrW").Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE -20
	return ex&8 != 0
}

func watchCoveredByTopmost(target uintptr) bool {
	var rect watchRect
	if ok, _, _ := watchUser.NewProc("GetWindowRect").Call(target, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return false
	}
	hwnd, _, _ := watchUser.NewProc("GetTopWindow").Call(0)
	for n := 0; hwnd != 0 && hwnd != target && n < 512; n++ {
		visible, _, _ := watchUser.NewProc("IsWindowVisible").Call(hwnd)
		ex, _, _ := watchUser.NewProc("GetWindowLongPtrW").Call(hwnd, ^uintptr(19))
		style, _, _ := watchUser.NewProc("GetWindowLongPtrW").Call(hwnd, ^uintptr(15)) // GWL_STYLE -16
		// Ordinary application windows only: exclude the watcher, taskbar,
		// remote-desktop privacy screens and transparent helper overlays.
		if visible != 0 && ex&8 != 0 && ex&0x80 == 0 && style&0x00C00000 != 0 {
			var cloaked uint32
			watchDWM.NewProc("DwmGetWindowAttribute").Call(hwnd, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
			var other watchRect
			watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&other)))
			if cloaked == 0 && watchRectsOverlap(rect, other) {
				return true
			}
		}
		hwnd, _, _ = watchUser.NewProc("GetWindow").Call(hwnd, 2) // GW_HWNDNEXT
	}
	return false
}
