package main

import "testing"

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
