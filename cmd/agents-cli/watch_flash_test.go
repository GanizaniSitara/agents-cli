package main

import "testing"

func TestWatchFlashFollowsViewedDesktop(t *testing.T) {
	c := watchFlashController{}
	entries := map[uint32]*watchEntry{
		1: {Sample: watchSample{HWND: 11, DesktopID: "one", Phase: "waiting"}, Stopped: true},
		2: {Sample: watchSample{HWND: 22, DesktopID: "two", Phase: "waiting"}, Stopped: true, Attention: true},
	}
	actions := map[uintptr]bool{}
	flash := func(hwnd uintptr, on bool) { actions[hwnd] = on }
	c.update(entries, "one", flash)
	if actions[11] || actions[22] {
		t.Fatal("startup idle or off-desktop window flashed")
	}
	if on, ok := actions[22]; !ok || on {
		t.Fatal("existing off-desktop flash not cleared")
	}
	if !entries[2].Attention {
		t.Fatal("background stop lost pending attention")
	}
	actions = map[uintptr]bool{}
	c.update(entries, "two", flash)
	if !actions[22] {
		t.Fatal("pending stop did not flash on entering its desktop")
	}
	if actions[11] {
		t.Fatal("other desktop flashed on switch")
	}
	actions = map[uintptr]bool{}
	c.update(entries, "two", flash)
	if len(actions) != 0 {
		t.Fatal("unchanged state restarted flashing after owner may have read it")
	}
	c.update(entries, "one", flash)
	if !actions[11] || actions[22] {
		t.Fatal("desktop switch did not swap attention flags")
	}
	entries[1].Dismissed = true
	actions = map[uintptr]bool{}
	c.update(entries, "one", flash)
	if actions[11] {
		t.Fatal("dismissed session still flashing")
	}
	actions = map[uintptr]bool{}
	c.update(entries, "", flash)
	for _, on := range actions {
		if on {
			t.Fatal("unknown current desktop must never flash")
		}
	}
}

func TestWatchFlashClearsResumedRemovedAndUnknownDesktop(t *testing.T) {
	c := watchFlashController{}
	entry := &watchEntry{Sample: watchSample{HWND: 11, DesktopID: "one", Phase: "waiting"}, Stopped: true, Attention: true}
	entries := map[uint32]*watchEntry{1: entry}
	actions := map[uintptr]bool{}
	flash := func(hwnd uintptr, on bool) { actions[hwnd] = on }
	c.update(entries, "one", flash)
	if !actions[11] {
		t.Fatal("current desktop stop not flashed")
	}
	entry.Stopped = false
	c.update(entries, "one", flash)
	if actions[11] {
		t.Fatal("resumed window flash not cleared")
	}
	entry.Stopped, entry.Attention = true, true
	entry.Sample.DesktopID = ""
	c.update(entries, "one", flash)
	if actions[11] {
		t.Fatal("unknown window desktop flashed")
	}
	entry.Sample.DesktopID = "one"
	c.update(entries, "one", flash)
	c.update(nil, "one", flash)
	if actions[11] {
		t.Fatal("removed window flash not cleared")
	}
}

func TestIdleAndFinishedNeverFlash(t *testing.T) {
	c := watchFlashController{}
	entry := &watchEntry{Sample: watchSample{HWND: 11, DesktopID: "one", Phase: "waiting"}, Stopped: true, Attention: true}
	entries := map[uint32]*watchEntry{1: entry}
	on := false
	flash := func(_ uintptr, value bool) { on = value }
	c.update(entries, "one", flash)
	if !on {
		t.Fatal("needs input did not flash")
	}
	for _, phase := range []string{"idle", "finished", "unknown", "error"} {
		entry.Sample.Phase = phase
		c.update(entries, "one", flash)
		if on {
			t.Fatalf("%s flashed", phase)
		}
	}
}
