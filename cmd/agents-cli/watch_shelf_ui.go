package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func watchShelfOwner() uintptr {
	h, _, _ := watchUser.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("AgentsCLIWatchPanel"))), 0)
	return h
}

func watchShelfMessage(text string, confirm bool) bool {
	flags := uintptr(0x40000 | 0x10000)
	if confirm {
		flags |= 0x4 | 0x100
	} else {
		flags |= 0x40
	}
	r, _, _ := watchUser.NewProc("MessageBoxW").Call(watchShelfOwner(), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Session Shelf"))), flags)
	return r == 6
}

func watchShelfReview(dir string, s watchSample) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := shelfSource(s); err != nil {
		watchShelfMessage(err.Error(), false)
		return err
	}
	if s.Tool != "claude" && s.Tool != "codex" {
		watchShelfMessage(fmt.Sprintf("Verified shelving is not yet available for %s. This session will stay open.", s.Tool), false)
		return nil
	}
	p, err := peek(s.PID, 0)
	if err != nil {
		return err
	}
	name := firstNonEmpty(watchShortName(watchRowLabel(s)), s.Tool)
	if !shelfIdle(s, p) {
		if !watchShelfMessage(name+"\n\nShelve when idle?\nThe window will close after its resume details are saved. Cancel this request from Shelf or the row menu.", true) {
			return nil
		}
		err = watchShelfAction(dir, "queue", "", shelfReviewJSON(s))
	} else {
		if !watchShelfMessage(name+"\n\nSave to Shelf and close this session?\nYou can resume it later with the same agent and account.", true) {
			return nil
		}
		err = watchShelfAction(dir, "save", "", shelfReviewJSON(s))
	}
	if err != nil {
		watchShelfMessage(err.Error(), false)
	}
	return err
}

// A confirmation may wait for its owner indefinitely; the subsequent disk,
// console and launch work runs in a separate helper with a hard deadline.
func watchShelfAction(dir, action, id, input string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"shelf", action}
	if id != "" {
		args = append(args, id)
	}
	args = append(args, "--state-dir", dir)
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	cmd.WaitDelay = 100 * time.Millisecond
	b, err := cmd.Output()
	if err == nil {
		return nil
	}
	var failure struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &failure) == nil && failure.Error.Message != "" {
		return fmt.Errorf("%s", failure.Error.Message)
	}
	return fmt.Errorf("Shelf action failed or timed out. Inspect Shelf and the session before retrying")
}

func watchShelfHelper(ctx context.Context, dir, action, input string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, "shelf", action, "--state-dir", dir)
	cmd.Stdin = strings.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	cmd.WaitDelay = 100 * time.Millisecond
	if err = cmd.Start(); err != nil {
		return err
	}
	watchUser.NewProc("AllowSetForegroundWindow").Call(uintptr(cmd.Process.Pid))
	return cmd.Wait()
}

func watchShelfVisible(entries []shelfEntry, filter string) []shelfEntry {
	var rows []shelfEntry
	filter = strings.ToLower(strings.TrimSpace(filter))
	for _, e := range entries {
		if e.State == "completed" || e.State == "cancelled" {
			continue
		}
		if filter == "" || strings.Contains(strings.ToLower(e.Name+" "+e.Tool+" "+e.Directory), filter) {
			rows = append(rows, e)
		}
	}
	return rows
}

func watchShelfWindow(dir string) error {
	return watchShelfDialog(dir, true)
}

