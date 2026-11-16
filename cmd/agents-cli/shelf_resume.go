package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func shelfResumeAndPresent(dir, id string) error {
	if len(watchDesktops()) < 2 {
		return errors.New("Desktop 2 is unavailable; no session was started")
	}
	if err := shelfResume(dir, id); err != nil {
		return err
	}
	entries, err := shelfRead(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		b, _ := json.Marshal(e)
		out, err := runHelperInput(string(b), "shelf-present", strconv.FormatUint(uint64(e.ResumedPID), 10))
		if err == nil && len(out) > 0 {
			err = helperErr(out)
		}
		return err
	}
	return errors.New("Resume entry disappeared")
}

// Attach and show only the new console. Move it before showing it, without
// activating it or switching the owner's desktop.
func shelfPresent(e shelfEntry) error {
	if !shelfAlive(e.ResumedPID, e.ResumedStart) {
		return errors.New("Resumed process exited; inspect Shelf before retrying")
	}
	until := time.Now().Add(5 * time.Second)
	for {
		if err := attach(e.ResumedPID); err == nil {
			break
		}
		if time.Now().After(until) {
			return errors.New("Resumed process has no console yet; entry retained")
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer pFreeConsole.Call()
	hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
	if hwnd == 0 {
		return errors.New("Resumed console is unavailable")
	}
	desktops := watchDesktops()
	if len(desktops) < 2 {
		return errors.New("Desktop 2 is unavailable; resumed console remains hidden")
	}
	manager, release := openWatchDesktopManager()
	defer release()
	if manager == nil {
		return errors.New("Desktop manager unavailable; resumed console remains hidden")
	}
	id, err := windows.GUIDFromString(desktops[1].ID)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(manager.VTable[5], uintptr(unsafe.Pointer(manager)), hwnd, uintptr(unsafe.Pointer(&id)))
	if int32(hr) < 0 {
		// A never-shown console has no shell application view. Register it
		// minimized without activation, then move it before displaying its body.
		watchUser.NewProc("ShowWindowAsync").Call(hwnd, 7) // SW_SHOWMINNOACTIVE
		var moveErr error
		until := time.Now().Add(2 * time.Second)
		for {
			moveErr = shelfMoveConsole(hwnd, id)
			if moveErr == nil || time.Now().After(until) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if moveErr != nil {
			return errors.New(moveErr.Error() + "; resumed session remains minimized")
		}
	}
	if actual, _ := windowWatchDesktop(manager, hwnd, desktops); actual != desktops[1].ID {
		return errors.New("Resumed console desktop was not confirmed")
	}
	if watchValidName(e.Name) {
		k32.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(e.Name))))
	}
	watchUser.NewProc("ShowWindowAsync").Call(hwnd, 4) // SW_SHOWNOACTIVATE
	return nil
}
