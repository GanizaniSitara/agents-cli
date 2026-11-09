package main

import (
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Four narrow overlays leave the target's contents and placement untouched.
func watchPulseEdges(r watchRect) [4]watchRect {
	const w = 6
	return [4]watchRect{
		{r.Left, r.Top, r.Right, r.Top + w},
		{r.Left, r.Bottom - w, r.Right, r.Bottom},
		{r.Left, r.Top + w, r.Left + w, r.Bottom - w},
		{r.Right - w, r.Top + w, r.Right, r.Bottom - w},
	}
}

func watchPulseWindow(target uintptr) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Allow an asynchronously restored window to reach its saved bounds.
		time.Sleep(100 * time.Millisecond)
		var overlays []uintptr
		defer func() {
			for _, h := range overlays {
				watchUser.NewProc("DestroyWindow").Call(h)
			}
		}()
		for pulse := 0; pulse < 3; pulse++ {
			var r watchRect
			ok, _, _ := watchUser.NewProc("GetWindowRect").Call(target, uintptr(unsafe.Pointer(&r)))
			if ok == 0 || r.Right-r.Left < 12 || r.Bottom-r.Top < 12 {
				return
			}
			for i, edge := range watchPulseEdges(r) {
				if pulse == 0 {
					// STATIC SS_WHITERECT; transparent layered tool windows never
					// activate or intercept mouse input, including across monitors.
					h, _, _ := watchUser.NewProc("CreateWindowExW").Call(0x080800A8,
						uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), 0,
						0x80000006, 0, 0, 0, 0, 0, 0, 0, 0)
					if h == 0 {
						return
					}
					overlays = append(overlays, h)
					watchUser.NewProc("SetLayeredWindowAttributes").Call(h, 0, 230, 2)
				}
				h := overlays[i]
				watchUser.NewProc("SetWindowPos").Call(h, ^uintptr(0), uintptr(edge.Left), uintptr(edge.Top), uintptr(edge.Right-edge.Left), uintptr(edge.Bottom-edge.Top), 0x10|0x40)
				watchUser.NewProc("UpdateWindow").Call(h)
			}
			time.Sleep(165 * time.Millisecond)
			for _, h := range overlays {
				watchUser.NewProc("ShowWindow").Call(h, 0)
			}
			time.Sleep(165 * time.Millisecond)
		}
	}()
}
