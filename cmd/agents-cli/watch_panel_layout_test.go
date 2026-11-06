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
	rows, summaries := watchPanelLayout(entries, "one", []watchDesktop{{ID: "one", Name: "Desktop 1"}, {ID: "two", Name: "Desktop 2"}}, nil)
	if len(rows) != 1 || rows[0].Sample.PID != 1 {
		t.Fatalf("current desktop rows: %+v", rows)
	}
	if len(summaries) != 3 || summaries[1].Text != "▸ Desktop 2 · 1 stopped · 1 working" {
		t.Fatalf("summaries: %v", summaries)
	}
}

func TestWatchPanelExpandDesktopWithoutChangingAttention(t *testing.T) {
	entries := []watchEntry{
		{Sample: watchSample{Session: Session{PID: 1, State: "prompt"}, DesktopID: "one", Desktop: "Desktop 1"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 2, State: "prompt"}, DesktopID: "two", Desktop: "Desktop 2"}, Stopped: true, Attention: true},
		{Sample: watchSample{Session: Session{PID: 3, State: "busy"}, DesktopID: "two", Desktop: "Desktop 2"}},
		{Sample: watchSample{Session: Session{PID: 4, State: "idle"}, DesktopID: "two", Desktop: "Desktop 2"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 5, State: "prompt"}, DesktopID: "two", Desktop: "Desktop 2"}, Stopped: true},
	}
	desktops := []watchDesktop{{ID: "one", Name: "Desktop 1"}, {ID: "two", Name: "Desktop 2"}}
	expanded := map[string]bool{"two": true}
	rows, summaries := watchPanelLayout(entries, "one", desktops, expanded)
	for i, pid := range []uint32{1, 2, 5, 4, 3} {
		if len(rows) != 5 || rows[i].Sample.PID != pid {
			t.Fatalf("expanded priority/stability: %+v", rows)
		}
		if rows[i].Sample.Desktop == "" {
			t.Fatal("desktop label lost")
		}
	}
	if !summaries[0].Expanded || summaries[0].DesktopID != "two" {
		t.Fatal("summary cannot identify collapse target")
	}
	if !entries[1].Attention {
		t.Fatal("view expansion changed pending flash state")
	}
	flashes := watchFlashController{}
	flashEntries := map[uint32]*watchEntry{}
	for i := range entries {
		flashEntries[entries[i].Sample.PID] = &entries[i]
		entries[i].Sample.HWND = uintptr(entries[i].Sample.PID)
	}
	flashes.update(flashEntries, "one", func(hwnd uintptr, on bool) {
		if hwnd != 1 && on {
			t.Fatal("expanded background row started flashing")
		}
	})
	expanded["two"] = false
	rows, _ = watchPanelLayout(entries, "one", desktops, expanded)
	if len(rows) != 1 || rows[0].Sample.PID != 1 {
		t.Fatal("collapse retained background rows")
	}
}

func TestWatchPanelSummaryHitUsesVisibleRows(t *testing.T) {
	// Twenty sessions with room for three: summaries are pinned after the
	// three visible rows, not below all twenty off-screen sessions.
	for _, y := range []int{0, 33, 34, 201} {
		if watchPanelSummaryIndex(y, 20, 3, 2) != -1 {
			t.Fatalf("session/header mistaken for summary: %d", y)
		}
	}
	if watchPanelSummaryIndex(202, 20, 3, 2) != 0 || watchPanelSummaryIndex(230, 20, 3, 2) != 1 || watchPanelSummaryIndex(258, 20, 3, 2) != -1 {
		t.Fatal("summary hit bounds do not match scrolling viewport")
	}
}

func TestWatchPanelFitsTwelveAndBoundsScrolling(t *testing.T) {
	if n := watchPanelCapacity(1600, 2); n < 12 {
		t.Fatalf("only %d rows", n)
	}
	if n := watchPanelCapacity(600, 2); 34+n*watchRowHeight+2*28+24 > 600 {
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
