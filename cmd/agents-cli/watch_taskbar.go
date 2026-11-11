package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows supports green (normal), red (error), and amber (paused) fills.
// There is no arbitrary RGB/blue progress-fill state.
type watchTaskbarCOM struct{ VTable *[21]uintptr }

func openWatchTaskbar() (*watchTaskbarCOM, func()) {
	ole := windows.NewLazySystemDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0)
	if int32(hr) < 0 {
		return nil, func() {}
	}
	cleanup := func() { ole.NewProc("CoUninitialize").Call() }
	clsid, _ := windows.GUIDFromString("{56FDF344-FD6D-11D0-958A-006097C9A090}")
	iid, _ := windows.GUIDFromString("{EA1AFB91-9E28-4B86-90E9-9E9F8A5EEFAF}")
	var taskbar *watchTaskbarCOM
	hr, _, _ = ole.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&taskbar)))
	if int32(hr) < 0 {
		cleanup()
		return nil, func() {}
	}
	hr, _, _ = syscall.SyscallN(taskbar.VTable[3], uintptr(unsafe.Pointer(taskbar)))
	if int32(hr) < 0 {
		syscall.SyscallN(taskbar.VTable[2], uintptr(unsafe.Pointer(taskbar)))
		cleanup()
		return nil, func() {}
	}
	return taskbar, func() { syscall.SyscallN(taskbar.VTable[2], uintptr(unsafe.Pointer(taskbar))); cleanup() }
}

func (t *watchTaskbarCOM) set(hwnd uintptr, state uint32) bool {
	if t == nil {
		return false
	}
	hr, _, _ := syscall.SyscallN(t.VTable[10], uintptr(unsafe.Pointer(t)), hwnd, uintptr(state))
	if int32(hr) < 0 {
		return false
	}
	if state != 0 {
		hr, _, _ = syscall.SyscallN(t.VTable[9], uintptr(unsafe.Pointer(t)), hwnd, 1, 1)
	}
	return int32(hr) >= 0
}

type watchTaskbarController struct{ applied map[uintptr]uint32 }

func watchTaskbarState(entry *watchEntry, current string) uint32 {
	s := entry.Sample
	if current == "" || s.DesktopID != current || !entry.Stopped || entry.Dismissed || entry.Parked {
		return 0
	}
	switch {
	case s.State == "error" || s.Phase == "error":
		return 4
	case s.State == "prompt" || s.Phase == "waiting" || s.Phase == "question" || s.Phase == "review":
		return 8
	case s.Phase == "finished":
		return 2
	}
	return 0
}

func (c *watchTaskbarController) update(entries map[uint32]*watchEntry, current string, set func(uintptr, uint32) bool) {
	if c.applied == nil {
		c.applied = map[uintptr]uint32{}
	}
	alive := map[uintptr]bool{}
	for _, entry := range entries {
		hwnd := entry.Sample.HWND
		if hwnd == 0 {
			continue
		}
		alive[hwnd] = true
		state := watchTaskbarState(entry, current)
		if previous, ok := c.applied[hwnd]; !ok || previous != state {
			if set(hwnd, state) {
				c.applied[hwnd] = state
			}
		}
	}
	for hwnd, state := range c.applied {
		if !alive[hwnd] {
			if state != 0 {
				set(hwnd, 0)
			}
			delete(c.applied, hwnd)
		}
	}
}
