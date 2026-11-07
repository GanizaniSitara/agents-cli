package main

import "testing"

func TestTaskbarNeedsInputAndFinishedStates(t *testing.T) {
	entry := &watchEntry{Sample: watchSample{HWND: 11, DesktopID: "one", Phase: "waiting"}, Stopped: true}
	for phase, want := range map[string]uint32{"waiting": 8, "finished": 2, "error": 4, "idle": 0, "unknown": 0} {
		entry.Sample.Phase = phase
		if got := watchTaskbarState(entry, "one"); got != want {
			t.Fatalf("%s: %d", phase, got)
		}
		if watchTaskbarState(entry, "two") != 0 {
			t.Fatal("background desktop coloured")
		}
	}
}

func TestTaskbarAppliesOnlyChangesAndClears(t *testing.T) {
	c := watchTaskbarController{}
	entry := &watchEntry{Sample: watchSample{HWND: 11, DesktopID: "one", Phase: "waiting"}, Stopped: true}
	entries := map[uint32]*watchEntry{1: entry}
	var calls []uint32
	set := func(_ uintptr, state uint32) bool { calls = append(calls, state); return true }
	c.update(entries, "one", set)
	c.update(entries, "one", set)
	if len(calls) != 1 || calls[0] != 8 {
		t.Fatal(calls)
	}
	entry.Sample.Phase = "idle"
	c.update(entries, "one", set)
	if len(calls) != 2 || calls[1] != 0 {
		t.Fatal(calls)
	}
	entry.Sample.Phase = "finished"
	c.update(entries, "one", set)
	c.update(nil, "one", set)
	if len(calls) != 4 || calls[3] != 0 {
		t.Fatal(calls)
	}
}
