package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// A ConPTY console window is a hidden compatibility window. Follow only its
// actual ownership chain, never a title match or a process-name guess.
func watchResolveConsole(hwnd uintptr, visible func(uintptr) bool, owner func(uintptr) uintptr, class func(uintptr) string) uintptr {
	if hwnd == 0 {
		return 0
	}
	root := owner(hwnd)
	if root != 0 && root != hwnd && class(root) == "CASCADIA_HOSTING_WINDOW_CLASS" && visible(root) {
		return root
	}
	if visible(hwnd) {
		return hwnd
	}
	return 0
}

func watchWindowClass(hwnd uintptr) string {
	var name [256]uint16
	watchUser.NewProc("GetClassNameW").Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	return windows.UTF16ToString(name[:])
}

func watchConsolePresentation(hwnd uintptr) uintptr {
	return watchResolveConsole(hwnd, func(h uintptr) bool {
		visible, _, _ := watchUser.NewProc("IsWindowVisible").Call(h)
		return visible != 0
	}, func(h uintptr) uintptr {
		owner, _, _ := watchUser.NewProc("GetAncestor").Call(h, 3) // GA_ROOTOWNER
		return owner
	}, watchWindowClass)
}
