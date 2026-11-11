package main

import (
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
