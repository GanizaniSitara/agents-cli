package main

import "testing"

func TestPinAndDesktopParkingLayout(t *testing.T) {
	pinned := watchEntry{Pinned: true, Sample: watchSample{Session: Session{PID: 1, State: "busy"}, DesktopID: "one"}}
	parked := watchEntry{Parked: true, ParkOrigin: "one", Sample: watchSample{Session: Session{PID: 2}, DesktopID: "two"}}
	other := watchEntry{Sample: watchSample{Session: Session{PID: 3}, DesktopID: "one"}}
	rows, _ := watchPanelLayout([]watchEntry{parked, other, pinned}, "one", nil, nil)
	if len(rows) != 3 || !rows[0].Pinned || !rows[2].Parked {
		t.Fatal(rows)
	}
	tracker := newWatchTracker(0)
	tracker.entries[1] = &pinned
	if tracker.togglePark(pinned.Sample) || pinned.Parked {
		t.Fatal("parked pinned session")
	}
}
