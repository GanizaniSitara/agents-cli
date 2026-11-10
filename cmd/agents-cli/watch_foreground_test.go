package main

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Exercise the real native stacking operation without displaying or activating
// a window, touching another application, or changing virtual desktops.
func TestWatchForegroundLiftNativePlacement(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	hwnd, _, err := watchUser.NewProc("CreateWindowExW").Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), 0,
		0x00CF0000, 100, 100, 640, 420, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer watchUser.NewProc("DestroyWindow").Call(hwnd)
	var before, after watchRect
	watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&before)))
	watchFlashCaption(hwnd)
	watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&after)))
	actualForeground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if before != after || actualForeground != foreground {
		t.Fatal("caption flash changed placement or focus")
	}
	for _, topmost := range []bool{true, false} {
		if !watchSetLift(hwnd, topmost) {
			t.Fatal("native SetWindowPos failed")
		}
		if watchTopmost(hwnd) != topmost {
			t.Fatal("native topmost flag did not change")
		}
		watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&after)))
		if before != after {
			t.Fatalf("bounds changed: %+v -> %+v", before, after)
		}
		actual, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
		if actual != foreground {
			t.Fatal("stacking operation activated a window")
		}
	}
}

func TestWatchForegroundLiftLifecycle(t *testing.T) {
	l := watchForegroundLift{}
	states := map[uintptr]bool{}
	raises := 0
	set := func(h uintptr, on bool) bool {
		states[h] = on
		if on {
			raises++
		}
		return true
	}
	valid := func(watchSample) bool { return true }
	s := watchSample{Session: Session{PID: 1}, HWND: 11}
	l.selectWindow(s, true, false, valid, set)
	if !states[11] {
		t.Fatal("covered selected window not raised")
	}
	l.selectWindow(s, true, true, valid, set)
	if raises != 2 {
		t.Fatal("repeat selection failed to raise covered window again")
	}
	l.update(11, valid, set)
	if !states[11] {
		t.Fatal("repeat selection or retained focus released lift")
	}
	l.update(22, valid, set)
	if states[11] {
		t.Fatal("focus loss left window permanently topmost")
	}
	l.selectWindow(s, false, false, valid, set)
	if states[11] {
		t.Fatal("uncovered window promoted")
	}
	l.selectWindow(s, true, true, valid, set)
	if l.sample.HWND != 0 {
		t.Fatal("original topmost window enrolled for demotion")
	}
	l.selectWindow(s, true, false, valid, set)
	l.selectWindow(watchSample{Session: Session{PID: 2}, HWND: 22}, true, false, valid, set)
	if states[11] || !states[22] {
		t.Fatal("new selection did not release old lift")
	}
	l.release(valid, set)
	if states[22] {
		t.Fatal("shutdown left promotion behind")
	}
}

func TestWatchForegroundLiftIdentityAndFailure(t *testing.T) {
	l := watchForegroundLift{}
	s := watchSample{HWND: 11}
	l.selectWindow(s, true, false, func(watchSample) bool { return true }, func(uintptr, bool) bool { return false })
	if l.sample.HWND != 0 {
		t.Fatal("failed raise tracked as success")
	}
	l.sample = s
	called := false
	l.release(func(watchSample) bool { return false }, func(uintptr, bool) bool { called = true; return true })
	if called || l.sample.HWND != 0 {
		t.Fatal("reused or closed window was modified")
	}
}

func TestWatchForegroundOverlap(t *testing.T) {
	a := watchRect{0, 0, 100, 100}
	if !watchRectsOverlap(a, watchRect{50, 50, 150, 150}) || watchRectsOverlap(a, watchRect{100, 0, 200, 100}) {
		t.Fatal("overlap detection")
	}
}
