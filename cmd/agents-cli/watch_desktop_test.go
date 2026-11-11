package main

import "testing"

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
