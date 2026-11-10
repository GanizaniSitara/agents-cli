package main

import (
	"testing"
	"time"
)

func TestWatchPulseMatchesRow(t *testing.T) {
	theme := defaultWatchTheme()
	for _, phase := range []string{"idle", "unknown", "waiting", "question", "review", "error", "finished", "working", "active"} {
		row := watchEntry{Sample: watchSample{Phase: phase}}
		if got := watchPulseColour(row, theme, false); got != theme.rowColour(phase) {
			t.Fatalf("%s: %#x", phase, got)
		}
		if got := watchPulseColour(row, theme, true); got != 0xFFFFFF {
			t.Fatalf("locator %s: %#x", phase, got)
		}
	}
	theme.Idle = "#012345"
	if got := watchPulseColour(watchEntry{Sample: watchSample{Phase: "idle"}}, theme, false); got != 0x452301 {
		t.Fatalf("custom palette ignored: %#x", got)
	}
}

func TestWatchDoubleSelectionIdentityAndPosition(t *testing.T) {
	previous := watchPanelPress{Time: time.Now(), Screen: watchPoint{50, 80}, Row: watchEntry{Sample: watchSample{Session: Session{PID: 1}, HWND: 11, RootStarted: "original"}}}
	current := previous
	current.Time = previous.Time.Add(200 * time.Millisecond)
	check := func(want bool) {
		t.Helper()
		if got := watchDoubleSelection(previous, current, 500*time.Millisecond, 4, 4); got != want {
			t.Fatalf("double click = %v, want %v", got, want)
		}
	}
	check(true)
	current.Screen.X += 10
	check(false)
	current = previous
	current.Time = previous.Time.Add(time.Second)
	check(false)
	current.Time = previous.Time.Add(200 * time.Millisecond)
	current.Row.Sample.RootStarted = "reused"
	check(false)
	previous.Time = time.Time{}
	check(false)
}
