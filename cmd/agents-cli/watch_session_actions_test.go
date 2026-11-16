package main

import (
	"encoding/json"
	"testing"
	"time"
	"unsafe"
)

func TestWatchActionSelfResolution(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{Session: Session{PID: 1, AgentPID: 11, Tool: "codex"}}}, {Sample: watchSample{Session: Session{PID: 2, AgentPID: 22, Tool: "claude"}}}}
	command := func(pid uint32) string {
		if pid == 11 {
			return `"C:\path with spaces\codex.exe" resume exact-thread`
		}
		return ""
	}
	if got, err := watchResolveSelf(rows, 999, "exact-thread", command); err != nil || got.PID != 1 {
		t.Fatalf("daemon caller: %+v %v", got, err)
	}
	if got, err := watchResolveSelf(rows, 2, "", command); err != nil || got.PID != 2 {
		t.Fatalf("console caller: %+v %v", got, err)
	}
	if _, err := watchResolveSelf(rows, 2, "wrong-thread", command); err == nil {
		t.Fatal("unmatched thread fell back to an unrelated console")
	}
	rows = append(rows, rows[0])
	if _, err := watchResolveSelf(rows, 1, "exact-thread", command); err == nil {
		t.Fatal("ambiguous conversation accepted")
	}
	if _, err := watchResolveSelf(rows[:1], 999, "exact-thread", func(uint32) string { return `codex.exe exec "resume exact-thread"` }); err == nil {
		t.Fatal("historical command prose selected a session")
	}
}

func TestWatchSessionActionMessage(t *testing.T) {
	a := &watchApp{tracker: newWatchTracker(time.Second)}
	s := watchSample{Session: Session{PID: 7, State: "busy"}, RootStarted: "original", HWND: 8}
	a.tracker.observe(s, time.Now())
	b, _ := json.Marshal(watchSessionAction{Action: "park", Sample: s})
	data := watchCopyData{Tag: watchSessionActionTag, Size: uint32(len(b)), Data: uintptr(unsafe.Pointer(&b[0]))}
	if a.receiveSessionAction(uintptr(unsafe.Pointer(&data))) != 1 || !a.tracker.entries[7].Parked {
		t.Fatal("native message did not apply parking")
	}
	data.Tag++
	if a.receiveSessionAction(uintptr(unsafe.Pointer(&data))) != 0 {
		t.Fatal("unrelated copydata accepted")
	}
	data.Tag = watchSessionActionTag
	data.Size = 16385
	if a.receiveSessionAction(uintptr(unsafe.Pointer(&data))) != 0 {
		t.Fatal("oversize payload accepted")
	}
}

func TestWatchSessionActionsAreIdempotentAndIdentityChecked(t *testing.T) {
	a := &watchApp{tracker: newWatchTracker(time.Second)}
	s := watchSample{Session: Session{PID: 1, AgentPID: 2, State: "busy", Started: "original"}, RootStarted: "original", HWND: 3}
	a.tracker.observe(s, time.Now())
	for _, action := range []string{"park", "park", "unpark", "unpark"} {
		if !a.applySessionAction(watchSessionAction{Action: action, Sample: s}) {
			t.Fatal("action refused", action)
		}
		if a.tracker.entries[1].Parked != (action == "park") {
			t.Fatal("repeated action toggled parking")
		}
	}
	for _, change := range []func(*watchSample){func(s *watchSample) { s.RootStarted = "reused" }, func(s *watchSample) { s.AgentPID++ }, func(s *watchSample) { s.HWND++ }} {
		stale := s
		change(&stale)
		if a.applySessionAction(watchSessionAction{Action: "park", Sample: stale}) {
			t.Fatal("changed identity accepted")
		}
	}
	if a.applySessionAction(watchSessionAction{Action: "shelve", Sample: s}) {
		t.Fatal("parking IPC allowed a close action")
	}
}
