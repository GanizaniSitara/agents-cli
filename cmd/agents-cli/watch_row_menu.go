package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	watchRowShelve = 102 + iota
	watchRowRename
	watchRowCancelShelve
)

func watchRowMenu(_ bool) uintptr {
	menu, _, _ := watchUser.NewProc("CreatePopupMenu").Call()
	for _, item := range []struct {
		id   uintptr
		text string
	}{{watchRowRename, "Rename…"}, {watchRowShelve, "Shelve…"}, {watchRowCancelShelve, "Cancel pending shelving"}} {
		watchUser.NewProc("AppendMenuW").Call(menu, 0, item.id, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(item.text))))
	}
	return menu
}

func watchShowRowMenu(window uintptr, row watchEntry, point watchPoint) uintptr {
	menu := watchRowMenu(row.Parked)
	if menu == 0 {
		return 0
	}
	defer watchUser.NewProc("DestroyMenu").Call(menu)
	previous, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	watchUser.NewProc("SetForegroundWindow").Call(window)
	id, _, _ := watchUser.NewProc("TrackPopupMenu").Call(menu, 0x100|0x2, uintptr(point.X), uintptr(point.Y), 0, window, 0)
	// Complete the menu loop before the next popup (TrackPopupMenu contract).
	watchUser.NewProc("PostMessageW").Call(window, 0, 0, 0)
	current, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if previous != 0 && previous != window && current == window {
		watchUser.NewProc("SetForegroundWindow").Call(previous)
	}
	return id
}
