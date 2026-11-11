package main

import (
	"testing"
	"time"
)

func TestWatchBlockedWindowsSignalDoesNotHoldTracker(t *testing.T) {
	for _, blocked := range []string{"flash", "taskbar"} {
		t.Run(blocked, func(t *testing.T) {
			a := &watchApp{tracker: newWatchTracker(0)}
			s := watchSample{Session: Session{PID: 1, State: "prompt"}, HWND: 10, DesktopID: "here", Phase: "waiting"}
			a.tracker.observe(s, time.Now())
			entered, release, finished := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
			defer func() {
				close(release)
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("blocked signal did not finish after release")
				}
			}()
			wait := func() { entered <- struct{}{}; <-release }
			go func() {
				defer close(finished)
				a.panelSignals("here", &watchTaskbarController{}, func(uintptr, bool) {
					if blocked == "flash" {
						wait()
					}
				}, func(uintptr, uint32) bool {
					if blocked == "taskbar" {
						wait()
					}
					return true
				})
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("Windows sink not reached")
			}
			progress := make(chan struct{})
			go func() {
				a.mu.Lock()
				s.State, s.Phase = "busy", "working"
				a.tracker.observe(s, time.Now())
				a.mu.Unlock()
				close(progress)
			}()
			select {
			case <-progress:
			case <-time.After(time.Second):
				t.Fatal("native call blocked status update")
			}
			// The sink remains blocked while the tracker has already advanced.
			select {
			case <-finished:
				t.Fatal("sink did not stay blocked")
			default:
			}
		})
	}
}

func TestWatchSharedTerminalSignalsAreAggregatedOnce(t *testing.T) {
	entries := map[uint32]*watchEntry{
		1: {Sample: watchSample{HWND: 10, DesktopID: "here", Phase: "waiting"}, Stopped: true, Attention: true},
		2: {Sample: watchSample{HWND: 10, DesktopID: "here", Phase: "working"}},
		3: {Sample: watchSample{HWND: 10, DesktopID: "here", Phase: "error"}, Stopped: true},
	}
	flashes, taskbar := watchFlashController{}, watchTaskbarController{}
	flashCalls, setCalls := 0, 0
	flash := func(_ uintptr, on bool) {
		flashCalls++
		if !on {
			t.Fatal("working tab cancelled another tab's input flash")
		}
	}
	set := func(_ uintptr, state uint32) bool {
		setCalls++
		if state != 4 {
			t.Fatal("error priority lost", state)
		}
		return true
	}
	for i := 0; i < 20; i++ {
		flashes.update(entries, "here", flash)
		taskbar.update(entries, "here", set)
	}
	if flashCalls != 1 || setCalls != 1 {
		t.Fatalf("shared window oscillated: flash=%d fill=%d", flashCalls, setCalls)
	}
	delete(entries, 3)
	if watchTaskbarStates(entries, "here")[10] != 8 {
		t.Fatal("remaining input tab did not retain amber fill")
	}
	entries[1].Parked = true
	if flashes.states(entries, "here")[10] || watchTaskbarStates(entries, "here")[10] != 0 {
		t.Fatal("parked input tab retained attention")
	}
}
