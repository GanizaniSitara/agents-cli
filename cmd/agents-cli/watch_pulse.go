package main

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var watchPulseSerial sync.Mutex
var watchPulseGeneration atomic.Uint64
var watchPulseOnce sync.Once
var watchPulseRegistered bool

func watchCancelPulse() { watchPulseGeneration.Add(1) }

func watchPulseColour(row watchEntry, theme watchTheme, locator bool) uintptr {
	if locator {
		return 0xFFFFFF
	}
	if row.Parked {
		return theme.rowColour("parked")
	}
	phase := row.Sample.Phase
	if phase == "" {
		switch row.Sample.State {
		case "busy":
			phase = "working"
		case "prompt":
			phase = "waiting"
		case "error":
			phase = "error"
		default:
			phase = "idle"
		}
	}
	return theme.rowColour(phase)
}

func watchPulseWindow(sample watchSample, colour uintptr) {
	target := sample.HWND
	generation := watchPulseGeneration.Add(1)
	go func() {
		watchPulseSerial.Lock()
		defer watchPulseSerial.Unlock()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Allow an asynchronously restored window to reach its saved bounds.
		time.Sleep(100 * time.Millisecond)
		if watchPulseGeneration.Load() != generation || !watchLiftIdentity(sample) || !watchRegisterPulse() {
			return
		}
		var overlay uintptr
		defer func() {
			if overlay != 0 {
				watchUser.NewProc("DestroyWindow").Call(overlay)
			}
		}()
		for pulse := 0; pulse < 3; pulse++ {
			foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
			r, ok := watchPulseCaption(target)
			if !ok || foreground != target || watchPulseGeneration.Load() != generation || !watchLiftIdentity(sample) {
				return
			}
			if pulse == 0 {
				// Opaque, click-through band; the original window stays untouched.
				overlay, _, _ = watchUser.NewProc("CreateWindowExW").Call(0x080800A8,
					uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLISelectionHeader"))),
					uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(watchWindowTitle(target)))),
					0x80000000, 0, 0, 0, 0, 0, 0, 0, 0)
				if overlay == 0 {
					return
				}
				if ok, _, _ := watchUser.NewProc("SetPropW").Call(overlay, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIPulseColour"))), colour+1); ok == 0 {
					return
				}
				watchUser.NewProc("SetLayeredWindowAttributes").Call(overlay, 0, 255, 2)
			}
			watchUser.NewProc("SetWindowPos").Call(overlay, ^uintptr(0), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x10|0x40)
			watchUser.NewProc("UpdateWindow").Call(overlay)
			for tick := 0; tick < 14; tick++ {
				foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
				bounds, valid := watchPulseCaption(target)
				if foreground != target || watchPulseGeneration.Load() != generation || !valid || bounds != r {
					return
				}
				var msg watchMessage
				for {
					pending, _, _ := watchUser.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
					if pending == 0 {
						break
					}
					watchUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
					watchUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
				}
				time.Sleep(25 * time.Millisecond)
			}
			watchUser.NewProc("ShowWindow").Call(overlay, 0)
			time.Sleep(200 * time.Millisecond)
		}
	}()
}

func watchPulseBand(window, caption, buttons watchRect) (watchRect, bool) {
	if buttons.Left < 0 || buttons.Right > window.Right-window.Left || buttons.Right <= buttons.Left || buttons.Bottom <= buttons.Top {
		return watchRect{}, false
	}
	caption.Right = min(caption.Right, window.Left+buttons.Left)
	if caption.Left < window.Left || caption.Top < window.Top || caption.Bottom > window.Bottom || caption.Right-caption.Left < 24 || caption.Bottom-caption.Top < 8 {
		return watchRect{}, false
	}
	return caption, true
}

func watchPulseCaption(target uintptr) (watchRect, bool) {
	var title struct {
		Size  uint32
		Rect  watchRect
		State [6]uint32
	}
	title.Size = uint32(unsafe.Sizeof(title))
	var window, buttons watchRect
	var cloaked uint32
	watchDWM.NewProc("DwmGetWindowAttribute").Call(target, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
	visible, _, _ := watchUser.NewProc("IsWindowVisible").Call(target)
	iconic, _, _ := watchUser.NewProc("IsIconic").Call(target)
	a, _, _ := watchUser.NewProc("GetTitleBarInfo").Call(target, uintptr(unsafe.Pointer(&title)))
	b, _, _ := watchUser.NewProc("GetWindowRect").Call(target, uintptr(unsafe.Pointer(&window)))
	hr, _, _ := watchDWM.NewProc("DwmGetWindowAttribute").Call(target, 5, uintptr(unsafe.Pointer(&buttons)), unsafe.Sizeof(buttons))
	if a == 0 || b == 0 || int32(hr) < 0 || cloaked != 0 || visible == 0 || iconic != 0 || title.State[0]&(0x8000|0x10000) != 0 {
		return watchRect{}, false
	}
	return watchPulseBand(window, title.Rect, buttons)
}

func watchRegisterPulse() bool {
	watchPulseOnce.Do(func() {
		proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
			if msg == 0x84 {
				return ^uintptr(0)
			}
			if msg == 0x21 {
				return 3
			}
			if msg == 0x14 {
				return 1
			}
			if msg == 0xF || msg == 0x318 {
				var paint watchPaint
				dc := wp
				if msg == 0xF {
					dc, _, _ = watchUser.NewProc("BeginPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
					defer watchUser.NewProc("EndPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
				}
				var rect watchRect
				watchUser.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
				colour, _, _ := watchUser.NewProc("GetPropW").Call(hwnd, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIPulseColour"))))
				if colour == 0 {
					colour = 0x2828C6
				} else {
					colour--
				}
				watchFill(dc, rect, colour)
				font, _, _ := watchGDI.NewProc("GetStockObject").Call(17)
				old, _, _ := watchGDI.NewProc("SelectObject").Call(dc, font)
				defer watchGDI.NewProc("SelectObject").Call(dc, old)
				watchGDI.NewProc("SetBkMode").Call(dc, 1)
				textColour := uintptr(0xFFFFFF)
				if (299*(colour&255)+587*((colour>>8)&255)+114*((colour>>16)&255))/1000 >= 150 {
					textColour = 0x191919
				}
				watchGDI.NewProc("SetTextColor").Call(dc, textColour)
				watchText(dc, watchWindowTitle(hwnd), 6, 2, rect.Right-6, rect.Bottom)
				return 0
			}
			r, _, _ := watchUser.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
			return r
		})
		instance, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
		class := watchClass{Proc: proc, Instance: instance, Name: windows.StringToUTF16Ptr("AgentsCLISelectionHeader")}
		class.Size = uint32(unsafe.Sizeof(class))
		r, _, _ := watchUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
		watchPulseRegistered = r != 0
	})
	return watchPulseRegistered
}
