package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	watchRowShelve = 102 + iota
	watchRowRename
	watchRowCancelShelve
	watchRowPin
	watchRowParkDesktop
)

func watchRowMenu(parked, pinned bool) uintptr {
	menu, _, _ := watchUser.NewProc("CreatePopupMenu").Call()
	appendItem := func(id uintptr, text string) {
		watchUser.NewProc("AppendMenuW").Call(menu, 0, id, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}
	if !parked && !pinned {
		appendItem(watchRowPin, "Pin to top")
	}
	appendItem(watchRowRename, "Rename…")
	appendItem(watchRowShelve, "Shelve…")
	appendItem(watchRowCancelShelve, "Cancel pending shelving")
	if !parked && !pinned {
		appendItem(watchRowParkDesktop, "Park to Desktop 2")
	}
	return menu
}

func watchShowRowMenu(window uintptr, row watchEntry, point watchPoint) uintptr {
	menu := watchRowMenu(row.Parked, row.Pinned)
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
