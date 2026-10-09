package main

import (
	"testing"
	"time"
)

func TestMutedScreenNeverRead(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		s := &Session{PID: 42, Alive: true, PollingMuted: true}
		refreshScreen(s, enabled, func(uint32, int) (*peekResult, error) {
			t.Fatal("muted session console was accessed")
			return nil, nil
		})
		if s.State != "muted" || requirePolling(s) == nil {
			t.Fatal("mute must be visible and reject direct screen reads")
		}
	}
	s := &Session{PID: 42, Alive: true}
	calls := 0
	read := func(uint32, int) (*peekResult, error) {
		calls++
		return &peekResult{Title: "example", Lines: []string{"Working (1s"}}, nil
	}
	refreshScreen(s, false, read)
	if calls != 0 {
		t.Fatal("fast discovery read a screen")
	}
	refreshScreen(s, true, read)
	if calls != 1 || s.State != "busy" || requirePolling(s) != nil {
		t.Fatal("unmuted session should be readable")
	}
}

func TestMutePersistenceAndPIDReuse(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	started := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	a := adopted{Tool: "claude", Name: "example", Start: started.Format(time.RFC3339), PollingMuted: true}
	if err := writeAdopted(map[string]adopted{"42": a}); err != nil {
		t.Fatal(err)
	}
	m := readAdopted()
	got, ok := adoptedFor(m, 42, started)
	if !ok || !got.PollingMuted || got.Name != "example" {
		t.Fatal("mute/metadata not persisted")
	}
	if _, ok := adoptedFor(m, 42, started.Add(time.Hour)); ok {
		t.Fatal("mute applied to reused PID")
	}
	got.PollingMuted = false
	m["42"] = got
	if err := writeAdopted(m); err != nil {
		t.Fatal(err)
	}
	if readAdopted()["42"].PollingMuted {
		t.Fatal("unmute not persisted")
	}
}
