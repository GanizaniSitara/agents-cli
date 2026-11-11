package main

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWatchNativeConsoleOwnerAndPointerGuard(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	var cursor watchPoint
	watchUser.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	class := windows.StringToUTF16Ptr("STATIC")
	create := func(owner uintptr) uintptr {
		h, _, err := watchUser.NewProc("CreateWindowExW").Call(0x08000080, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 100, 100, owner, 0, 0, 0)
		if h == 0 {
			t.Fatal(err)
		}
		return h
	}
	owner := create(0)
	defer watchUser.NewProc("DestroyWindow").Call(owner)
	console := create(owner)
	defer watchUser.NewProc("DestroyWindow").Call(console)
	got := watchResolveConsole(console, func(h uintptr) bool { return h == owner }, func(h uintptr) uintptr {
		root, _, _ := watchUser.NewProc("GetAncestor").Call(h, 3)
		return root
	}, func(h uintptr) string {
		if h == owner {
			return "CASCADIA_HOSTING_WINDOW_CLASS"
		}
		return "ConsoleWindowClass"
	})
	if got != owner {
		t.Fatal("native root-owner chain did not resolve presentation")
	}
	if watchMovePointer(watchSample{HWND: owner}) {
		t.Fatal("pointer moved for a background/invalid target")
	}
	after, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	var point watchPoint
	watchUser.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	if after != foreground || point != cursor {
		t.Fatal("hidden owner test changed focus or pointer")
	}
}

func TestWatchConsolePresentationOwnership(t *testing.T) {
	visible := map[uintptr]bool{1: true, 3: true}
	owners := map[uintptr]uintptr{1: 1, 2: 3, 4: 5, 6: 3}
	classes := map[uintptr]string{3: "CASCADIA_HOSTING_WINDOW_CLASS", 5: "OtherApp"}
	for hwnd, want := range map[uintptr]uintptr{0: 0, 1: 1, 2: 3, 4: 0, 6: 3} {
		got := watchResolveConsole(hwnd, func(h uintptr) bool { return visible[h] }, func(h uintptr) uintptr { return owners[h] }, func(h uintptr) string { return classes[h] })
		if got != want {
			t.Fatalf("console %d mapped to %d, want %d", hwnd, got, want)
		}
	}
	visible[3] = false
	if got := watchResolveConsole(2, func(h uintptr) bool { return visible[h] }, func(h uintptr) uintptr { return owners[h] }, func(h uintptr) string { return classes[h] }); got != 0 {
		t.Fatal("unavailable Terminal presentation accepted")
	}
}

func TestWatchTerminalTabUniqueCurrentTitle(t *testing.T) {
	for _, test := range []struct {
		title   string
		names   []string
		index   int
		problem string
	}{
		{"MY-001", []string{"OTHER", "MY-001"}, 1, ""},
		{"MY-001", []string{"MY-001", "OTHER"}, 0, ""},
		{"MY-001", []string{"MY-001", "MY-001"}, -1, "terminal-tab-ambiguous"},
		{"MY-001", []string{"MY-001 extra", "my-001"}, -1, "terminal-tab-not-found"},
		{"MY-001", []string{"renamed by user"}, -1, "terminal-tab-not-found"},
		{"", []string{""}, -1, "terminal-tab-title-empty"},
		{"MY-001", nil, -1, "terminal-tab-not-found"},
	} {
		index, problem := watchTerminalTabIndex(test.title, test.names)
		if index != test.index || problem != test.problem {
			t.Fatalf("%q in %q: %d / %s", test.title, test.names, index, problem)
		}
	}
}
