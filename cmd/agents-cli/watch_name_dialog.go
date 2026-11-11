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
	window, _, _ = watchUser.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Suggested session name"))), 0x00c80000, 200, 200, 520, 210, 0, 0, instance, 0)
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
	create("STATIC", "Session "+strconv.FormatUint(uint64(review.Sample.PID), 10)+": accept changes its window or tab title.", 0, 14, 12, 480, 20, 0)
	edit = create("EDIT", review.Name, 0x00810080, 14, 40, 480, 26, 10)
	watchUser.NewProc("SendMessageW").Call(edit, 0xc5, 160, 0) // EM_SETLIMITTEXT
	status = create("STATIC", "Edit the suggestion, accept it, or dismiss it for this session.", 0, 14, 74, 480, 40, 0)
	create("BUTTON", "Accept", 0x10001, 14, 120, 100, 28, 1)
	create("BUTTON", "Dismiss", 0x10000, 124, 120, 100, 28, 3)
	create("BUTTON", "Cancel", 0x10000, 234, 120, 100, 28, 2)
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
