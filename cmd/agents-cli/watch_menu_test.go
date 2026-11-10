package main

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWatchTrayMenuNativeItems(t *testing.T) {
	menu := watchTrayMenu()
	if menu == 0 {
		t.Fatal("native menu creation failed")
	}
	defer watchUser.NewProc("DestroyMenu").Call(menu)
	count, _, _ := watchUser.NewProc("GetMenuItemCount").Call(menu)
	if count != 2 {
		t.Fatalf("menu item count %d", count)
	}
	for i, want := range []string{"Show", "Exit"} {
		var text [64]uint16
		watchUser.NewProc("GetMenuStringW").Call(menu, uintptr(i), uintptr(unsafe.Pointer(&text[0])), 64, 0x400)
		if got := windows.UTF16ToString(text[:]); got != want {
			t.Fatalf("menu item = %q, want %q", got, want)
		}
	}
	id, _, _ := watchUser.NewProc("GetMenuItemID").Call(menu, 1)
	if id != watchMenuExit {
		t.Fatal("Exit command not wired")
	}
}

func TestWatchExitNativeClose(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	hwnd, _, err := watchUser.NewProc("CreateWindowExW").Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), 0,
		0x80000000, 0, 0, 100, 100, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer watchUser.NewProc("DestroyWindow").Call(hwnd)
	watchRequestExit(hwnd)
	var msg watchMessage
	for {
		pending, _, _ := watchUser.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), hwnd, 0, 0, 1)
		if pending == 0 {
			break
		}
		watchUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	alive, _, _ := watchUser.NewProc("IsWindow").Call(hwnd)
	if alive != 0 {
		t.Fatal("Exit did not close the owned window")
	}
	after, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if after != foreground {
		t.Fatal("Exit changed foreground focus")
	}
}
