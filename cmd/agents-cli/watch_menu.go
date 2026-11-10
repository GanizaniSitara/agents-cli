package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	watchMenuShow = 1
	watchMenuExit = 2
)

func watchTrayMenu() uintptr {
	menu, _, _ := watchUser.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return 0
	}
	for _, item := range []struct {
		id   uintptr
		text string
	}{{watchMenuShow, "Show"}, {watchMenuExit, "Exit"}} {
		ok, _, _ := watchUser.NewProc("AppendMenuW").Call(menu, 0, item.id, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(item.text))))
		if ok == 0 {
			watchUser.NewProc("DestroyMenu").Call(menu)
			return 0
		}
	}
	return menu
}

// Only invoked by an explicit tray right-click. Restore the previous focus
// after the native menu closes; never activate an agent or switch desktops.
func watchShowTrayMenu(window uintptr) uintptr {
	menu := watchTrayMenu()
	if menu == 0 {
		return 0
	}
	defer watchUser.NewProc("DestroyMenu").Call(menu)
	var point watchPoint
	watchUser.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	previous, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	watchUser.NewProc("SetForegroundWindow").Call(window)
	command, _, _ := watchUser.NewProc("TrackPopupMenu").Call(menu, 0x100|0x2, uintptr(point.X), uintptr(point.Y), 0, window, 0)
	watchUser.NewProc("PostMessageW").Call(window, 0, 0, 0)
	current, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if previous != 0 && previous != window && current == window {
		watchUser.NewProc("SetForegroundWindow").Call(previous)
	}
	return command
}

func watchRequestExit(window uintptr) {
	// WM_CLOSE takes the normal DestroyWindow/WM_DESTROY cleanup path.
	watchUser.NewProc("PostMessageW").Call(window, 0x10, 0, 0)
}
