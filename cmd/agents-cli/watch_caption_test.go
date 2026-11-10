package main

import (
	"golang.org/x/sys/windows"
	"runtime"
	"testing"
	"unsafe"
)

func TestWatchCaptionNativePaint(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if !watchRegisterPulse() {
		t.Fatal("caption class registration failed")
	}
	hwnd, _, err := watchUser.NewProc("CreateWindowExW").Call(0x080800A8,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLISelectionHeader"))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Test session"))),
		0x80000000, 100, 100, 400, 24, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer watchUser.NewProc("DestroyWindow").Call(hwnd)
	dc, _, _ := watchGDI.NewProc("CreateCompatibleDC").Call(0)
	defer watchGDI.NewProc("DeleteDC").Call(dc)
	bitmap, _, _ := watchGDI.NewProc("CreateBitmap").Call(400, 24, 1, 32, 0)
	defer watchGDI.NewProc("DeleteObject").Call(bitmap)
	old, _, _ := watchGDI.NewProc("SelectObject").Call(dc, bitmap)
	defer watchGDI.NewProc("SelectObject").Call(dc, old)
	watchUser.NewProc("SendMessageW").Call(hwnd, 0x318, dc, 4)
	colour, _, _ := watchGDI.NewProc("GetPixel").Call(dc, 390, 12)
	if colour != 0x2828C6 {
		t.Fatalf("caption background = %#x", colour)
	}
	for _, wanted := range []uintptr{0xFFB754, 0x47C2FF, 0x7373ED, 0xFFFFFF} {
		watchUser.NewProc("SetPropW").Call(hwnd, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIPulseColour"))), wanted+1)
		watchUser.NewProc("SendMessageW").Call(hwnd, 0x318, dc, 4)
		actual, _, _ := watchGDI.NewProc("GetPixel").Call(dc, 390, 12)
		if actual != wanted {
			t.Fatalf("painted colour %#x, want %#x", actual, wanted)
		}
	}
	actual, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	if actual != foreground {
		t.Fatal("caption painting stole focus")
	}
	style, _, _ := watchUser.NewProc("GetWindowLongW").Call(hwnd, ^uintptr(19))
	if style&0x080800A8 != 0x080800A8 {
		t.Fatalf("unsafe overlay style %#x", style)
	}
}

func TestWatchPulseBand(t *testing.T) {
	window := watchRect{-1920, -200, -20, 900}
	caption := watchRect{-1890, -192, -28, -169}
	buttons := watchRect{1740, 0, 1892, 30}
	got, ok := watchPulseBand(window, caption, buttons)
	if !ok || got != (watchRect{-1890, -192, -180, -169}) {
		t.Fatalf("caption must leave icon and buttons uncovered: %+v %v", got, ok)
	}
	for _, invalid := range []watchRect{{}, {-1, 0, 100, 30}, {0, 0, 1901, 30}} {
		if _, ok := watchPulseBand(window, caption, invalid); ok {
			t.Fatal("accepted unsafe button bounds", invalid)
		}
	}
	if _, ok := watchPulseBand(window, watchRect{-2000, -192, -28, -169}, buttons); ok {
		t.Fatal("caption outside window accepted")
	}
}

func TestWatchCancelPulse(t *testing.T) {
	before := watchPulseGeneration.Load()
	watchCancelPulse()
	if watchPulseGeneration.Load() == before {
		t.Fatal("pulse not cancelled")
	}
}
