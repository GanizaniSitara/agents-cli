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
// the watcher thread. Nothing is shown until the owner clicks "rename".
func watchNameDialogPosition(anchor, area watchRect, width, height int32) watchPoint {
	p := watchPoint{anchor.Left, anchor.Top}
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
	suggesting := false
	suggestions := make(chan watchNameReview, 1)
	var cancelSuggestion context.CancelFunc
	instance, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	setText := func(h uintptr, text string) {
		watchUser.NewProc("SetWindowTextW").Call(h, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}
	proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x400: // DM_GETDEFID: IsDialogMessage routes Enter to Rename.
			return 0x534b0001 // DC_HASDEFID | button 1
		case 0x10:
			if suggesting && cancelSuggestion != nil {
				cancelSuggestion()
				watchUser.NewProc("DestroyWindow").Call(hwnd)
				return 0
			}
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
				checked, _, _ := watchUser.NewProc("SendMessageW").Call(watchDialogItem(hwnd, 5), 0xf0, 0, 0) // BM_GETCHECK
				review.RenameSession = checked == 1
				if !watchValidName(review.Name) {
					setText(status, "Use 1–80 characters, without control characters.")
					return 0
				}
				setText(status, "Applying rename…")
				applying = true
				go func(r watchNameReview) {
					r.Action = "accept" // Only the explicit Rename button authorizes application.
					exe, _ := os.Executable()
					ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
					defer cancel()
					input, _ := json.Marshal(r)
					cmd := helperCommand(ctx, exe, string(input), "name-apply", strconv.FormatUint(uint64(r.Sample.PID), 10))
					cmd.WaitDelay = 100 * time.Millisecond
					out, err := cmd.Output()
					problem := ""
					if err != nil {
						problem = "Rename timed out or failed. Check the session before retrying."
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
				if suggesting && cancelSuggestion != nil {
					cancelSuggestion()
					watchUser.NewProc("DestroyWindow").Call(hwnd)
					return 0
				}
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
			case 4:
				if applying {
					return 0
				}
				applying, suggesting = true, true
				setText(status, "Asking this agent for a name (up to one minute). No title change.")
				watchUser.NewProc("EnableWindow").Call(edit, 0)
				watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 1), 0)
				watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 4), 0)
				ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
				cancelSuggestion = cancel
				go func(r watchNameReview) {
					defer cancel()
					r.Action = "suggest"
					input, _ := json.Marshal(r)
					exe, _ := os.Executable()
					cmd := helperCommand(ctx, exe, string(input), "name-suggest", strconv.FormatUint(uint64(r.Sample.PID), 10))
					cmd.WaitDelay = 250 * time.Millisecond
					out, err := cmd.Output()
					var result watchNameReview
					if err != nil || json.Unmarshal(out, &result) != nil {
						result.Error = "Naming request failed or timed out. Check the agent before retrying."
					}
					suggestions <- result
					watchUser.NewProc("PostMessageW").Call(hwnd, 0x8002, 0, 0)
				}(review)
				return 0
			}
		case 0x8002:
			result := <-suggestions
			applying, suggesting = false, false
			watchUser.NewProc("EnableWindow").Call(edit, 1)
			watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 1), 1)
			watchUser.NewProc("EnableWindow").Call(watchDialogItem(hwnd, 4), 1)
			if result.Error != "" {
				setText(status, result.Error)
			} else {
				setText(edit, result.Name)
				setText(status, "Agent suggestion ready. Edit it or click Rename to apply.")
				foreground, _, _ := watchUser.NewProc("GetForegroundWindow").Call()
				watchSelectNameEdit(edit, foreground == hwnd)
			}
			return 0
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
	position := watchNameDialogOrigin(520, 214)
	window, _, _ = watchUser.NewProc("CreateWindowExW").Call(0x8, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Confirm session rename"))), 0x00c80000, uintptr(position.X), uintptr(position.Y), 520, 214, 0, 0, instance, 0)
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
	create("STATIC", "Name:", 0, 14, 12, 480, 20, 0)
	edit = create("EDIT", review.Name, 0x00810080, 14, 34, 480, 26, 10)
	watchUser.NewProc("SendMessageW").Call(edit, 0xc5, 160, 0) // EM_SETLIMITTEXT
	watchSelectNameEdit(edit, false)
	// Child creation order is the keyboard tab order: name, Rename, other actions, checkbox.
	create("BUTTON", "Rename", 0x10001, 174, 140, 100, 28, 1)
	create("BUTTON", "Ask agent", 0x10000, 14, 140, 100, 28, 4)
	create("BUTTON", "Dismiss", 0x10000, 284, 140, 100, 28, 3)
	create("BUTTON", "Cancel", 0x10000, 394, 140, 100, 28, 2)
	if watchHarnessRenameSupported(review.Sample.Tool) {
		check := create("BUTTON", "Also rename the harness session (requires idle)", 0x10003, 14, 68, 480, 22, 5)
		if review.RenameSession {
			watchUser.NewProc("SendMessageW").Call(check, 0xf1, 1, 0) // BM_SETCHECK
		}
	} else {
		create("STATIC", "Window/tab title only for this harness.", 0, 14, 68, 480, 22, 0)
	}
	status = create("STATIC", "Ask agent requests a suggestion. Rename applies the changes selected above.", 0, 14, 94, 480, 40, 0)
	if !loadWatchSettings(review.Dir).SuggestSessionNames {
		watchUser.NewProc("EnableWindow").Call(watchDialogItem(window, 4), 0)
		setText(status, "Rename applies the selected changes. Enable naming suggestions in Settings to use Ask agent.")
	}
	if show {
		// Detached helpers start hidden. SetWindowPos avoids the first
		// ShowWindow call being overridden by STARTUPINFO's SW_HIDE.
		watchUser.NewProc("SetWindowPos").Call(window, 0, 0, 0, 0, 0, 0x1|0x2|0x4|0x40)
		watchUser.NewProc("SetForegroundWindow").Call(window)
		watchSelectNameEdit(edit, true)
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

func watchSelectNameEdit(edit uintptr, focus bool) {
	watchUser.NewProc("SendMessageW").Call(edit, 0xb1, 0, ^uintptr(0)) // EM_SETSEL: select all
	if focus {
		watchUser.NewProc("SetFocus").Call(edit)
	}
}

func watchDialogItem(window, id uintptr) uintptr {
	h, _, _ := watchUser.NewProc("GetDlgItem").Call(window, id)
	return h
}
