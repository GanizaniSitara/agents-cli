package main

import "testing"

func TestWatchDesktopFiltering(t *testing.T) {
	s := watchSettings{}
	for _, id := range []string{"one", "two"} {
		if !s.monitorsDesktop(id) {
			t.Fatal("all desktops should be enabled by default")
		}
	}
	s.toggleDesktop("two")
	if s.monitorsDesktop("two") || !s.monitorsDesktop("one") {
		t.Fatal("desktop exclusion incorrect")
	}
	s.toggleDesktop("one")
	if s.monitorsDesktop("") {
		t.Fatal("unknown desktop must not bypass exclusions")
	}
	s.toggleDesktop("two")
	if !s.monitorsDesktop("two") {
		t.Fatal("toggle did not restore desktop")
	}
	if s.monitorsDesktop("one") {
		t.Fatal("another toggle changed unrelated desktop")
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