func watchShelfDialog(dir string, show bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := k32.NewProc("GetModuleHandleW").Call(0)
	var window, edit, list, status uintptr
	var rows []shelfEntry
	var lastShelfTime time.Time
	var lastShelfSize int64
	busy := false
	result := make(chan error, 1)
	setText := func(h uintptr, text string) {
		watchUser.NewProc("SetWindowTextW").Call(h, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}
	refresh := func() {
		selectedID := ""
		if list != 0 {
			index, _, _ := watchUser.NewProc("SendMessageW").Call(list, 0x188, 0, 0)
			if index < uintptr(len(rows)) {
				selectedID = rows[index].ID
			}
		}
		var filter [256]uint16
		watchUser.NewProc("GetWindowTextW").Call(edit, uintptr(unsafe.Pointer(&filter[0])), 256)
		entries, err := shelfRead(dir)
		if err != nil {
			setText(status, err.Error())
			return
		}
		if info, err := os.Stat(shelfPath(dir)); err == nil {
			lastShelfTime = info.ModTime()
			lastShelfSize = info.Size()
		}
		rows = watchShelfVisible(entries, windows.UTF16ToString(filter[:]))
		watchUser.NewProc("SendMessageW").Call(list, 0x184, 0, 0) // LB_RESETCONTENT
		for _, e := range rows {
			text := fmt.Sprintf("%s  |  %s  |  %s  |  %s", e.Name, e.Tool, e.State, e.Saved.Local().Format("02 Jan 15:04"))
			watchUser.NewProc("SendMessageW").Call(list, 0x180, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
		}
		if len(rows) > 0 {
			selected := 0
			for i, e := range rows {
				if e.ID == selectedID {
					selected = i
					break
				}
			}
			watchUser.NewProc("SendMessageW").Call(list, 0x186, uintptr(selected), 0)
		}
	}
	proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x113:
			if !busy {
				if info, err := os.Stat(shelfPath(dir)); err == nil && (info.ModTime() != lastShelfTime || info.Size() != lastShelfSize) {
					refresh()
				}
			}
			return 0
		case 0x400:
			return 0x534b0001
		case 0x10:
			if busy {
				return 0
			}
		case 0x111:
			id := wp & 0xffff
			if id == 11 && wp>>16 == 1 {
				index, _, _ := watchUser.NewProc("SendMessageW").Call(list, 0x188, 0, 0)
				if index < uintptr(len(rows)) {
					e := rows[index]
					detail := e.Directory
					if detail == "" {
						detail = "Waiting to save resume details"
					}
					if e.Problem != "" {
						detail = e.Problem + " | " + filepath.Base(e.Directory)
					}
					setText(status, detail)
				}
				return 0
			}
			if id == 10 && wp>>16 == 0x300 && !busy {
				refresh()
				return 0
			} // EN_CHANGE
			if id == 2 {
				if !busy {
					watchUser.NewProc("DestroyWindow").Call(hwnd)
				}
				return 0
			}
			if (id == 1 || id == 3) && !busy {
				index, _, _ := watchUser.NewProc("SendMessageW").Call(list, 0x188, 0, 0)
				if index >= uintptr(len(rows)) {
					return 0
				}
				entry := rows[index]
				busy = true
				setText(status, "Working…")
				go func() {
					var err error
					if id == 3 {
						err = watchShelfAction(dir, "cancel", entry.ID, "")
					} else {
						err = watchShelfAction(dir, "resume", entry.ID, "")
					}
					result <- err
					watchUser.NewProc("PostMessageW").Call(hwnd, 0x8001, 0, 0)
				}()
				return 0
			}
		case 0x8001:
			busy = false
			err := <-result
			if err != nil {
				setText(status, err.Error())
			} else {
				setText(status, "Done")
			}
			refresh()
			return 0
		case 2:
			watchUser.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		r, _, _ := watchUser.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
		return r
	})
	className := windows.StringToUTF16Ptr("AgentsCLIShelf")
	class := watchClass{Proc: proc, Instance: instance, Background: 16, Name: className}
	class.Size = uint32(unsafe.Sizeof(class))
	watchUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	defer watchUser.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
	pos := watchNameDialogOrigin(520, 350)
	window, _, _ = watchUser.NewProc("CreateWindowExW").Call(8, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Shelf"))), 0x00c80000, uintptr(pos.X), uintptr(pos.Y), 520, 350, watchShelfOwner(), 0, instance, 0)
	if window == 0 {
		return fmt.Errorf("Could not open Shelf")
	}
	create := func(class, text string, style uintptr, x, y, width, height int32, id uintptr) uintptr {
		h, _, _ := watchUser.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(class))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), 0x50000000|style, uintptr(x), uintptr(y), uintptr(width), uintptr(height), window, id, instance, 0)
		font, _, _ := watchGDI.NewProc("GetStockObject").Call(17)
		watchUser.NewProc("SendMessageW").Call(h, 0x30, font, 1)
		return h
	}
	create("STATIC", "Search name, agent or project:", 0, 12, 10, 480, 20, 0)
	edit = create("EDIT", "", 0x00810080, 12, 32, 480, 26, 10)
	list = create("LISTBOX", "", 0x00a10001, 12, 66, 480, 164, 11)
	status = create("STATIC", "Select a saved session to resume. Pending shelving can be cancelled.", 0, 12, 234, 480, 40, 0)
	create("BUTTON", "Resume", 0x10001, 172, 278, 100, 28, 1)
	create("BUTTON", "Cancel pending", 0x10000, 282, 278, 110, 28, 3)
	create("BUTTON", "Close", 0x10000, 402, 278, 90, 28, 2)
	refresh()
	watchUser.NewProc("SetTimer").Call(window, 1, 2000, 0)
	if show {
		watchUser.NewProc("SetWindowPos").Call(window, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x40)
		watchUser.NewProc("SetForegroundWindow").Call(window)
		watchUser.NewProc("SetFocus").Call(edit)
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
	return nil
}

func shelfReviewJSON(s watchSample) string { b, _ := json.Marshal(s); return string(b) }
