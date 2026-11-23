package main

import (
	"errors"
	"testing"
	"time"
)

func TestWatchDesktopCacheRetainsLastKnownStateDuringWorkerRecovery(t *testing.T) {
	a := &watchApp{}
	if a.currentDesktop() != "" || a.desktopList() != nil {
		t.Fatal("startup desktop was guessed")
	}
	batch := watchBatch{CurrentDesktop: "one", Desktops: []watchDesktop{{ID: "one", Name: "Desktop 1"}}}
	published := 0
	if err := watchPublishProgress(batch, &published, func(value any) error {
		a.acceptDesktopState(value.(watchBatch))
		return nil
	}, func(time.Duration) bool { return true }); err != nil {
		t.Fatal(err)
	}
	batch.Desktops[0].Name = "modified after publication"
	a.acceptDesktopState(watchBatch{Stage: "desktop"})
	if a.currentDesktop() != "one" || a.desktopList()[0].Name != "Desktop 1" {
		t.Fatal("partial/recovery update lost or mutated known desktop")
	}
}

func TestWatchDesktopWorkerTimeoutLeavesCacheReadable(t *testing.T) {
	t.Setenv("AGENTS_CLI_TEST_READER_ERROR", "desktop-watchdog")
	a := &watchApp{dir: t.TempDir(), stop: make(chan struct{}), tracker: newWatchTracker(time.Second)}
	err := a.readWorker()
	failure, ok := err.(*watchReaderError)
	if !ok || failure.Code != "WR104" || a.readerStage != "desktop" {
		t.Fatalf("desktop watchdog diagnostics lost: %v / %s", err, a.readerStage)
	}
	if a.currentDesktop() != "one" || len(a.desktopList()) != 1 {
		t.Fatal("worker timeout prevented cached UI desktop reads")
	}
}

func TestWatchDesktopInclusionPersists(t *testing.T) {
	s := watchSettings{}
	if s.includedDesktops()["two"] {
		t.Fatal("background desktop should start excluded from view")
	}
	s.toggleDesktop("two")
	if !s.includedDesktops()["two"] {
		t.Fatal("include did not reveal desktop")
	}
	dir := t.TempDir()
	if err := saveWatchSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	loaded := loadWatchSettings(dir)
	if !loaded.includedDesktops()["two"] {
		t.Fatal("include did not survive restart")
	}
	loaded.toggleDesktop("two")
	if loaded.includedDesktops()["two"] {
		t.Fatal("uncheck did not hide desktop")
	}
	for _, pair := range [][2]string{{"two", "one"}, {"", "one"}, {"one", ""}} {
		if watchDesktopSelectable(pair[0], pair[1]) {
			t.Fatal("display-only/unknown desktop became activatable")
		}
	}
	if !watchDesktopSelectable("one", "one") {
		t.Fatal("current desktop cannot activate")
	}
}

func TestWatchMonitorFallback(t *testing.T) {
	ms := []watchMonitor{{Device: "secondary"}, {Device: "primary", Primary: true}}
	if selectWatchMonitor(ms, "missing").Device != "primary" {
		t.Fatal("disconnected monitor should fall back to primary")
	}
	if selectWatchMonitor(ms, "secondary").Device != "secondary" {
		t.Fatal("selected monitor ignored")
	}
}

func TestWatchDesktopPullNeeded(t *testing.T) {
	if !watchDesktopPullNeeded("two", "one") {
		t.Fatal("other-desktop row needs a pull on explicit selection")
	}
	for _, pair := range [][2]string{{"one", "one"}, {"", "one"}, {"two", ""}, {"", ""}} {
		if watchDesktopPullNeeded(pair[0], pair[1]) {
			t.Fatalf("no pull expected for %v", pair)
		}
	}
}

func TestPullThenForegroundOrdering(t *testing.T) {
	var calls []string
	move := func(hwnd uintptr, desktopID string) error {
		if hwnd != 77 || desktopID != "one" {
			t.Fatalf("move target = %d %q", hwnd, desktopID)
		}
		calls = append(calls, "move")
		return nil
	}
	foreground := func(hwnd uintptr) bool {
		if hwnd != 77 {
			t.Fatalf("foreground target = %d", hwnd)
		}
		calls = append(calls, "foreground")
		return true
	}
	pulled, proceeded := watchPullAndForeground(77, "two", "one", move, foreground)
	if !pulled || !proceeded || len(calls) != 2 || calls[0] != "move" || calls[1] != "foreground" {
		t.Fatalf("other-desktop row must move-then-foreground: pulled=%v proceeded=%v calls=%v", pulled, proceeded, calls)
	}
	calls = nil
	pulled, proceeded = watchPullAndForeground(77, "one", "one", move, foreground)
	if pulled || !proceeded || len(calls) != 1 || calls[0] != "foreground" {
		t.Fatalf("current-desktop row must not move: pulled=%v proceeded=%v calls=%v", pulled, proceeded, calls)
	}
	calls = nil
	failMove := func(uintptr, string) error { calls = append(calls, "move"); return errors.New("denied") }
	pulled, proceeded = watchPullAndForeground(77, "two", "one", failMove, foreground)
	if pulled || proceeded || len(calls) != 1 {
		t.Fatalf("failed move must stop before foreground: pulled=%v proceeded=%v calls=%v", pulled, proceeded, calls)
	}
}

func TestBackgroundRefreshNeverMovesWindows(t *testing.T) {
	a := &watchApp{dir: t.TempDir(), tracker: newWatchTracker(time.Second)}
	moved := 0
	a.moveWindowToDesktop = func(hwnd uintptr, desktopID string) error {
		moved++
		return nil
	}
	// Exercise the background refresh paths: worker batch acceptance and
	// panel layout over rows that live on another desktop.
	a.acceptDesktopState(watchBatch{CurrentDesktop: "one", Desktops: []watchDesktop{{ID: "one", Name: "Desktop 1"}, {ID: "two", Name: "Desktop 2"}}})
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 9, State: "idle"}, DesktopID: "two", HWND: 99}}}
	watchPanelLayout(rows, a.currentDesktop(), a.desktopList(), map[string]bool{})
	if moved != 0 {
		t.Fatal("background refresh moved a window across desktops")
	}
}

func TestWatchVerifiedCurrentDesktop(t *testing.T) {
	desktops := []watchDesktop{{ID: "one"}, {ID: "two"}}
	for _, tc := range []struct {
		hwnd    uintptr
		id      string
		current bool
		want    string
	}{
		{1, "two", true, "two"},
		{1, "two", false, ""},
		{1, "unlisted", true, ""},
		{0, "two", true, ""},
	} {
		got := watchVerifiedCurrentDesktop(tc.hwnd, desktops, func(uintptr) (string, bool) { return tc.id, tc.current })
		if got != tc.want {
			t.Fatalf("%+v: got %q", tc, got)
		}
	}
}
