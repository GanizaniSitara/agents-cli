package main

import (
	"testing"
	"time"
)

func TestWatchAnimatedInputRequestStaysVisible(t *testing.T) {
	w := newWatchTracker(2 * time.Second)
	s := watchSample{Session: Session{PID: 4764, Tool: "vibe", State: "busy"}, DesktopID: "one", Phase: "working"}
	now := time.Now()
	w.observe(s, now)
	s.State, s.Phase = "prompt", "waiting"
	for i := 0; i < 5; i++ {
		s.Signature = string(rune('a' + i))
		if notify := w.observe(s, now.Add(time.Duration(i+1)*time.Second)); notify != (i == 0) {
			t.Fatalf("input notification at frame %d: %v", i, notify)
		}
		rows, _ := watchPanelLayout(w.panelRows(), "one", nil, nil)
		if len(rows) != 1 || rows[0].Sample.PID != s.PID || !rows[0].Stopped {
			t.Fatalf("animated permission request disappeared: %+v", rows)
		}
	}
	s.State, s.Phase = "busy", "working"
	w.observe(s, now.Add(6*time.Second))
	if w.entries[s.PID].Stopped || w.entries[s.PID].Attention {
		t.Fatal("cleared request retained input attention")
	}
}

func TestWatchPanelCurrentDesktopAndSummary(t *testing.T) {
	entries := []watchEntry{
		{Sample: watchSample{Session: Session{PID: 1, State: "idle"}, DesktopID: "one"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 2, State: "busy"}, DesktopID: "one"}},
		{Sample: watchSample{Session: Session{PID: 3, State: "prompt"}, DesktopID: "two"}, Stopped: true},
		{Sample: watchSample{Session: Session{PID: 4, State: "busy"}, DesktopID: "two"}},
		{Sample: watchSample{Session: Session{PID: 5, State: "unknown"}}},
	}
	rows, summaries := watchPanelLayout(entries, "one", []watchDesktop{{ID: "one", Name: "Desktop 1"}, {ID: "two", Name: "Desktop 2"}}, nil)
	if len(rows) != 2 || rows[0].Sample.PID != 1 || rows[1].Sample.PID != 2 {
		t.Fatalf("current desktop rows: %+v", rows)
	}
	if len(summaries) != 2 || summaries[0].Text != "[ ] Include Desktop 2 | 1 stopped | 1 working" {
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
	included := map[string]bool{"two": true}
	rows, summaries := watchPanelLayout(entries, "one", desktops, included)
	for i, pid := range []uint32{1, 2, 5, 4, 3} {
		if len(rows) != 5 || rows[i].Sample.PID != pid {
			t.Fatalf("included priority/stability: %+v", rows)
		}
		if rows[i].Sample.Desktop == "" {
			t.Fatal("desktop label lost")
		}
	}
	if !summaries[0].Included || summaries[0].DesktopID != "two" {
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
			t.Fatal("included background row started flashing")
		}
	})
	included["two"] = false
	rows, _ = watchPanelLayout(entries, "one", desktops, included)
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

func TestWatchRunningRowsStayBelowStoppedAndUnknown(t *testing.T) {
	entries := []watchEntry{
		{Sample: watchSample{Session: Session{PID: 1, State: "busy"}, DesktopID: "one", Phase: "working"}},
		{Sample: watchSample{Session: Session{PID: 2, State: "unknown"}, DesktopID: "one"}},
		{Sample: watchSample{Session: Session{PID: 3, State: "prompt"}, DesktopID: "one"}, Stopped: true},
	}
	rows, summaries := watchPanelLayout(entries, "one", []watchDesktop{{ID: "one", Name: "Desktop 1"}}, nil)
	if len(rows) != 3 || rows[0].Sample.PID != 3 || rows[1].Sample.PID != 2 || rows[2].Sample.PID != 1 {
		t.Fatalf("running row misplaced: %+v", rows)
	}
	if len(summaries) != 0 {
		t.Fatal("current running sessions should have named rows, not duplicate summary")
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
