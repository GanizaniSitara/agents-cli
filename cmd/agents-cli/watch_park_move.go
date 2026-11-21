package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// The helper never switches desktops. Keep Explorer calls outside the panel
// process and bound the time a row action can spend waiting for the shell.
func watchMoveParked(s watchSample, target string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "_park-move", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("Desktop move timed out")
	}
	if err != nil {
		return fmt.Errorf("Desktop move: %s", firstNonEmpty(strings.TrimSpace(string(out)), err.Error()))
	}
	return nil
}
func watchParkMoveHelper(target string) error {
	var s watchSample
	if err := json.NewDecoder(os.Stdin).Decode(&s); err != nil {
		return err
	}
	guid, err := windows.GUIDFromString(target)
	if err != nil {
		return err
	}
	if problem := watchTargetProblem(s, watchReadTarget(s)); problem != "" {
		return errors.New(problem)
	}
	if watchWindowClass(s.HWND) != "ConsoleWindowClass" {
		return errors.New("Requires a standalone console window")
	}
	m, release := openWatchDesktopManager()
	defer release()
	if m == nil {
		return errors.New("Desktop manager unavailable")
	}
	origin, _ := windowWatchDesktop(m, s.HWND, nil)
	if origin == target {
		return nil
	}
	if origin == "" || origin != s.DesktopID {
		return errors.New("Window desktop changed")
	}
	if err = shelfMoveConsole(s.HWND, guid); err != nil {
		return err
	}
	actual, _ := windowWatchDesktop(m, s.HWND, nil)
	if actual != target {
		return errors.New("Window move unconfirmed")
	}
	return nil
}
func (a *watchApp) parkDesktop(e *watchEntry) error {
	ds := a.desktopList()
	if len(ds) < 2 {
		return errors.New("Desktop 2 unavailable")
	}
	if e.Sample.DesktopID == "" {
		return errors.New("Session desktop unknown")
	}
	if err := watchMoveParked(e.Sample, ds[1].ID); err != nil {
		return err
	}
	e.ParkOrigin = e.Sample.DesktopID
	e.Sample.DesktopID, e.Sample.Desktop = ds[1].ID, ds[1].Name
	a.tracker.togglePark(e.Sample)
	return nil
}
func (a *watchApp) toggleRowPark(s watchSample) error {
	e := a.tracker.entries[s.PID]
	if e == nil || !watchSameClickSession(s, e.Sample) {
		return errors.New("Session changed")
	}
	if e.Pinned {
		return errors.New("Unpin before parking")
	}
	if e.Parked && e.ParkOrigin != "" {
		if err := watchMoveParked(e.Sample, e.ParkOrigin); err != nil {
			return err
		}
		e.Sample.DesktopID = e.ParkOrigin
		for _, d := range a.desktopList() {
			if d.ID == e.ParkOrigin {
				e.Sample.Desktop = d.Name
			}
		}
		e.ParkOrigin = ""
	}
	if !a.tracker.togglePark(s) {
		return errors.New("Cannot park session")
	}
	return nil
}
