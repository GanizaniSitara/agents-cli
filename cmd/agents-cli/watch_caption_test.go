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
	watchConfigurePulse(hwnd, "Header", true)
	var alpha byte
	var key, flags uint32
	ok, _, _ := watchUser.NewProc("GetLayeredWindowAttributes").Call(hwnd, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags)))
	if ok == 0 || alpha != 100 || flags&2 == 0 {
		t.Fatalf("Terminal pulse is opaque: alpha=%d flags=%d", alpha, flags)
	}
	watchUser.NewProc("SendMessageW").Call(hwnd, 0x318, dc, 4)
	for y := uintptr(2); y < 22; y++ {
		for x := uintptr(6); x < 150; x++ {
			pixel, _, _ := watchGDI.NewProc("GetPixel").Call(dc, x, y)
			if pixel != 0xFFFFFF {
				t.Fatalf("Terminal pulse drew copied caption text at %d,%d", x, y)
			}
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

func TestWatchTerminalPulseKeepsOriginalHeaderVisible(t *testing.T) {
	for _, style := range []string{"", "Header", "Whole window"} {
		alpha, noText := watchPulseAppearance(style, true)
		if alpha == 0 || alpha >= 255 || !noText {
			t.Fatalf("Terminal %q hides or replaces its header: alpha=%d noText=%v", style, alpha, noText)
		}
	}
	alpha, noText := watchPulseAppearance("Frame", true)
	if alpha != 255 || !noText {
		t.Fatal("frame should remain an opaque text-free border")
	}
	alpha, noText = watchPulseAppearance("Header", false)
	if alpha != 255 || noText {
		t.Fatal("classic console header changed")
	}
}

func TestWatchPulseStyleCycle(t *testing.T) {
	if watchPulseStyle("") != "Header" || watchPulseStyle("invalid") != "Header" {
		t.Fatal("existing settings must retain header pulses")
	}
	style := ""
	for _, want := range []string{"Frame", "Whole window", "Header"} {
		style = watchNextPulseStyle(style)
		if style != want {
			t.Fatalf("pulse style %q, want %q", style, want)
		}
	}
	dir := t.TempDir()
	settings := watchSettings{SelectionBlinkStyle: "Whole window", DisableForeground: true}
	if err := saveWatchSettings(dir, settings); err != nil {
		t.Fatal(err)
	}
	loaded := loadWatchSettings(dir)
	if loaded.SelectionBlinkStyle != "Whole window" || !loaded.DisableForeground {
		t.Fatal("pulse style did not persist independently of foreground choice")
	}
}
