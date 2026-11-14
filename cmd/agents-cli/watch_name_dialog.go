package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A separate, explicitly opened window keeps editing and native title work off
// the watcher thread. Nothing is shown until the owner clicks "name?".
func watchNameDialogPosition(anchor, area watchRect, width, height int32) watchPoint {
	p := watchPoint{anchor.Right + 8, anchor.Top}
	if p.X+width > area.Right {
		p.X = anchor.Left - width - 8
	}
	return watchPanelPosition(area, width, height, &p)
}

func watchNameDialogOrigin(width, height int32) watchPoint {
	panel, _, _ := watchUser.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIWatchPanel"))), 0)
	var anchor watchRect
	if panel != 0 {
		if ok, _, _ := watchUser.NewProc("GetWindowRect").Call(panel, uintptr(unsafe.Pointer(&anchor))); ok != 0 {
			monitor, _, _ := watchUser.NewProc("MonitorFromWindow").Call(panel, 2) // nearest monitor
			var info struct {
				Size         uint32
				Bounds, Work watchRect
				Flags        uint32
			}
			info.Size = uint32(unsafe.Sizeof(info))
			if ok, _, _ := watchUser.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
				return watchNameDialogPosition(anchor, info.Work, width, height)
			}
		}
	}
	return watchPanelPosition(selectWatchMonitor(watchMonitors(), "").Work, width, height, nil)
}

func watchNameDialog(review watchNameReview, show bool) watchNameReview {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var edit, status, window uintptr
	applying := false
	instance, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	setText := func(h uintptr, text string) {
		watchUser.NewProc("SetWindowTextW").Call(h, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}
	proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x10:
			if applying {
				return 0
			}
		case 0x111: // WM_COMMAND
			switch wp & 0xffff {
			case 1:
				if applying {
					return 0
				}
				var text [256]uint16
				watchUser.NewProc("GetWindowTextW").Call(edit, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
				review.Name = windows.UTF16ToString(text[:])
				if !watchValidName(review.Name) {
					setText(status, "Use 1–80 characters, without control characters.")
					return 0
				}
				setText(status, "Applying title…")
				applying = true
				go func(r watchNameReview) {
					r.Action = "accept" // Only the explicit Rename button authorizes application.
					exe, _ := os.Executable()
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					input, _ := json.Marshal(r)
					cmd := helperCommand(ctx, exe, string(input), "name-apply", strconv.FormatUint(uint64(r.Sample.PID), 10))
					cmd.WaitDelay = 100 * time.Millisecond
					out, err := cmd.Output()
					problem := ""
					if err != nil {
						problem = "Title helper failed or timed out."
					} else if err = helperErr(out); err != nil {
						problem = err.Error()
					}
					// The dialog waits for this result before allowing another apply.
					nameDialogResults <- problem
					watchUser.NewProc("PostMessageW").Call(hwnd, 0x8001, 0, 0)
				}(review)
				watchUser.NewProc("EnableWindow").Call(edit, 0)
				watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 1), 0)
				return 0
			case 2:
				if applying {
					return 0
				}
				watchUser.NewProc("DestroyWindow").Call(hwnd)
				return 0
			case 3:
				if applying {
					return 0
				}
				review.Action = "dismiss"
				watchUser.NewProc("DestroyWindow").Call(hwnd)
				return 0
			}
		case 0x8001:
			applying = false
			problem := <-nameDialogResults
			if problem == "" {
				review.Action = "accept"
				watchUser.NewProc("DestroyWindow").Call(hwnd)
			} else {
				setText(status, problem)
				watchUser.NewProc("EnableWindow").Call(edit, 1)
				watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 1), 1)
			}
			return 0
		case 2:
			watchUser.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		r, _, _ := watchUser.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
		return r
	})
	className := windows.StringToUTF16Ptr("AgentsCLINameReview")
	cursor, _, _ := watchUser.NewProc("LoadCursorW").Call(0, 32512)
	class := watchClass{Proc: proc, Instance: instance, Cursor: cursor, Background: 16, Name: className}
	class.Size = uint32(unsafe.Sizeof(class))
	watchUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	defer watchUser.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
	position := watchNameDialogOrigin(520, 270)
	window, _, _ = watchUser.NewProc("CreateWindowExW").Call(0x8, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Confirm session rename"))), 0x00c80000, uintptr(position.X), uintptr(position.Y), 520, 270, 0, 0, instance, 0)
	if window == 0 {
		review.Error = "Could not open name review."
		return review
	}
	create := func(class, text string, style uintptr, x, y, w, h int32, id uintptr) uintptr {
		child, _, _ := watchUser.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(class))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), 0x50000000|style, uintptr(x), uintptr(y), uintptr(w), uintptr(h), window, id, instance, 0)
		font, _, _ := watchGDI.NewProc("GetStockObject").Call(17)
		watchUser.NewProc("SendMessageW").Call(child, 0x30, font, 1)
		return child
	}
	create("STATIC", "Current window/tab name:", 0, 14, 12, 480, 20, 0)
	create("EDIT", review.Sample.Title, 0x00800880, 14, 34, 480, 26, 11) // Read-only; long titles can be scrolled.
	create("STATIC", "Proposed name:", 0, 14, 68, 480, 20, 0)
	edit = create("EDIT", review.Name, 0x00810080, 14, 90, 480, 26, 10)
	watchUser.NewProc("SendMessageW").Call(edit, 0xc5, 160, 0) // EM_SETLIMITTEXT
	status = create("STATIC", "Only Rename changes the window/tab title. Cancel keeps its current name.", 0, 14, 124, 480, 40, 0)
	create("BUTTON", "Rename", 0x10000, 14, 176, 100, 28, 1)
	create("BUTTON", "Dismiss", 0x10000, 124, 176, 100, 28, 3)
	create("BUTTON", "Cancel", 0x10001, 234, 176, 100, 28, 2)
	if show {
		// Detached helpers start hidden. SetWindowPos avoids the first
		// ShowWindow call being overridden by STARTUPINFO's SW_HIDE.
		watchUser.NewProc("SetWindowPos").Call(window, 0, 0, 0, 0, 0, 0x1|0x2|0x4|0x40)
	}
	var msg watchMessage
	for {
		ok, _, _ := watchUser.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ok) <= 0 {
			break
		}
		handled, _, _ := watchUser.NewProc("IsDialogMessageW").Call(window, uintptr(unsafe.Pointer(&msg)))
		if handled == 0 {
			watchUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			watchUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
	return review
}

var nameDialogResults = make(chan string, 1)

func watchDialogItem(window, id uintptr) uintptr {
	h, _, _ := watchUser.NewProc("GetDlgItem").Call(window, id)
	return h
}
