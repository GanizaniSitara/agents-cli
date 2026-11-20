package main

import "testing"

func TestThreeTerminalTabsKeepInactiveCopilotStatus(t *testing.T) {
	rows := []watchEntry{}
	for i, title := range []string{"first", "second", "third"} {
		rows = append(rows, watchEntry{Stopped: true, Sample: watchSample{HWND: 42, Phase: "waiting", Session: Session{PID: uint32(101 + i), Tool: "copilot", Title: title, State: "prompt"}}})
	}
	for i, title := range []string{"first", "second", "third", "unmatched"} {
		result := watchActiveTabRows(rows, 42, title, true)
		if len(result) != 3 {
			t.Fatal("missing tab")
		}
		for j, r := range result {
			if (r.Sample.Phase == "active") != (i == j) || r.Stopped != (i != j) {
				t.Fatalf("selected %s: row %d: %+v", title, j, r)
			}
		}
	}
	rows[2].Sample.Title = "first"
	for _, r := range watchActiveTabRows(rows, 42, "first", true) {
		if !r.Stopped {
			t.Fatal("ambiguous tab hid an input request")
		}
	}
}

func TestActiveWindowDoesNotShowWaitingAlert(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{HWND: 42, Phase: "waiting", Session: Session{State: "prompt"}}, Stopped: true}}
	active := watchActiveRows(rows, 42)
	if active[0].Sample.Phase != "active" || active[0].Stopped {
		t.Fatal("foreground window still shows an attention alert")
	}
	if rows[0].Sample.Phase != "waiting" || !rows[0].Stopped {
		t.Fatal("presentation changed underlying request")
	}
	if watchActiveRows(rows, 43)[0].Sample.Phase != "waiting" {
		t.Fatal("background request was hidden")
	}
}

func TestActiveBusyAGYPreservesBackgroundWork(t *testing.T) {
	rows := []watchEntry{{Sample: watchSample{HWND: 42, Phase: "working", WorkProcesses: 1, Session: Session{Tool: "agy", Title: "chrome-devtools-mcp", State: "busy"}}}}
	active := watchActiveRows(rows, 42)
	s := active[0].Sample
	if s.Phase != "active" || s.State != "busy" || !s.Working || s.WorkProcesses != 1 || watchProminentStatus(s.Phase) != "ACTIVE" {
		t.Fatalf("focused busy AGY not active, or lost work: %+v", s)
	}
	if got := watchRowActivity(s); got != "working | agents: unknown" {
		t.Fatalf("concurrent work not visible: %q", got)
	}
	if rows[0].Sample.Phase != "working" || rows[0].Sample.Working || watchActiveRows(rows, 43)[0].Sample.Phase != "working" {
		t.Fatal("foreground presentation changed background work state")
	}
	for _, phase := range []string{"error", "blocked"} {
		bad := []watchEntry{{Sample: watchSample{HWND: 42, Phase: phase, Session: Session{State: "busy"}}}}
		if watchActiveRows(bad, 42)[0].Sample.Phase != phase {
			t.Fatalf("hid %s", phase)
		}
	}
	rows[0].Parked = true
	if watchActiveRows(rows, 42)[0].Sample.Phase == "active" {
		t.Fatal("parked row became active")
	}
}
