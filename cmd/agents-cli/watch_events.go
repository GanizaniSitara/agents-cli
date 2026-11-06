package main

import (
	"syscall"
	"unsafe"
)

// Console accessibility update events invalidate idle screen snapshots. Busy
// windows still get sampled every second. A periodic reread covers missed
// events and hosts that do not implement console accessibility.
func watchConsoleEvents() (func() map[uintptr]bool, func(), bool) {
	dirty := map[uintptr]bool{}
	callback := syscall.NewCallback(func(hook, event, hwnd, object, child, thread, at uintptr) uintptr {
		dirty[hwnd] = true
		return 0
	})
	hook, _, _ := watchUser.NewProc("SetWinEventHook").Call(0x4002, 0x4005, 0, callback, 0, 0, 2)
	drain := func() map[uintptr]bool {
		var msg watchMessage
		// Bound message processing even if a console emits events continuously.
		for i := 0; i < 256; i++ {
			ok, _, _ := watchUser.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
			if ok == 0 {
				break
			}
			watchUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
		result := dirty
		dirty = map[uintptr]bool{}
		return result
	}
	return drain, func() {
		if hook != 0 {
			watchUser.NewProc("UnhookWinEvent").Call(hook)
		}
	}, hook != 0
}
