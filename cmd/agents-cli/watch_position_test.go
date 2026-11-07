package main

import "testing"

func TestWatchSavedPositionAndMonitorRecovery(t *testing.T) {
	monitors := []watchMonitor{
		{Device: "main", Primary: true, Work: watchRect{0, 0, 1920, 1040}},
		{Device: "portrait", Work: watchRect{-1080, -400, 0, 1480}},
	}
	s := watchSettings{Monitor: "main", Position: &watchPoint{-900, -200}}
	m := watchPositionMonitor(monitors, s)
	if m.Device != "portrait" {
		t.Fatal("drag across monitors did not choose destination")
	}
	if got := watchPanelPosition(m.Work, 360, 500, s.Position); got != *s.Position {
		t.Fatalf("saved position moved: %v", got)
	}
	// A taller panel stays accessible, even when its old position was near the bottom.
	if got := watchPanelPosition(m.Work, 360, 900, &watchPoint{-200, 1200}); got != (watchPoint{-360, 580}) {
		t.Fatalf("bad clamp: %v", got)
	}
	// Removing a monitor recovers the window on the remaining display.
	m = watchPositionMonitor(monitors[:1], s)
	if got := watchPanelPosition(m.Work, 360, 500, s.Position); got != (watchPoint{0, 0}) {
		t.Fatalf("off-screen recovery: %v", got)
	}
	// Reset clears the custom position and restores the selected monitor's corner.
	s.Position = nil
	m = watchPositionMonitor(monitors, s)
	if got := watchPanelPosition(m.Work, 360, 500, s.Position); got != (watchPoint{1548, 528}) {
		t.Fatalf("reset: %v", got)
	}
}

func TestWatchPositionPersists(t *testing.T) {
	dir := t.TempDir()
	want := watchSettings{Monitor: "portrait", Position: &watchPoint{-900, 125}, ExcludedDesktops: []string{"two"}}
	if err := saveWatchSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	got := loadWatchSettings(dir)
	if got.Position == nil || *got.Position != *want.Position || got.Monitor != want.Monitor || len(got.ExcludedDesktops) != 1 {
		t.Fatalf("settings lost: %+v", got)
	}
}
