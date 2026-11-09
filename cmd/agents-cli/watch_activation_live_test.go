package main

import (
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Opt in: this briefly displays a disposable window to exercise the real
// click handler. Ordinary tests must never activate desktop windows.
func TestWatchActivationLivePreservesPlacement(t *testing.T) {
	if os.Getenv("AGENTS_CLI_LIVE_TEST") != "1" {
		t.Skip("set AGENTS_CLI_LIVE_TEST=1 for the visible activation check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
	defer watchUser.NewProc("SetForegroundWindow").Call(previous)
	hwnd, _, err := watchUser.NewProc("CreateWindowExW").Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("agents-cli disposable placement test"))),
		0x00CF0000, 100, 100, 640, 420, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatal(err)
	}
	defer watchUser.NewProc("DestroyWindow").Call(hwnd)
	pid := uint32(os.Getpid())
	started := startTime(pid).Format(time.RFC3339Nano)
	sample := watchSample{Session: Session{PID: pid}, HWND: hwnd,
		RootStarted: started, WindowPID: pid, WindowStarted: started}
	entry := &watchEntry{Sample: sample}
	app := &watchApp{dir: t.TempDir(), tracker: newWatchTracker(time.Second)}
	app.tracker.entries[pid] = entry
	for _, mode := range []struct {
		name string
		show uintptr
	}{{"normal", 1}, {"maximized", 3}} {
		// Keep the handler on the thread that owns the window; t.Run would
		// block that owner while a different thread tries synchronous focus.
		watchUser.NewProc("ShowWindow").Call(hwnd, mode.show)
		var before, after watchRect
		watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&before)))
		zoomBefore, _, _ := watchUser.NewProc("IsZoomed").Call(hwnd)
		app.activateWatchRow(watchPanelPress{Row: *entry, Time: time.Now()}, 0)
		time.Sleep(1200 * time.Millisecond) // includes all three pulses
		watchUser.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&after)))
		zoomAfter, _, _ := watchUser.NewProc("IsZoomed").Call(hwnd)
		if before != after || zoomBefore != zoomAfter {
			t.Fatalf("placement changed: %+v / %d -> %+v / %d", before, zoomBefore, after, zoomAfter)
		}
		t.Logf("%s bounds unchanged: %+v; maximized=%v", mode.name, after, zoomAfter != 0)
	}
}
