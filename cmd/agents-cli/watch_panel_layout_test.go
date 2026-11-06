package main

import "testing"

func TestWatchPanelCurrentDesktopAndSummary(t *testing.T) {
	entries := []watchEntry{
		{Sample: watchSample{Session: Session{PID: 1, State: "idle"}, DesktopID: "one"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 2, State: "busy"}, DesktopID: "one"}},
		{Sample: watchSample{Session: Session{PID: 3, State: "prompt"}, DesktopID: "two"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 4, State: "busy"}, DesktopID: "two"}},
		{Sample: watchSample{Session: Session{PID: 5, State: "unknown"}}},
	}
	rows, summaries := watchPanelLayout(entries, "one", []watchDesktop{{ID: "one", Name: "Desktop 1"}, {ID: "two", Name: "Desktop 2"}})
	if len(rows) != 1 || rows[0].Sample.PID != 1 {
		t.Fatalf("current desktop rows: %+v", rows)
	}
	if len(summaries) != 3 || summaries[1] != "Desktop 2 · 1 stopped · 1 working" {
		t.Fatalf("summaries: %v", summaries)
	}
}

func TestWatchPanelFitsTwelveAndBoundsScrolling(t *testing.T) {
	if n := watchPanelCapacity(1600, 2); n < 12 {
		t.Fatalf("only %d rows", n)
	}
	if n := watchPanelCapacity(600, 2); 34+n*watchRowHeight+2*28+watchKeyHeight+24 > 600 {
		t.Fatal("panel exceeds work area")
	}
}

func TestWatchPanelWakeIgnoresAgeAndBackground(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 1, State: "idle"}, Phase: "finished"}}}
	previous := watchPanelStates(rows)
	if watchPanelNeedsWake(previous, rows) {
		t.Fatal("unchanged status reopened panel")
	}
	rows[0].Sample.State = "prompt"
	if !watchPanelNeedsWake(previous, rows) {
		t.Fatal("changed status did not reopen panel")
	}
	if watchPanelNeedsWake(previous, nil) {
		t.Fatal("removal reopened panel")
	}
}
